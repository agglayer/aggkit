package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeSettlement is one canned row a fakeSettlementsServer answers GET /bridge/v1/settlements
// with, paired with the deposit-count index its LER resolves to on /bridge/v1/root-by-ler (what
// Covers reads)
type fakeSettlement struct {
	ler       common.Hash
	rootIndex uint32
	txHash    *common.Hash
}

// fakeSettlementsServer serves both GET /bridge/v1/root-by-ler (used by Covers) and GET
// /bridge/v1/settlements (paginated, most-recent first: settlements[0] is the newest) on network
// l2ToL1Bridge().NetworkID, from a fixed in-order list. notFound makes /settlements always
// answer HTTP 404, simulating a bridge-service instance that predates the endpoint
type fakeSettlementsServer struct {
	settlements []fakeSettlement
	notFound    bool
	// settlementReqs, when non-nil, is incremented once per GET /bridge/v1/settlements request
	// received -- tests use it to assert the binary search resolves in O(log N) requests, not a
	// full linear walk of the history
	settlementReqs *int
}

func (f fakeSettlementsServer) start(t *testing.T) NetworkURLResolver {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/bridge/v1/root-by-ler", func(w http.ResponseWriter, r *http.Request) {
		lerHex := r.URL.Query().Get("ler")
		for _, s := range f.settlements {
			if s.ler.Hex() == lerHex {
				fmt.Fprintf(w, `{"index":%d,"block_num":0,"block_position":0}`, s.rootIndex)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"not found (not synced yet)"}`)
	})
	mux.HandleFunc("/bridge/v1/settlements", func(w http.ResponseWriter, r *http.Request) {
		if f.settlementReqs != nil {
			*f.settlementReqs++
		}
		if f.notFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		pageNumber, _ := strconv.Atoi(r.URL.Query().Get("page_number"))
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if pageNumber < 1 {
			pageNumber = 1
		}
		start := (pageNumber - 1) * pageSize
		result := bridgeservicetypes.SettlementsResult{Count: len(f.settlements)}
		for i := start; i < len(f.settlements) && i < start+pageSize; i++ {
			s := f.settlements[i]
			resp := &bridgeservicetypes.SettlementResponse{
				NewLocalExitRoot: bridgeservicetypes.Hash(s.ler.Hex()),
				BlockNumber:      uint64(1000 + i),
			}
			if s.txHash != nil {
				h := bridgeservicetypes.Hash(s.txHash.Hex())
				resp.TxHash = &h
			}
			result.Settlements = append(result.Settlements, resp)
		}
		body, err := json.Marshal(result)
		require.NoError(t, err)
		_, err = w.Write(body)
		require.NoError(t, err)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return staticURLs{l2ToL1Bridge().NetworkID: bridgeservicefinder.NetworkURLs{BridgeURL: server.URL}}
}

// TestEarliestSettlementTxCoveringViaBridgeService proves the primary path: it walks
// bridge-service's most-recent-first settlement list and returns the tx hash of the last (most
// recent) entry that still covers the bridge -- the earliest one that does, right after the
// transition from non-covering to covering
func TestEarliestSettlementTxCoveringViaBridgeService(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	txC, txB, txA := common.HexToHash("0xc"), common.HexToHash("0xb"), common.HexToHash("0xa")
	server := fakeSettlementsServer{settlements: []fakeSettlement{
		{ler: common.HexToHash("0x111"), rootIndex: 9, txHash: &txC}, // covers (9 >= 7)
		{ler: common.HexToHash("0x222"), rootIndex: 7, txHash: &txB}, // covers (7 >= 7): the exact one
		{ler: common.HexToHash("0x333"), rootIndex: 3, txHash: &txA}, // does not cover (3 < 7)
	}}
	source := NewCertificateSource(nil, server.start(t), nil, testRollupManagerAddress, testLogger)

	got, _, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, txB, *got)
}

// TestEarliestSettlementTxCoveringViaBridgeServiceBinarySearchesLargeHistory proves the search
// resolves in O(log N) bridge-service requests, not one per settlement: a linear walk over a
// mature network's full history (thousands of entries, one HTTP round-trip each) can outlast the
// engine's own per-tick resolve timeout and never make progress (see issue #1817's own
// resumable-search fix, which the primary bridge-service path does not need because binary
// search sidesteps the problem instead)
func TestEarliestSettlementTxCoveringViaBridgeServiceBinarySearchesLargeHistory(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	const historySize = 16
	txCovering := common.HexToHash("0xcovering")
	settlements := make([]fakeSettlement, historySize)
	for i := range settlements {
		// most-recent-first, root index descending: covers (>= 7) for i in [0,13], not for [14,15]
		settlements[i] = fakeSettlement{ler: common.HexToHash(fmt.Sprintf("0x%x", 100+i)), rootIndex: uint32(20 - i)}
	}
	settlements[13].txHash = &txCovering // the earliest (oldest) covering entry -- the answer

	requests := 0
	server := fakeSettlementsServer{settlements: settlements, settlementReqs: &requests}
	source := NewCertificateSource(nil, server.start(t), nil, testRollupManagerAddress, testLogger)

	got, _, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, txCovering, *got)
	require.Less(t, requests, historySize, "binary search must not walk every settlement")
}

// TestEarliestSettlementTxCoveringViaBridgeServiceOldestCoversEverything proves that when every
// settlement fetched covers the bridge (the network's very first certificate already did), the
// oldest one seen is returned as the earliest possible answer, instead of erroring or hanging
func TestEarliestSettlementTxCoveringViaBridgeServiceOldestCoversEverything(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	txB, txA := common.HexToHash("0xb"), common.HexToHash("0xa")
	server := fakeSettlementsServer{settlements: []fakeSettlement{
		{ler: common.HexToHash("0x222"), rootIndex: 9, txHash: &txB},
		{ler: common.HexToHash("0x333"), rootIndex: 7, txHash: &txA}, // oldest, still covers (7 >= 7)
	}}
	source := NewCertificateSource(nil, server.start(t), nil, testRollupManagerAddress, testLogger)

	got, _, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, txA, *got)
}

// TestEarliestSettlementTxCoveringLegacyRowResolvesAtExactBlock proves that when the exact
// covering entry bridge-service reports has no recorded tx hash (a settlement synced before
// #1817's tx_hash column existed), the search resolves it with a single-block FilterLogs at the
// entry's own (recorded) BlockNumber, instead of falling all the way back to a genesis-ward scan
func TestEarliestSettlementTxCoveringLegacyRowResolvesAtExactBlock(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	server := fakeSettlementsServer{settlements: []fakeSettlement{
		{ler: common.HexToHash("0x222"), rootIndex: 7, txHash: nil}, // covers, but legacy (no tx hash)
	}}
	ethClient := mocks.NewBaseEthereumClienter(t) // no genesis-ward scan expectation: must not be needed
	logTxHash := common.HexToHash("0xexact")
	expectVerifyBatchesLogAtBlock(ethClient, bridge, 1000, []gethtypes.Log{{
		Topics: []common.Hash{
			verifyBatchesTrustedAggregatorSignature, rollupIDTopicFor(bridge.NetworkID),
		},
		Data:        verifyBatchesLogData(common.HexToHash("0x222")),
		TxHash:      logTxHash,
		BlockNumber: 1000,
	}})

	source := NewCertificateSource(
		nil, server.start(t), StaticClients{0: ethClient}, testRollupManagerAddress, testLogger)

	got, _, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 999999, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, logTxHash, *got)
}

// TestEarliestSettlementTxCoveringLegacyRowFallsBackToLogs proves that when the legacy entry's
// own recorded BlockNumber does not carry a matching log either (e.g. an indexer gap), the
// search still falls all the way back to the genesis-ward scan anchored at fromBlock, instead of
// giving up
func TestEarliestSettlementTxCoveringLegacyRowFallsBackToLogs(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	txA := common.HexToHash("0xa")
	server := fakeSettlementsServer{settlements: []fakeSettlement{
		{ler: common.HexToHash("0x222"), rootIndex: 7, txHash: nil},  // covers, but legacy (no tx hash)
		{ler: common.HexToHash("0x333"), rootIndex: 3, txHash: &txA}, // does not cover
	}}
	ethClient := mocks.NewBaseEthereumClienter(t)
	expectVerifyBatchesLogAtBlock(ethClient, bridge, 1000, nil) // the legacy row's own block: nothing there
	logTxHash := common.HexToHash("0xfallback")
	expectVerifyBatchesLog(ethClient, bridge, 100, gethtypes.Log{
		Topics: []common.Hash{
			verifyBatchesTrustedAggregatorSignature, rollupIDTopicFor(bridge.NetworkID),
		},
		Data:        verifyBatchesLogData(common.HexToHash("0x222")), // same LER as the legacy row: covers
		TxHash:      logTxHash,
		BlockNumber: 100,
	})

	source := NewCertificateSource(
		nil, server.start(t), StaticClients{0: ethClient}, testRollupManagerAddress, testLogger)

	got, _, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 100, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, logTxHash, *got)
}

// TestEarliestSettlementTxCoveringEndpointNotFoundFallsBackToLogs proves that a bridge-service
// instance that predates GET /bridge/v1/settlements (HTTP 404) is treated the same way: fall
// back to L1 logs instead of erroring
func TestEarliestSettlementTxCoveringEndpointNotFoundFallsBackToLogs(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	// notFound only affects GET /settlements; /root-by-ler (what the fallback's Covers check
	// asks) is still served normally, off the same settlements list
	server := fakeSettlementsServer{
		notFound:    true,
		settlements: []fakeSettlement{{ler: common.HexToHash("0x222"), rootIndex: 7}},
	}
	ethClient := mocks.NewBaseEthereumClienter(t)
	logTxHash := common.HexToHash("0xfallback")
	expectVerifyBatchesLog(ethClient, bridge, 100, gethtypes.Log{
		Topics: []common.Hash{
			verifyBatchesTrustedAggregatorSignature, rollupIDTopicFor(bridge.NetworkID),
		},
		Data:        verifyBatchesLogData(common.HexToHash("0x222")),
		TxHash:      logTxHash,
		BlockNumber: 100,
	})

	source := NewCertificateSource(
		nil, server.start(t), StaticClients{0: ethClient}, testRollupManagerAddress, testLogger)

	got, _, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 100, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, logTxHash, *got)
}

// TestEarliestSettlementTxCoveringResumesFromProgress proves that a resumable
// *types.SettlementSearchProgress from an earlier tick (see issue #1817's resumable-search fix)
// anchors the backwards log scan at progress.NextToBlock, not fromBlock, and carries over its
// LastCoveringTxHash as the fallback answer -- letting the search keep moving forward across
// retries instead of restarting from the tracked certificate's own settlement block every time,
// and skipping the bridge-service walk a resumed search already decided needed the log fallback
func TestEarliestSettlementTxCoveringResumesFromProgress(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	server := fakeSettlementsServer{settlements: []fakeSettlement{
		{ler: common.HexToHash("0x333"), rootIndex: 3}, // does not cover (3 < 7)
	}}
	ethClient := mocks.NewBaseEthereumClienter(t)
	// stubbed at NextToBlock (100), not at fromBlock (999999): proves the resume cursor, not
	// fromBlock, anchors the scan
	expectVerifyBatchesLog(ethClient, bridge, 100, gethtypes.Log{
		Topics: []common.Hash{
			verifyBatchesTrustedAggregatorSignature, rollupIDTopicFor(bridge.NetworkID),
		},
		Data:        verifyBatchesLogData(common.HexToHash("0x333")),
		TxHash:      common.HexToHash("0xfresh"),
		BlockNumber: 100,
	})

	source := NewCertificateSource(
		nil, server.start(t), StaticClients{0: ethClient}, testRollupManagerAddress, testLogger)

	priorTxHash := common.HexToHash("0xstale")
	resume := &trackertypes.SettlementSearchProgress{NextToBlock: 100, LastCoveringTxHash: &priorTxHash}
	got, progress, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 999999, resume)
	require.NoError(t, err)
	require.Nil(t, progress)
	require.NotNil(t, got)
	require.Equal(t, priorTxHash, *got) // resume's own carried-over answer, not the fresh log's tx hash
}

// TestEarliestSettlementTxCoveringViaLogsStopsBeforeDeadline proves the backwards scan checks
// ctx's remaining budget before each chunk and returns a resumable *types.SettlementSearchProgress
// once too little is left, instead of letting the engine's own per-tick resolve timeout cut off
// FilterLogs mid-flight and lose all progress made so far (issue #1817)
func TestEarliestSettlementTxCoveringViaLogsStopsBeforeDeadline(t *testing.T) {
	bridge := l2ToL1Bridge()
	ethClient := mocks.NewBaseEthereumClienter(t) // no FilterLogs expectation: must never be called

	source := NewCertificateSource(nil, nil, StaticClients{0: ethClient}, testRollupManagerAddress, testLogger)

	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // let the deadline pass, well under the safety margin

	got, progress, err := source.earliestSettlementTxCoveringViaLogs(ctx, bridge, 12345, nil)
	require.NoError(t, err)
	require.Nil(t, got)
	require.NotNil(t, progress)
	require.Equal(t, uint64(12345), progress.NextToBlock)
	require.Nil(t, progress.LastCoveringTxHash)
}

// rollupIDTopicFor mirrors earliestSettlementTxCoveringViaLogs' own topic encoding, for tests
// that need to stub FilterLogs with a matching FilterQuery
func rollupIDTopicFor(networkID uint32) common.Hash {
	return common.BigToHash(new(big.Int).SetUint64(uint64(networkID)))
}

// verifyBatchesLogData builds a VerifyBatchesTrustedAggregator log's non-indexed Data (numBatch,
// stateRoot, exitRoot, each word-padded to 32 bytes) carrying exitRoot as its last word
func verifyBatchesLogData(exitRoot common.Hash) []byte {
	data := make([]byte, verifyBatchesTrustedAggregatorDataLen)
	copy(data[64:], exitRoot[:])
	return data
}

// expectVerifyBatchesLog stubs ethClient's FilterLogs for earliestSettlementTxCoveringViaLogs'
// very first backwards chunk (fromBlock down to fromBlock minus l1InfoTreeBackwardsSearchChunkSize
// plus one, or 0), filtered to bridge's rollupID, with a single matching log
func expectVerifyBatchesLog(
	ethClient *mocks.BaseEthereumClienter, bridge *bridgetracker.BridgeInfo, fromBlock uint64, log gethtypes.Log,
) {
	fromChunk := uint64(0)
	if fromBlock >= l1InfoTreeBackwardsSearchChunkSize {
		fromChunk = fromBlock - l1InfoTreeBackwardsSearchChunkSize + 1
	}
	ethClient.EXPECT().FilterLogs(mock.Anything, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromChunk),
		ToBlock:   new(big.Int).SetUint64(fromBlock),
		Addresses: []common.Address{testRollupManagerAddress},
		Topics:    [][]common.Hash{{verifyBatchesTrustedAggregatorSignature}, {rollupIDTopicFor(bridge.NetworkID)}},
	}).Return([]gethtypes.Log{log}, nil)
}

// expectVerifyBatchesLogAtBlock stubs ethClient's FilterLogs for verifyBatchesTxHashAtBlock's
// exact single-block query (FromBlock == ToBlock == blockNumber), filtered to bridge's rollupID,
// returning logs (nil/empty means the block does not carry a matching log)
func expectVerifyBatchesLogAtBlock(
	ethClient *mocks.BaseEthereumClienter, bridge *bridgetracker.BridgeInfo, blockNumber uint64, logs []gethtypes.Log,
) {
	ethClient.EXPECT().FilterLogs(mock.Anything, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(blockNumber),
		ToBlock:   new(big.Int).SetUint64(blockNumber),
		Addresses: []common.Address{testRollupManagerAddress},
		Topics:    [][]common.Hash{{verifyBatchesTrustedAggregatorSignature}, {rollupIDTopicFor(bridge.NetworkID)}},
	}).Return(logs, nil)
}
