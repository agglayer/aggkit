package sources

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// This file is the regression suite for inconsistent upstream data over a fake JSON-RPC endpoint: the
// origin network answers eth_getTransactionReceipt, eth_getBlockByNumber and eth_getBlockByHash
// from a mutable fake chain, and a real etherman client decodes its JSON, so the tests exercise
// FindBridge (and ResolveBridgeTx on top of it) exactly as the tracker does against a node
// answering a receipt, a log or a header that do not describe one same block.
//
// The tests only rely on the observable behavior of FindBridge (a BridgeInfo, or an error and no
// BridgeInfo), so they also run on a tree without the fix, where the contradictory answers are
// accepted: that is what makes them a proof (errInvalidUpstreamData is the only symbol of the fix;
// jsonRPCReceipt, the serialization of a receipt, is the helper of bridge_event_test.go).

// errInvalidUpstreamData is the error FindBridge returns for contradictory upstream data
var errInvalidUpstreamData = bridgetracker.ErrInvalidUpstreamData

const (
	// upstreamBlockNumber is the block the fake chain mines the bridge tx in
	upstreamBlockNumber = uint64(12345)
	// upstreamBlockTime is the timestamp of upstreamBlockNumber's block (testBlockTimestamp)
	upstreamBlockTime = testBlockTimestamp
	// upstreamOtherBlockTime is the timestamp of the competing block of the same height: it tells
	// which block FindBridge took its BlockTimestamp from
	upstreamOtherBlockTime = testBlockTimestamp + 12
)

var (
	// upstreamBlockHashA is the hash of the block the bridge tx is mined in (testBlockHash)
	upstreamBlockHashA = testBlockHash
	// upstreamBlockHashB is the hash of the competing block of the same height (0xBBBB of the reported case)
	upstreamBlockHashB = common.HexToHash("0xbbbb567890123456789012345678901234567890123456789012345678901234")
	// upstreamOtherTxHash is the hash of a tx that is not the tracked one
	upstreamOtherTxHash = common.HexToHash("0xdead")
)

// upstreamBlock is a block of the fake chain: its header as a node serializes it, and the hash the
// node reports for it
type upstreamBlock struct {
	header *gethtypes.Header
	hash   common.Hash
}

// newUpstreamBlock returns a block whose reported hash is hash, arbitrary (not the RLP hash of
// its header), as on a chain with non-standard headers
func newUpstreamBlock(number uint64, hash common.Hash, timestamp uint64) upstreamBlock {
	return upstreamBlock{
		header: &gethtypes.Header{
			Number: new(big.Int).SetUint64(number), Time: timestamp, Difficulty: new(big.Int),
			Extra: hash.Bytes(),
		},
		hash: hash,
	}
}

// newRealHashUpstreamBlock returns a block whose reported hash is the RLP hash of its header, as
// on an Ethereum chain; salt makes the headers (hence the hashes) of same-height blocks differ
func newRealHashUpstreamBlock(number, timestamp uint64, salt byte) upstreamBlock {
	block := newUpstreamBlock(number, common.Hash{}, timestamp)
	block.header.Extra = []byte{salt}
	block.hash = block.header.Hash()
	return block
}

// rpcJSON is the eth_getBlockByNumber/eth_getBlockByHash result of the block: a full header (so
// that go-ethereum decodes it too) whose "hash" is the one the node reports
func (b upstreamBlock) rpcJSON() (map[string]any, error) {
	raw, err := json.Marshal(b.header)
	if err != nil {
		return nil, err
	}
	var block map[string]any
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, err
	}
	block["hash"] = b.hash.Hex()
	return block, nil
}

// upstreamChain is the state the fake endpoint answers from
type upstreamChain struct {
	// receipt is the answer for the tracked tx, nil for "not found"
	receipt *gethtypes.Receipt
	// finalized is the block the "finalized" tag points to
	finalized upstreamBlock
	// canonical is the block at each height, as eth_getBlockByNumber answers it
	canonical map[uint64]upstreamBlock
	// byHash is the block of each hash, as eth_getBlockByHash answers it (a block reorged out
	// stays there: nodes keep serving side blocks by hash)
	byHash map[common.Hash]upstreamBlock
}

// setCanonical makes block the answer for its height
func (c *upstreamChain) setCanonical(block upstreamBlock) {
	c.canonical[block.header.Number.Uint64()] = block
}

// setByHash makes block the answer for its hash
func (c *upstreamChain) setByHash(block upstreamBlock) {
	c.byHash[block.hash] = block
}

// upstreamBridgeLog is the BridgeEvent log (to destinationNetwork, deposit count 7) of the tracked
// tx, mined in blockA at upstreamBlockNumber
func upstreamBridgeLog(t *testing.T, destinationNetwork uint32, blockA upstreamBlock) *gethtypes.Log {
	t.Helper()

	l := bridgeEventLog(t, destinationNetwork, 7)
	l.BlockNumber = upstreamBlockNumber
	l.BlockHash = blockA.hash
	l.TxHash = testTxHash
	return l
}

// coherentUpstreamChain is a chain in which everything agrees: the receipt of testTxHash, mined
// in blockA at upstreamBlockNumber with a BridgeEvent log of the same block and tx, which is the
// canonical block of its height and well below the finalized one
func coherentUpstreamChain(t *testing.T, blockA upstreamBlock) *upstreamChain {
	t.Helper()

	return &upstreamChain{
		receipt: &gethtypes.Receipt{
			Status:      gethtypes.ReceiptStatusSuccessful,
			BlockNumber: new(big.Int).SetUint64(upstreamBlockNumber),
			BlockHash:   blockA.hash,
			TxHash:      testTxHash,
			Logs:        []*gethtypes.Log{upstreamBridgeLog(t, 1, blockA)},
		},
		finalized: newUpstreamBlock(upstreamBlockNumber+10, common.HexToHash("0xf1"), upstreamBlockTime+120),
		canonical: map[uint64]upstreamBlock{upstreamBlockNumber: blockA},
		byHash:    map[common.Hash]upstreamBlock{blockA.hash: blockA},
	}
}

// fakeUpstream is an httptest JSON-RPC endpoint answering from an upstreamChain that a test can
// change while the endpoint is being used (a reorg between two calls, an endpoint recovering...)
type fakeUpstream struct {
	url string

	mu    sync.Mutex
	chain *upstreamChain
}

// newFakeUpstream serves chain over HTTP until the test ends
func newFakeUpstream(t *testing.T, chain *upstreamChain) *fakeUpstream {
	t.Helper()

	upstream := &fakeUpstream{chain: chain}
	server := httptest.NewServer(http.HandlerFunc(upstream.serve))
	t.Cleanup(server.Close)
	upstream.url = server.URL
	return upstream
}

// mutate changes the chain the endpoint answers from, between two of the tracker's calls
func (u *fakeUpstream) mutate(change func(chain *upstreamChain)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	change(u.chain)
}

// answer returns the result of one JSON-RPC call (nil is JSON null: not found)
func (u *fakeUpstream) answer(method string, params []json.RawMessage) (any, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	var arg string
	if len(params) > 0 {
		if err := json.Unmarshal(params[0], &arg); err != nil {
			return nil, err
		}
	}
	var block *upstreamBlock
	switch method {
	case "eth_getTransactionReceipt":
		if u.chain.receipt == nil || !strings.EqualFold(arg, testTxHash.Hex()) {
			return nil, nil //nolint:nilnil // JSON null
		}
		return jsonRPCReceipt(u.chain.receipt), nil
	case "eth_getBlockByNumber":
		if arg == "finalized" {
			block = &u.chain.finalized
		} else if number, err := hexutil.DecodeUint64(arg); err == nil {
			if b, ok := u.chain.canonical[number]; ok {
				block = &b
			}
		}
	case "eth_getBlockByHash":
		if b, ok := u.chain.byHash[common.HexToHash(arg)]; ok {
			block = &b
		}
	default:
		return nil, errors.New("method not found: " + method)
	}
	if block == nil {
		return nil, nil //nolint:nilnil // JSON null
	}
	return block.rpcJSON()
}

func (u *fakeUpstream) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	if result, err := u.answer(req.Method, req.Params); err != nil {
		response["error"] = map[string]any{"code": -32601, "message": err.Error()}
	} else {
		response["result"] = result
	}
	body, err := json.Marshal(response)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// upstreamFlavour is a kind of chain the fake endpoint can mimic
type upstreamFlavour struct {
	name string
	// hashFromJSON is the client's HashFromJSON: the block hash it reports is the node's own
	// (the proxy default) or the RLP hash of the decoded header
	hashFromJSON bool
	// blocks returns the block the tx is mined in (A) and a competing block of the same height
	// (B), with different hashes and timestamps
	blocks func() (blockA, blockB upstreamBlock)
}

var upstreamFlavours = []upstreamFlavour{
	{
		name:         "node-reported hashes (HashFromJSON)",
		hashFromJSON: true,
		blocks: func() (upstreamBlock, upstreamBlock) {
			return newUpstreamBlock(upstreamBlockNumber, upstreamBlockHashA, upstreamBlockTime),
				newUpstreamBlock(upstreamBlockNumber, upstreamBlockHashB, upstreamOtherBlockTime)
		},
	},
	{
		name:         "RLP hashes (no HashFromJSON)",
		hashFromJSON: false,
		blocks: func() (upstreamBlock, upstreamBlock) {
			return newRealHashUpstreamBlock(upstreamBlockNumber, upstreamBlockTime, 0xa),
				newRealHashUpstreamBlock(upstreamBlockNumber, upstreamOtherBlockTime, 0xb)
		},
	},
}

// newUpstreamSource returns a BridgeEventSource of network 0 over a real etherman client of
// upstream: see newUpstreamNetworkSource
func newUpstreamSource(t *testing.T, upstream *fakeUpstream, hashFromJSON bool) *BridgeEventSource {
	t.Helper()

	return newUpstreamNetworkSource(t, upstream, 0, hashFromJSON)
}

// newUpstreamNetworkSource returns a BridgeEventSource of networkID (its only network, whose
// canonical bridge is testBridgeAddr, with finalized finality) over a real etherman client of
// upstream
func newUpstreamNetworkSource(
	t *testing.T, upstream *fakeUpstream, networkID uint32, hashFromJSON bool,
) *BridgeEventSource {
	t.Helper()

	cfg := ethermanconfig.NewDefaultRPCClientConfig()
	cfg.URL = upstream.url
	cfg.HashFromJSON = hashFromJSON
	client, err := etherman.NewRPCClient(t.Context(), log.WithFields("module", "bridge_event_upstream_test"), *cfg)
	require.NoError(t, err)

	source, err := NewBridgeEventSource(
		StaticClients{networkID: client}, aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock, nil,
		staticBridgeAddressResolver{addrs: map[uint32]common.Address{networkID: testBridgeAddr}})
	require.NoError(t, err)
	return source
}

// upstreamTxID is the tracked bridge tx of these tests
var upstreamTxID = bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash}

// expectedUpstreamBridge is the BridgeInfo of a bridge accepted from coherentUpstreamChain(blockA)
func expectedUpstreamBridge(blockA upstreamBlock) *bridgetracker.BridgeInfo {
	info := l1ToL2Bridge()
	info.BlockHash = blockA.hash
	info.BlockTimestamp = blockA.header.Time
	return info
}

// requireRejectedUpstreamData asserts that FindBridge refused the upstream's answer as a whole: no
// BridgeInfo (the bridge must not be tracked nor resolved from it) and a transient error (the
// engine retries: a defective or out-of-sync upstream may answer coherently later). When
// invalidUpstream is true the error must be ErrInvalidUpstreamData itself
func requireRejectedUpstreamData(t *testing.T, info *bridgetracker.BridgeInfo, err error, invalidUpstream bool) {
	t.Helper()

	require.Nil(t, info, "no BridgeInfo may come out of inconsistent upstream data")
	require.Error(t, err)
	if invalidUpstream {
		require.ErrorIs(t, err, errInvalidUpstreamData)
	}
	require.False(t, domain.IsPermanent(err), "a defective upstream may recover: the engine must retry: %v", err)
	require.NotErrorIs(t, err, bridgetracker.ErrBridgeTxNotABridge)
	require.NotErrorIs(t, err, bridgetracker.ErrBridgeTxNotFound)
}

// upstreamInconsistency is a way for the upstream to contradict itself, as the table-driven tests
// of this file apply it to a coherent chain
type upstreamInconsistency struct {
	name string
	// tamper turns the coherent chain into the contradictory one; blockA is the block the tx is
	// mined in, blockB a competing block of the same height
	tamper func(chain *upstreamChain, blockA, blockB upstreamBlock)
	// invalidUpstream is whether FindBridge must report it as ErrInvalidUpstreamData; false when
	// the failure is of another (still transient) kind
	invalidUpstream bool
}

// upstreamInconsistencies lists the contradictions the fix refuses: the
// receipt, the BridgeEvent log and the header of the block must agree on the block hash, the
// block number and the tx
var upstreamInconsistencies = []upstreamInconsistency{
	{
		// receipt.blockHash=0xAAAA, log.blockHash=0xBBBB, with the log's block served by the node
		name: "receipt.blockHash is A, log.blockHash is B",
		tamper: func(chain *upstreamChain, _, blockB upstreamBlock) {
			chain.receipt.Logs[0].BlockHash = blockB.hash
			chain.setByHash(blockB)
		},
		invalidUpstream: true,
	},
	{
		// same, and the node's canonical block of that height is the log's block too: everything
		// the timestamp resolution needs is served for the log-selected block
		name: "receipt.blockHash is A, log.blockHash is B, and B is canonical",
		tamper: func(chain *upstreamChain, _, blockB upstreamBlock) {
			chain.receipt.Logs[0].BlockHash = blockB.hash
			chain.setByHash(blockB)
			chain.setCanonical(blockB)
		},
		invalidUpstream: true,
	},
	{
		name: "receipt.blockHash is B, log.blockHash is A",
		tamper: func(chain *upstreamChain, _, blockB upstreamBlock) {
			chain.receipt.BlockHash = blockB.hash
			chain.setByHash(blockB)
		},
		invalidUpstream: true,
	},
	{
		name: "log.blockNumber differs from receipt.blockNumber",
		tamper: func(chain *upstreamChain, _, _ upstreamBlock) {
			chain.receipt.Logs[0].BlockNumber = upstreamBlockNumber + 1
		},
		invalidUpstream: true,
	},
	{
		name: "receipt.blockNumber differs from log.blockNumber",
		tamper: func(chain *upstreamChain, _, _ upstreamBlock) {
			chain.receipt.BlockNumber = new(big.Int).SetUint64(upstreamBlockNumber - 1)
		},
		invalidUpstream: true,
	},
	{
		name: "log.transactionHash is not the tracked tx",
		tamper: func(chain *upstreamChain, _, _ upstreamBlock) {
			chain.receipt.Logs[0].TxHash = upstreamOtherTxHash
		},
		invalidUpstream: true,
	},
	{
		// the receipt and its log agree, but a reorg between the receipt and the header calls made
		// another block the canonical one of that height: the receipt's block is only a side
		// block, which the node still serves by hash
		name: "the canonical block of the receipt's height is another block (reorg between calls)",
		tamper: func(chain *upstreamChain, _, blockB upstreamBlock) {
			chain.setCanonical(blockB)
		},
		invalidUpstream: true,
	},
	{
		name: "the header of the receipt's height has another number",
		tamper: func(chain *upstreamChain, blockA, _ upstreamBlock) {
			shifted := blockA
			shifted.header = gethtypes.CopyHeader(blockA.header)
			shifted.header.Number = new(big.Int).SetUint64(upstreamBlockNumber + 1)
			chain.canonical[upstreamBlockNumber] = shifted
		},
		invalidUpstream: true,
	},
	{
		// not an inconsistency verdict, but the bridge must not be resolved from the receipt alone
		name: "the node has no block at the receipt's height",
		tamper: func(chain *upstreamChain, _, _ upstreamBlock) {
			delete(chain.canonical, upstreamBlockNumber)
		},
		invalidUpstream: false,
	},
}

// TestBridgeEventSourceRejectsInconsistentUpstreamJSON is the regression test for
// inconsistent upstream data: a receipt, BridgeEvent log and block header that do not describe one same
// block are refused as a whole, with a transient error and no BridgeInfo — never accepted as the
// bridge's facts. The endpoint serves real JSON, decoded by the real RPC client
func TestBridgeEventSourceRejectsInconsistentUpstreamJSON(t *testing.T) {
	for _, flavour := range upstreamFlavours {
		t.Run(flavour.name, func(t *testing.T) {
			for _, tc := range upstreamInconsistencies {
				t.Run(tc.name, func(t *testing.T) {
					blockA, blockB := flavour.blocks()
					chain := coherentUpstreamChain(t, blockA)
					tc.tamper(chain, blockA, blockB)
					source := newUpstreamSource(t, newFakeUpstream(t, chain), flavour.hashFromJSON)

					info, err := source.FindBridge(t.Context(), upstreamTxID)
					requireRejectedUpstreamData(t, info, err, tc.invalidUpstream)
				})
			}
		})
	}
}

// TestBridgeEventSourceAcceptsCoherentUpstreamJSON is the counterpart of the rejection test: when
// the receipt, its log and the header agree the bridge is accepted, with the facts and the
// timestamp of that block, whether the client reports the node's own block hash or the RLP hash
// of the header
func TestBridgeEventSourceAcceptsCoherentUpstreamJSON(t *testing.T) {
	for _, flavour := range upstreamFlavours {
		t.Run(flavour.name, func(t *testing.T) {
			blockA, blockB := flavour.blocks()
			chain := coherentUpstreamChain(t, blockA)
			// a competing side block known by hash must not interfere
			chain.setByHash(blockB)
			source := newUpstreamSource(t, newFakeUpstream(t, chain), flavour.hashFromJSON)

			info, err := source.FindBridge(t.Context(), upstreamTxID)
			require.NoError(t, err)
			require.Equal(t, expectedUpstreamBridge(blockA), info)
			require.Equal(t, upstreamBlockTime, info.BlockTimestamp, "the timestamp is the receipt's block's")
		})
	}
}

// TestBridgeEventSourceUpstreamOtherControlsAreKept checks that the consistency checks do not
// replace the complementary ones: an impostor emitter is still rejected (permanently, as not a
// bridge) whatever its block fields say, and an honestly reported lack of finality still leaves
// the bridge pending, even when the receipt is also contradictory
func TestBridgeEventSourceUpstreamOtherControlsAreKept(t *testing.T) {
	blockA, blockB := upstreamFlavours[0].blocks()

	t.Run("impostor emitter with coherent fields", func(t *testing.T) {
		chain := coherentUpstreamChain(t, blockA)
		chain.receipt.Logs[0].Address = common.HexToAddress("0xBAD")
		source := newUpstreamSource(t, newFakeUpstream(t, chain), true)

		info, err := source.FindBridge(t.Context(), upstreamTxID)
		require.Nil(t, info)
		require.ErrorIs(t, err, bridgetracker.ErrBridgeTxNotABridge)
		require.True(t, domain.IsPermanent(err))
	})

	t.Run("impostor emitter with contradictory fields is not a bridge either", func(t *testing.T) {
		chain := coherentUpstreamChain(t, blockA)
		chain.receipt.Logs[0].Address = common.HexToAddress("0xBAD")
		chain.receipt.Logs[0].BlockHash = blockB.hash
		chain.receipt.Logs[0].TxHash = upstreamOtherTxHash
		source := newUpstreamSource(t, newFakeUpstream(t, chain), true)

		info, err := source.FindBridge(t.Context(), upstreamTxID)
		require.Nil(t, info)
		require.ErrorIs(t, err, bridgetracker.ErrBridgeTxNotABridge)
		require.NotErrorIs(t, err, errInvalidUpstreamData)
	})

	t.Run("not final yet stays pending, whatever the receipt says", func(t *testing.T) {
		chain := coherentUpstreamChain(t, blockA)
		chain.receipt.Logs[0].BlockHash = blockB.hash
		chain.finalized = newUpstreamBlock(upstreamBlockNumber-1, common.HexToHash("0xf0"), upstreamBlockTime-12)
		source := newUpstreamSource(t, newFakeUpstream(t, chain), true)

		info, err := source.FindBridge(t.Context(), upstreamTxID)
		require.Nil(t, info)
		require.ErrorIs(t, err, bridgetracker.ErrBridgeTxNotFound)
		require.False(t, domain.IsPermanent(err))
	})

	t.Run("reverted tx is not a bridge", func(t *testing.T) {
		chain := coherentUpstreamChain(t, blockA)
		chain.receipt.Status = gethtypes.ReceiptStatusFailed
		chain.receipt.Logs[0].BlockHash = blockB.hash
		source := newUpstreamSource(t, newFakeUpstream(t, chain), true)

		info, err := source.FindBridge(t.Context(), upstreamTxID)
		require.Nil(t, info)
		require.ErrorIs(t, err, bridgetracker.ErrBridgeTxNotABridge)
	})
}

// TestResolveBridgeTxWithInconsistentUpstreamJSON covers the path from FindBridge to the
// tracking data: while the upstream contradicts itself the bridge stays unresolved (no Info, no
// path, a transient error counting the retries), it resolves as soon as the upstream answers
// coherently, and it is only given up on once the unresolved timeout elapses
func TestResolveBridgeTxWithInconsistentUpstreamJSON(t *testing.T) {
	const timeout = time.Minute
	start := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	blockA, blockB := upstreamFlavours[0].blocks()
	newInconsistentUpstream := func(t *testing.T) (*fakeUpstream, *BridgeEventSource) {
		t.Helper()
		chain := coherentUpstreamChain(t, blockA)
		// a reorg between the receipt and the header calls: the canonical block of the height is B
		chain.setCanonical(blockB)
		upstream := newFakeUpstream(t, chain)
		return upstream, newUpstreamSource(t, upstream, true)
	}
	resolve := func(
		t *testing.T, source *BridgeEventSource, tracking *domain.TrackingData, now time.Time,
	) (*domain.TrackingData, error) {
		t.Helper()
		// nil resolvers: only the start date of the first step of the path they could set
		return domain.ResolveBridgeTx(t.Context(), source, nil, tracking, timeout, now)
	}

	t.Run("retried while inconsistent, resolved once the upstream is coherent", func(t *testing.T) {
		upstream, source := newInconsistentUpstream(t)
		tracking := domain.NewTrackingData(upstreamTxID, domain.TrackingBridgeTx{}, nil)

		for attempt := 1; attempt <= 3; attempt++ {
			result, err := resolve(t, source, tracking, start.Add(time.Duration(attempt)*time.Second))
			require.Nil(t, result.Info(), "attempt %d: the bridge must not be resolved from inconsistent data", attempt)
			require.ErrorIs(t, err, errInvalidUpstreamData)
			require.Nil(t, result.AllSteps(), "attempt %d: no path is seeded for an unresolved bridge", attempt)
			require.False(t, result.BridgeTx().IsDone())
			require.False(t, result.TerminallyFailed(), "attempt %d: the failure is not final", attempt)
			require.Equal(t, trackertypes.TrackingStatusRegistered, result.TrackingStatus())
			errStep := result.BridgeTx().Error
			require.NotNil(t, errStep)
			require.Equal(t, trackertypes.StepErrorTransient, errStep.ErrorType)
			require.Equal(t, attempt, errStep.RetryCount)
			tracking = result
		}

		// the reorg settles: the canonical block of the height is the receipt's again
		upstream.mutate(func(chain *upstreamChain) { chain.setCanonical(blockA) })
		result, err := resolve(t, source, tracking, start.Add(4*time.Second))
		require.NoError(t, err)
		require.Equal(t, expectedUpstreamBridge(blockA), result.Info())
		require.Nil(t, result.BridgeTx().Error, "a successful resolution clears the accumulated error")
		require.True(t, result.BridgeTx().IsDone())
		require.NotEmpty(t, result.AllSteps(), "the expected path is seeded once the bridge resolves")
	})

	t.Run("given up on once the unresolved timeout elapses", func(t *testing.T) {
		_, source := newInconsistentUpstream(t)
		tracking := domain.NewTrackingData(upstreamTxID, domain.TrackingBridgeTx{}, nil)

		result, err := resolve(t, source, tracking, start)
		require.ErrorIs(t, err, errInvalidUpstreamData)
		require.Equal(t, trackertypes.StepErrorTransient, result.BridgeTx().Error.ErrorType)

		result, err = resolve(t, source, result, start.Add(timeout))
		require.ErrorIs(t, err, errInvalidUpstreamData)
		require.Nil(t, result.Info())
		require.Equal(t, trackertypes.StepErrorExhausted, result.BridgeTx().Error.ErrorType)
		require.True(t, result.TerminallyFailed())
		require.Equal(t, trackertypes.TrackingStatusError, result.TrackingStatus())
	})
}
