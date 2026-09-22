package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker"
	trackertypes "github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var testTxHash = common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234")

// testBlockHash is the block hash bridgeEventLog reports its log as mined in, used to stub
// HeaderByHash in tests that resolve a BridgeInfo's BlockTimestamp
var testBlockHash = common.HexToHash("0xaaaa567890123456789012345678901234567890123456789012345678901234")

// testBlockTimestamp is the timestamp expectBlockTimestamp stubs testBlockHash's header to
const testBlockTimestamp = uint64(1700000000)

// expectBlockTimestamp stubs client's HeaderByHash for testBlockHash to report testBlockTimestamp
func expectBlockTimestamp(client *mocks.BaseEthereumClienter) {
	client.EXPECT().HeaderByHash(mock.Anything, testBlockHash).
		Return(&gethtypes.Header{Time: testBlockTimestamp}, nil)
}

// l1ToL2Bridge is the BridgeInfo of an L1->L2 bridge used across the source tests
func l1ToL2Bridge() *bridgetracker.BridgeInfo {
	return &bridgetracker.BridgeInfo{
		NetworkID:          0,
		LeafType:           trackertypes.BridgeLeafTypeAsset,
		DestinationNetwork: 1,
		DepositCount:       7,
		BlockNumber:        12345,
		BlockHash:          testBlockHash,
		LogIndex:           3,
		OriginNetwork:      0,
		OriginAddress:      common.HexToAddress("0x20"),
		DestinationAddress: common.HexToAddress("0x30"),
		Amount:             big.NewInt(100),
		BlockTimestamp:     testBlockTimestamp,
	}
}

// l2ToL1Bridge is the BridgeInfo of an L2->L1 bridge used across the LERSource tests
func l2ToL1Bridge() *bridgetracker.BridgeInfo {
	return &bridgetracker.BridgeInfo{
		NetworkID:          5,
		LeafType:           trackertypes.BridgeLeafTypeAsset,
		DestinationNetwork: 0,
		DepositCount:       7,
		BlockNumber:        12345,
		LogIndex:           3,
	}
}

// bridgeEventLog packs a BridgeEvent log like the bridge contract emits it
func bridgeEventLog(t *testing.T, destinationNetwork, depositCount uint32) *gethtypes.Log {
	t.Helper()

	bridgeABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)
	event, err := bridgeABI.EventByID(bridgeEventSignature)
	require.NoError(t, err)

	data, err := event.Inputs.Pack(
		uint8(trackertypes.BridgeLeafTypeAsset), uint32(0), common.HexToAddress("0x20"),
		destinationNetwork, common.HexToAddress("0x30"),
		big.NewInt(100), []byte{}, depositCount)
	require.NoError(t, err)

	return &gethtypes.Log{
		Topics:      []common.Hash{bridgeEventSignature},
		Data:        data,
		BlockNumber: 12345,
		BlockHash:   testBlockHash,
		Index:       3,
	}
}

// staticBridgeAddressResolver is a hand-written bridgeAddressResolver fake for tests (no
// mockery mock exists for this narrow, package-private interface — see other fakes in this
// package, e.g. activityRPCTestLister). A network absent from addrs returns err (defaulting to
// a "not configured" error), mirroring how a real resolver failure surfaces.
type staticBridgeAddressResolver struct {
	addrs map[uint32]common.Address
	err   error
}

func (r staticBridgeAddressResolver) BridgeAddress(_ context.Context, networkID uint32) (common.Address, error) {
	if addr, ok := r.addrs[networkID]; ok {
		return addr, nil
	}
	if r.err != nil {
		return common.Address{}, r.err
	}
	return common.Address{}, fmt.Errorf("no bridge contract address configured for network %d", networkID)
}

// panicBridgeAddressResolver is a bridgeAddressResolver that fails the test if it is ever
// consulted — used to prove a static Tracker.BridgeAddrs override is used as-is, without ever
// falling through to the resolver.
type panicBridgeAddressResolver struct{}

func (panicBridgeAddressResolver) BridgeAddress(context.Context, uint32) (common.Address, error) {
	panic("resolver must not be consulted when a static bridgeAddrs override is configured")
}

// testBridgeAddr is the network-0 canonical bridge address newBridgeEventSource's resolver
// reports; matches the zero-value Address of the logs bridgeEventLog produces, so every test
// relying on newBridgeEventSource's default log fixtures still gets accepted.
var testBridgeAddr = common.Address{}

func newBridgeEventSource(t *testing.T, client *mocks.BaseEthereumClienter) *BridgeEventSource {
	t.Helper()

	source, err := NewBridgeEventSource(
		StaticClients{0: client}, aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock, nil,
		staticBridgeAddressResolver{addrs: map[uint32]common.Address{0: testBridgeAddr}})
	require.NoError(t, err)
	return source
}

// expectFinalized stubs client's CustomHeaderByNumber to report blockNumber itself as
// finalized, so a receipt mined in that block is accepted
func expectFinalized(client *mocks.BaseEthereumClienter, blockNumber uint64) {
	client.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.FinalizedBlock).
		Return(&aggkittypes.BlockHeader{Number: blockNumber}, nil)
}

func TestBridgeEventSourceFindBridge(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		Status:      gethtypes.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(12345),
		Logs:        []*gethtypes.Log{bridgeEventLog(t, 1, 7)},
	}, nil)
	expectFinalized(client, 12345)
	expectBlockTimestamp(client)

	source := newBridgeEventSource(t, client)
	info, err := source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
	require.NoError(t, err)
	require.Equal(t, l1ToL2Bridge(), info)
}

func TestBridgeEventSourceNotFoundCases(t *testing.T) {
	testCases := []struct {
		name        string
		receipt     *gethtypes.Receipt
		err         error
		expectedErr error
	}{
		{name: "tx does not exist", err: ethereum.NotFound, expectedErr: bridgetracker.ErrBridgeTxNotFound},
		{
			name:        "tx reverted",
			receipt:     &gethtypes.Receipt{Status: gethtypes.ReceiptStatusFailed, BlockNumber: big.NewInt(12345)},
			expectedErr: bridgetracker.ErrBridgeTxNotABridge,
		},
		{
			name: "no BridgeEvent log",
			receipt: &gethtypes.Receipt{
				Status:      gethtypes.ReceiptStatusSuccessful,
				BlockNumber: big.NewInt(12345),
				Logs:        []*gethtypes.Log{{Topics: []common.Hash{common.HexToHash("0x01")}}},
			},
			expectedErr: bridgetracker.ErrBridgeTxNotABridge,
		},
		{
			name:        "receipt not finalized yet",
			receipt:     &gethtypes.Receipt{Status: gethtypes.ReceiptStatusSuccessful, BlockNumber: big.NewInt(12346)},
			expectedErr: bridgetracker.ErrBridgeTxNotFound,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := mocks.NewBaseEthereumClienter(t)
			client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(tc.receipt, tc.err)
			if tc.receipt != nil {
				expectFinalized(client, 12345)
			}

			source := newBridgeEventSource(t, client)
			_, err := source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
			require.ErrorIs(t, err, tc.expectedErr)
		})
	}
}

func TestBridgeEventSourceUnknownNetwork(t *testing.T) {
	source := newBridgeEventSource(t, mocks.NewBaseEthereumClienter(t))

	_, err := source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 5, TxHash: testTxHash})
	require.ErrorContains(t, err, "network 5")
	require.NotErrorIs(t, err, bridgetracker.ErrBridgeTxNotFound,
		"a resolver failure is transient, not a terminal not-found")
}

// TestBridgeEventSourceRejectsUnverifiedEmitter checks that, once a network has a configured
// static bridgeAddrs override, a BridgeEvent log emitted by any other contract is ignored
// rather than treated as a real bridge — this is what stops an unrelated or malicious contract
// from spoofing the event. It uses panicBridgeAddressResolver to additionally prove the
// override wins over the resolver: if FindBridge ever fell through to the resolver instead of
// using the override as-is, this test would panic.
func TestBridgeEventSourceRejectsUnverifiedEmitter(t *testing.T) {
	realBridgeAddr := common.HexToAddress("0xB41D9E")
	spoofedLog := bridgeEventLog(t, 1, 7)
	spoofedLog.Address = common.HexToAddress("0xBAD")

	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		Status:      gethtypes.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(12345),
		Logs:        []*gethtypes.Log{spoofedLog},
	}, nil)
	expectFinalized(client, 12345)

	source, err := NewBridgeEventSource(
		StaticClients{0: client}, aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock,
		map[uint32]common.Address{0: realBridgeAddr}, panicBridgeAddressResolver{})
	require.NoError(t, err)

	_, err = source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
	require.ErrorIs(t, err, bridgetracker.ErrBridgeTxNotABridge)
}

// TestBridgeEventSourceOverrideAccepsCanonicalEmitter checks the accept-path complement of
// TestBridgeEventSourceRejectsUnverifiedEmitter: a log emitted by the overridden address is
// accepted, and the resolver (which would panic if consulted) is still never used.
func TestBridgeEventSourceOverrideAcceptsCanonicalEmitter(t *testing.T) {
	realBridgeAddr := common.HexToAddress("0xB41D9E")
	realLog := bridgeEventLog(t, 1, 7)
	realLog.Address = realBridgeAddr

	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		Status:      gethtypes.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(12345),
		Logs:        []*gethtypes.Log{realLog},
	}, nil)
	expectFinalized(client, 12345)
	expectBlockTimestamp(client)

	source, err := NewBridgeEventSource(
		StaticClients{0: client}, aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock,
		map[uint32]common.Address{0: realBridgeAddr}, panicBridgeAddressResolver{})
	require.NoError(t, err)

	info, err := source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
	require.NoError(t, err)
	require.Equal(t, uint32(1), info.DestinationNetwork)
	require.Equal(t, uint32(7), info.DepositCount)
}

// TestBridgeEventSourceResolverRejectsUnverifiedEmitter is like
// TestBridgeEventSourceRejectsUnverifiedEmitter, but for a network with no static bridgeAddrs
// override: the canonical address is resolved through the resolver instead, and a log from any
// other address is still the only candidate, so FindBridge reports ErrBridgeTxNotABridge
// (permanent — the receipt's log set can never change on retry).
func TestBridgeEventSourceResolverRejectsUnverifiedEmitter(t *testing.T) {
	realBridgeAddr := common.HexToAddress("0xB41D9E")
	spoofedLog := bridgeEventLog(t, 1, 7)
	spoofedLog.Address = common.HexToAddress("0xBAD")

	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		Status:      gethtypes.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(12345),
		Logs:        []*gethtypes.Log{spoofedLog},
	}, nil)
	expectFinalized(client, 12345)

	source, err := NewBridgeEventSource(
		StaticClients{0: client}, aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock, nil,
		staticBridgeAddressResolver{addrs: map[uint32]common.Address{0: realBridgeAddr}})
	require.NoError(t, err)

	_, err = source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
	require.ErrorIs(t, err, bridgetracker.ErrBridgeTxNotABridge)
}

// TestBridgeEventSourceResolverErrorIsTransient checks that a resolver failure (e.g. a
// transient RPC error hitting the rollup manager) is reported as a plain (non-permanent, non-
// not-found) error — the engine retries — and, crucially, that FindBridge never falls back to
// matching on the event signature alone: the log is never even inspected once the resolver
// fails, since no mock expectation is registered for HeaderByHash/ParseBridgeEvent-adjacent
// calls beyond TransactionReceipt/CustomHeaderByNumber.
func TestBridgeEventSourceResolverErrorIsTransient(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		Status:      gethtypes.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(12345),
		Logs:        []*gethtypes.Log{bridgeEventLog(t, 1, 7)},
	}, nil)
	expectFinalized(client, 12345)

	resolverErr := errors.New("rollup manager query failed")
	source, err := NewBridgeEventSource(
		StaticClients{0: client}, aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock, nil,
		staticBridgeAddressResolver{err: resolverErr})
	require.NoError(t, err)

	_, err = source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
	require.ErrorIs(t, err, resolverErr)
	require.NotErrorIs(t, err, bridgetracker.ErrBridgeTxNotABridge,
		"a resolver failure must never be treated as a permanent rejection")
	require.NotErrorIs(t, err, bridgetracker.ErrBridgeTxNotFound)
}

// TestBridgeEventSourceMatchesCanonicalAmongMultipleLogs checks that, when a receipt carries
// both an impostor BridgeEvent-shaped log and the real one, FindBridge resolves the real one
// regardless of which order they appear in the receipt's log list.
func TestBridgeEventSourceMatchesCanonicalAmongMultipleLogs(t *testing.T) {
	realBridgeAddr := common.HexToAddress("0xB41D9E")

	testCases := []struct {
		name    string
		reorder bool
	}{
		{name: "impostor log first"},
		{name: "real log first", reorder: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			impostorLog := bridgeEventLog(t, 1, 7)
			impostorLog.Address = common.HexToAddress("0xBAD")
			realLog := bridgeEventLog(t, 2, 9)
			realLog.Address = realBridgeAddr

			logs := []*gethtypes.Log{impostorLog, realLog}
			if tc.reorder {
				logs = []*gethtypes.Log{realLog, impostorLog}
			}

			client := mocks.NewBaseEthereumClienter(t)
			client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
				Status:      gethtypes.ReceiptStatusSuccessful,
				BlockNumber: big.NewInt(12345),
				Logs:        logs,
			}, nil)
			expectFinalized(client, 12345)
			expectBlockTimestamp(client)

			source, err := NewBridgeEventSource(
				StaticClients{0: client}, aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock, nil,
				staticBridgeAddressResolver{addrs: map[uint32]common.Address{0: realBridgeAddr}})
			require.NoError(t, err)

			info, err := source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
			require.NoError(t, err)
			require.Equal(t, uint32(2), info.DestinationNetwork)
			require.Equal(t, uint32(9), info.DepositCount)
		})
	}
}

// TestNewBridgeEventSourceRequiresResolver checks the fail-closed construction-time guard: a
// nil resolver is rejected outright, since it would leave FindBridge with no way to verify an
// unconfigured network's emitter.
func TestNewBridgeEventSourceRequiresResolver(t *testing.T) {
	_, err := NewBridgeEventSource(
		StaticClients{}, aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock, nil, nil)
	require.Error(t, err)
}

// fakeBridgeService emulates the aggkit bridge service endpoints the sources consume
type fakeBridgeService struct {
	// l1InfoTreeIndex is served by /bridge/v1/l1-info-tree-index; nil -> not covered (500)
	l1InfoTreeIndex *uint32
	// injectedLeaf is served by /bridge/v1/injected-l1-info-leaf; nil -> not injected (500)
	injectedLeaf map[string]any
	// claimsCount is served by /bridge/v1/claims
	claimsCount int
	// claimTxHash, claimBlockNum and claimBlockTimestamp populate the single claim served when
	// claimsCount > 0
	claimTxHash         string
	claimBlockNum       uint64
	claimBlockTimestamp uint64

	// lastLeafIndexQuery records the leaf_index of the last injected-l1-info-leaf request
	lastLeafIndexQuery string
	// lastNetworkIDQuery records the network_id of the last injected-l1-info-leaf request
	lastNetworkIDQuery string
	// lastGlobalIndexQuery records the global_index of the last claims request
	lastGlobalIndexQuery string
}

func (f *fakeBridgeService) start(t *testing.T) staticURLs {
	t.Helper()
	return f.startAt(t, 1)
}

// startAt is like start, but serves as networkID instead of the hardcoded 1 — for tests that
// need two fakeBridgeService instances resolvable under distinct networks (e.g. an origin and a
// destination queried separately, merged with staticURLs' own union of two maps)
func (f *fakeBridgeService) startAt(t *testing.T, networkID uint32) staticURLs {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/bridge/v1/l1-info-tree-index", func(w http.ResponseWriter, _ *http.Request) {
		if f.l1InfoTreeIndex == nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"failed to get l1 info tree index: not found"}`)
			return
		}
		fmt.Fprintf(w, "%d", *f.l1InfoTreeIndex)
	})
	mux.HandleFunc("/bridge/v1/injected-l1-info-leaf", func(w http.ResponseWriter, r *http.Request) {
		f.lastLeafIndexQuery = r.URL.Query().Get("leaf_index")
		f.lastNetworkIDQuery = r.URL.Query().Get("network_id")
		if f.injectedLeaf == nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"error getting injected info after index: not found"}`)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(f.injectedLeaf))
	})
	mux.HandleFunc("/bridge/v1/claims", func(w http.ResponseWriter, r *http.Request) {
		f.lastGlobalIndexQuery = r.URL.Query().Get("global_index")
		if f.claimsCount == 0 {
			fmt.Fprint(w, `{"claims":[],"count":0}`)
			return
		}
		fmt.Fprintf(w, `{"claims":[{"tx_hash":"%s","block_num":%d,"block_timestamp":%d}],"count":%d}`,
			f.claimTxHash, f.claimBlockNum, f.claimBlockTimestamp, f.claimsCount)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return staticURLs{networkID: bridgeservicefinder.NetworkURLs{BridgeURL: server.URL}}
}

// staticURLs is a fixed NetworkURLResolver for tests
type staticURLs map[uint32]bridgeservicefinder.NetworkURLs

func (s staticURLs) GetURL(networkID uint32) (bridgeservicefinder.NetworkURLs, error) {
	urls, ok := s[networkID]
	if !ok {
		return bridgeservicefinder.NetworkURLs{}, bridgeservicefinder.ErrURLNotFound
	}
	return urls, nil
}

// merge returns the union of s and other, for tests combining two fakeBridgeService.startAt
// results (e.g. an origin and a destination resolvable under distinct networks)
func (s staticURLs) merge(other staticURLs) staticURLs {
	merged := make(staticURLs, len(s)+len(other))
	maps.Copy(merged, s)
	maps.Copy(merged, other)
	return merged
}

// TestFinderClients pins the finder-backed EthClientResolver: overrides win without asking
// the finder, resolved URLs are dialed once and cached, and unresolvable networks are a
// transient failure (not ErrSourceUnavailable, which the engine treats as permanent)
func TestFinderClients(t *testing.T) {
	t.Parallel()

	override := mocks.NewBaseEthereumClienter(t)
	dialed := mocks.NewBaseEthereumClienter(t)

	urls := staticURLs{
		1: bridgeservicefinder.NetworkURLs{JSONRPCURL: "http://rpc-1"},
		3: bridgeservicefinder.NetworkURLs{BridgeURL: "http://bridge-3"}, // no JSON-RPC URL
	}
	fc := NewFinderClients(log.WithFields("module", "sources_test"), urls, StaticClients{0: override})

	dialCalls := 0
	fc.dial = func(_ context.Context, url string) (aggkittypes.BaseEthereumClienter, error) {
		dialCalls++
		require.Equal(t, "http://rpc-1", url)
		return dialed, nil
	}

	// overrides win and never ask the finder (network 0 is not in urls)
	c, err := fc.RPCClientFor(context.Background(), 0)
	require.NoError(t, err)
	require.Same(t, override, c)
	require.Zero(t, dialCalls)

	// a finder-resolved network dials once; later calls reuse the cached client
	c, err = fc.RPCClientFor(context.Background(), 1)
	require.NoError(t, err)
	require.Same(t, dialed, c)
	c, err = fc.RPCClientFor(context.Background(), 1)
	require.NoError(t, err)
	require.Same(t, dialed, c)
	require.Equal(t, 1, dialCalls)

	// a network the finder does not know is transient: the finder may discover it later
	_, err = fc.RPCClientFor(context.Background(), 2)
	require.ErrorIs(t, err, bridgeservicefinder.ErrURLNotFound)
	require.NotErrorIs(t, err, bridgetracker.ErrSourceUnavailable)

	// a network resolved without JSON-RPC endpoint is also an error
	_, err = fc.RPCClientFor(context.Background(), 3)
	require.ErrorContains(t, err, "no JSON-RPC URL resolved for network 3")
}

func TestGERSourceOriginGER(t *testing.T) {
	fake := &fakeBridgeService{}
	source := NewGERSource(fake.start(t), nil, common.Address{}, aggkittypes.FinalizedBlock, nil, 0, nil)

	// not covered yet -> nil, nil
	ger, err := source.OriginGER(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.Nil(t, ger)

	// covered -> the leaf is fetched with a direct index lookup (network_id=0) to get the
	// resulting GER and the block it was updated in
	idx := uint32(42)
	fake.l1InfoTreeIndex = &idx
	fake.injectedLeaf = map[string]any{
		"l1_info_tree_index": 42,
		"global_exit_root":   "0x0a",
		"block_num":          100,
	}
	ger, err = source.OriginGER(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.NotNil(t, ger)
	require.Equal(t, uint32(0), ger.NetworkID)
	require.Equal(t, trackertypes.LERTypeMainnet, ger.LERType)
	require.Equal(t, common.HexToHash("0x0a"), *ger.GER)
	require.Equal(t, uint64(100), *ger.BlockNumber)
	require.Equal(t, "0", fake.lastNetworkIDQuery, "must fetch the leaf with a direct index lookup")
}

func TestGERSourceInjectedGER(t *testing.T) {
	fake := &fakeBridgeService{}
	source := NewGERSource(fake.start(t), nil, common.Address{}, aggkittypes.FinalizedBlock, nil, 0,
		log.WithFields("module", "sources_test"))

	// not even covered on origin -> nil
	injected, err := source.InjectedGER(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.Nil(t, injected)

	// covered but not injected on destination -> nil
	idx := uint32(42)
	fake.l1InfoTreeIndex = &idx
	injected, err = source.InjectedGER(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.Nil(t, injected)
	require.Equal(t, "42", fake.lastLeafIndexQuery, "must ask for the covering leaf index")

	// injected -> GERData with the leaf roots and the injection's block number/timestamp
	fake.injectedLeaf = map[string]any{
		"l1_info_tree_index": 42,
		"global_exit_root":   "0x0a",
		"mainnet_exit_root":  "0x0b",
		"rollup_exit_root":   "0x0c",
		"block_num":          200,
		"timestamp":          1700000000,
	}
	injected, err = source.InjectedGER(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.NotNil(t, injected)
	require.Equal(t, uint32(1), injected.NetworkID)
	require.Equal(t, common.HexToHash("0x0a"), *injected.GER)
	require.Equal(t, common.HexToHash("0x0b"), *injected.MER)
	require.Equal(t, common.HexToHash("0x0c"), *injected.RER)
	require.Equal(t, uint64(200), *injected.BlockNumber)
	require.Equal(t, uint64(1700000000), *injected.BlockTimestamp)
	// no L2BlockNumber/L2BlockTimestamp yet: this bridge-service instance predates them, so
	// WaitingGERInjectionResolver must not mistake the L1 block above for the L2 injection one
	// (that conflation was #1818)
	require.Nil(t, injected.L2BlockNumber)
	require.Nil(t, injected.L2BlockTimestamp)
	// the fallback was attempted (no injected_l2_block_num reported) but l2GERAddrs is nil here,
	// so L2InjectionUnresolvedReason explains exactly why nothing was found
	require.Contains(t, injected.L2InjectionUnresolvedReason, "no L2GlobalExitRootAddress configured")

	// bridge-service reports the L2 injection block but not (yet) its own timestamp — e.g.
	// l2gersync syncing in Legacy mode, see InjectedL2BlockTimestamp's own doc
	fake.injectedLeaf["injected_l2_block_num"] = 999
	injected, err = source.InjectedGER(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.NotNil(t, injected)
	require.Equal(t, uint64(999), *injected.L2BlockNumber)
	require.Nil(t, injected.L2BlockTimestamp)
	require.Contains(t, injected.L2InjectionUnresolvedReason, "not its timestamp yet")

	// once the bridge-service reports the real L2 injection block/timestamp, they land on their
	// own fields — block_num/timestamp above stay the L1 event's, per InjectedL1InfoLeafHandler
	fake.injectedLeaf["injected_l2_block_timestamp"] = 1800000000
	injected, err = source.InjectedGER(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.NotNil(t, injected)
	require.Equal(t, uint64(200), *injected.BlockNumber, "the L1 block must stay untouched")
	require.Equal(t, uint64(1700000000), *injected.BlockTimestamp, "the L1 timestamp must stay untouched")
	require.Equal(t, uint64(999), *injected.L2BlockNumber)
	require.Equal(t, uint64(1800000000), *injected.L2BlockTimestamp)
	require.Empty(t, injected.L2InjectionUnresolvedReason, "fully resolved, nothing to explain")
}

// TestGERSourceInjectedGER_FallsBackToL2Scan covers the #1818 fallback: when the destination's
// bridge-service instance does not report injected_l2_block_num at all, and its
// GlobalExitRootManagerL2 address is configured (l2GERAddrs), InjectedGER finds the injection
// itself by scanning the destination network's own UpdateHashChainValue logs.
func TestGERSourceInjectedGER_FallsBackToL2Scan(t *testing.T) {
	fake := &fakeBridgeService{}
	idx := uint32(42)
	fake.l1InfoTreeIndex = &idx
	fake.injectedLeaf = map[string]any{
		"l1_info_tree_index": 42,
		"global_exit_root":   "0x0a",
		"mainnet_exit_root":  "0x0b",
		"rollup_exit_root":   "0x0c",
		"block_num":          200,
		"timestamp":          1700000000,
		// no injected_l2_block_num/injected_l2_block_timestamp: an old bridge-service instance
	}

	l2GERAddr := common.HexToAddress("0x1234")
	destNetwork := l1ToL2Bridge().DestinationNetwork

	mockL2Client := mocks.NewBaseEthereumClienter(t)
	source := NewGERSource(fake.start(t), StaticClients{destNetwork: mockL2Client}, common.Address{},
		aggkittypes.FinalizedBlock, map[uint32]common.Address{destNetwork: l2GERAddr}, 0, nil)

	head := uint64(5000) // within a single chunk (DefaultBlockChunkSize=10_000): no backward paging
	mockL2Client.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
		Return(&aggkittypes.BlockHeader{Number: head}, nil)

	targetGER := common.HexToHash("0x0a") // must match fake.injectedLeaf's global_exit_root
	otherGER := common.HexToHash("0x0b")
	blockHash := common.HexToHash("0xblockhash")
	mockL2Client.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{
		{ // an unrelated GER's injection must not be mistaken for the one being searched
			Topics:      []common.Hash{updateHashChainValueSignature, otherGER, {}},
			BlockNumber: 111,
			BlockHash:   common.HexToHash("0xother"),
		},
		{
			Topics:      []common.Hash{updateHashChainValueSignature, targetGER, {}},
			BlockNumber: 4321,
			BlockHash:   blockHash,
		},
	}, nil).Once()
	mockL2Client.EXPECT().HeaderByHash(mock.Anything, blockHash).
		Return(&gethtypes.Header{Time: 1800000000}, nil)

	injected, err := source.InjectedGER(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.NotNil(t, injected)
	require.Equal(t, uint64(200), *injected.BlockNumber, "the L1 block must stay untouched")
	require.NotNil(t, injected.L2BlockNumber)
	require.Equal(t, uint64(4321), *injected.L2BlockNumber)
	require.NotNil(t, injected.L2BlockTimestamp)
	require.Equal(t, uint64(1800000000), *injected.L2BlockTimestamp)
	require.Empty(t, injected.L2InjectionUnresolvedReason, "fully resolved via the fallback scan, nothing to explain")
}

// TestFindL2InjectionBlockBackwards exercises GERSource.findL2InjectionBlockBackwards directly:
// the paginated backward scan, its termination conditions, and how it degrades (never an error
// InjectedGERAtIndex must propagate) when the fallback simply isn't configured for the network.
func TestFindL2InjectionBlockBackwards(t *testing.T) {
	ger := common.HexToHash("0x0a")
	l2GERAddr := common.HexToAddress("0x1234")
	networkID := uint32(1)

	t.Run("network not in l2GERAddrs: no RPC call, nil result with a reason", func(t *testing.T) {
		source := NewGERSource(nil, nil, common.Address{}, aggkittypes.FinalizedBlock, nil, 0, nil)

		blockNumber, timestamp, reason, err := source.findL2InjectionBlockBackwards(t.Context(), networkID, ger)
		require.NoError(t, err)
		require.Nil(t, blockNumber)
		require.Nil(t, timestamp)
		require.Contains(t, reason, "no L2GlobalExitRootAddress configured")
	})

	t.Run("found on the very first (most recent) chunk", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		source := NewGERSource(nil, StaticClients{networkID: mockClient}, common.Address{},
			aggkittypes.FinalizedBlock, map[uint32]common.Address{networkID: l2GERAddr}, 0, nil)

		mockClient.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
			Return(&aggkittypes.BlockHeader{Number: 500}, nil)
		blockHash := common.HexToHash("0xblockhash")
		mockClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{
			{Topics: []common.Hash{updateHashChainValueSignature, ger, {}},
				BlockNumber: 400, BlockHash: blockHash},
		}, nil).Once()
		mockClient.EXPECT().HeaderByHash(mock.Anything, blockHash).
			Return(&gethtypes.Header{Time: 1700000000}, nil)

		blockNumber, timestamp, reason, err := source.findL2InjectionBlockBackwards(t.Context(), networkID, ger)
		require.NoError(t, err)
		require.Equal(t, uint64(400), *blockNumber)
		require.Equal(t, uint64(1700000000), *timestamp)
		require.Empty(t, reason, "fully resolved, nothing to explain")
	})

	t.Run("found only after paginating backwards past an empty chunk", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		// an explicit lookback, rather than relying on DefaultL2InjectionLookbackBlocks, so this
		// test's own expectations stay independent of whatever that default happens to be
		source := NewGERSource(nil, StaticClients{networkID: mockClient}, common.Address{},
			aggkittypes.FinalizedBlock, map[uint32]common.Address{networkID: l2GERAddr}, 20_000, nil)

		// head is past one full DefaultBlockChunkSize (10_000), so the first chunk covers
		// [5_000, 15_000] (empty) before the second one, [0, 4_999], finds the log
		mockClient.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
			Return(&aggkittypes.BlockHeader{Number: 15_000}, nil)
		mockClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{}, nil).Once()
		blockHash := common.HexToHash("0xblockhash")
		mockClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{
			{Topics: []common.Hash{updateHashChainValueSignature, ger, {}},
				BlockNumber: 123, BlockHash: blockHash},
		}, nil).Once()
		mockClient.EXPECT().HeaderByHash(mock.Anything, blockHash).
			Return(&gethtypes.Header{Time: 1600000000}, nil)

		blockNumber, timestamp, reason, err := source.findL2InjectionBlockBackwards(t.Context(), networkID, ger)
		require.NoError(t, err)
		require.Equal(t, uint64(123), *blockNumber)
		require.Equal(t, uint64(1600000000), *timestamp)
		require.Empty(t, reason)
	})

	t.Run("never found: scans back to genesis, returns nil with a reason, no error", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		source := NewGERSource(nil, StaticClients{networkID: mockClient}, common.Address{},
			aggkittypes.FinalizedBlock, map[uint32]common.Address{networkID: l2GERAddr}, 0, nil)

		mockClient.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
			Return(&aggkittypes.BlockHeader{Number: 500}, nil)
		mockClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{}, nil).Once()

		blockNumber, timestamp, reason, err := source.findL2InjectionBlockBackwards(t.Context(), networkID, ger)
		require.NoError(t, err)
		require.Nil(t, blockNumber)
		require.Nil(t, timestamp)
		require.Contains(t, reason, "not found within the last")
	})

	t.Run("respects a configured lookback: never scans below the floor", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		source := NewGERSource(nil, StaticClients{networkID: mockClient}, common.Address{},
			aggkittypes.FinalizedBlock, map[uint32]common.Address{networkID: l2GERAddr}, 5_000, nil)

		// floor = head (15_000) - lookback (5_000) = 10_000: the single chunk [10_000, 15_000]
		// already covers the whole allowed window, so exactly one FilterLogs call is made and the
		// scan gives up there instead of continuing down towards genesis (a second, unexpected
		// FilterLogs call would fail this test: mockClient has no expectation registered for it)
		mockClient.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
			Return(&aggkittypes.BlockHeader{Number: 15_000}, nil)
		mockClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{}, nil).Once()

		blockNumber, timestamp, reason, err := source.findL2InjectionBlockBackwards(t.Context(), networkID, ger)
		require.NoError(t, err)
		require.Nil(t, blockNumber)
		require.Nil(t, timestamp)
		require.Contains(t, reason, "not found within the last 5000 blocks")
	})

	t.Run("lookback <= 0 falls back to DefaultL2InjectionLookbackBlocks: scans back to genesis", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		source := NewGERSource(nil, StaticClients{networkID: mockClient}, common.Address{},
			aggkittypes.FinalizedBlock, map[uint32]common.Address{networkID: l2GERAddr}, 0, nil)

		// head (500) is well within DefaultL2InjectionLookbackBlocks, so the floor is genesis (0)
		// exactly as if no lookback had been configured at all
		mockClient.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
			Return(&aggkittypes.BlockHeader{Number: 500}, nil)
		mockClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{}, nil).Once()

		blockNumber, timestamp, reason, err := source.findL2InjectionBlockBackwards(t.Context(), networkID, ger)
		require.NoError(t, err)
		require.Nil(t, blockNumber)
		require.Nil(t, timestamp)
		require.NotEmpty(t, reason)
	})

	t.Run("head lookup fails: propagates the error, reason mirrors it", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		source := NewGERSource(nil, StaticClients{networkID: mockClient}, common.Address{},
			aggkittypes.FinalizedBlock, map[uint32]common.Address{networkID: l2GERAddr}, 0, nil)

		mockClient.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
			Return(nil, errors.New("boom"))

		_, _, reason, err := source.findL2InjectionBlockBackwards(t.Context(), networkID, ger)
		require.ErrorContains(t, err, "boom")
		require.Contains(t, reason, "boom")
	})

	t.Run("found, but resolving the block's timestamp fails: block number still returned", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		source := NewGERSource(nil, StaticClients{networkID: mockClient}, common.Address{},
			aggkittypes.FinalizedBlock, map[uint32]common.Address{networkID: l2GERAddr}, 0, nil)

		mockClient.EXPECT().CustomHeaderByNumber(mock.Anything, &aggkittypes.LatestBlock).
			Return(&aggkittypes.BlockHeader{Number: 500}, nil)
		blockHash := common.HexToHash("0xblockhash")
		mockClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]gethtypes.Log{
			{Topics: []common.Hash{updateHashChainValueSignature, ger, {}},
				BlockNumber: 400, BlockHash: blockHash},
		}, nil).Once()
		mockClient.EXPECT().HeaderByHash(mock.Anything, blockHash).Return(nil, errors.New("boom"))

		blockNumber, timestamp, reason, err := source.findL2InjectionBlockBackwards(t.Context(), networkID, ger)
		require.ErrorContains(t, err, "boom")
		require.Equal(t, uint64(400), *blockNumber)
		require.Nil(t, timestamp)
		require.Contains(t, reason, "boom")
	})
}

func TestClaimSourceClaimFor(t *testing.T) {
	fake := &fakeBridgeService{}
	source := NewClaimSource(fake.start(t))

	claim, err := source.ClaimFor(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.Nil(t, claim)

	// the claims lookup must filter by the bridge's global index:
	// mainnet flag set (bit 64) + deposit count 7
	expectedGlobalIndex := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(7))
	require.Equal(t, expectedGlobalIndex.String(), fake.lastGlobalIndexQuery)

	fake.claimsCount = 1
	fake.claimTxHash = "0x0d"
	fake.claimBlockNum = 50
	fake.claimBlockTimestamp = 1700000000
	claim, err = source.ClaimFor(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.Equal(t, common.HexToHash("0x0d"), claim.ClaimTx)
	require.Equal(t, uint64(50), claim.BlockNumber)
	require.Equal(t, uint64(1700000000), claim.BlockTimestamp)
}

// rootCallOutput ABI-encodes the bridge contract's getRoot() return value, like a JSON-RPC
// eth_call response would
func rootCallOutput(t *testing.T, root common.Hash) []byte {
	t.Helper()

	bridgeABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)
	output, err := bridgeABI.Methods["getRoot"].Outputs.Pack(root)
	require.NoError(t, err)
	return output
}

func TestLERSourceOriginLER(t *testing.T) {
	bridge := l2ToL1Bridge()
	bridge.BlockTimestamp = 1700000400 // the origin deposit's own block timestamp
	bridgeAddr := common.HexToAddress("0x40")
	root := common.HexToHash("0x0e")

	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().CallContract(mock.Anything, mock.MatchedBy(func(msg ethereum.CallMsg) bool {
		return msg.To != nil && *msg.To == bridgeAddr
	}), big.NewInt(int64(bridge.BlockNumber))).Return(rootCallOutput(t, root), nil)

	source, err := NewLERSource(
		StaticClients{bridge.NetworkID: client},
		staticBridgeAddressResolver{addrs: map[uint32]common.Address{bridge.NetworkID: bridgeAddr}})
	require.NoError(t, err)

	result, err := source.OriginLER(t.Context(), bridge)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, bridge.NetworkID, result.NetworkID)
	require.Equal(t, root, result.LER)
	require.Equal(t, bridge.BlockNumber, result.BlockNumber)
	require.Equal(t, bridge.BlockTimestamp, result.BlockTimestamp,
		"the same block GetRoot() was read at, free to report — no extra RPC call")
}

// TestLERSourceNeverTrustsLogDerivedAddress checks that OriginLER binds GetRoot() to the
// resolver's answer only. bridge.LogIndex here does not correspond to any real log at all (no
// FilterLogs expectation is registered on the mock client, so any attempt to fetch/inspect a
// log would fail this test) — OriginLER must never re-derive the bridge contract's address from
// the BridgeEvent log itself, only from the resolver.
func TestLERSourceNeverTrustsLogDerivedAddress(t *testing.T) {
	bridge := l2ToL1Bridge()
	bridge.LogIndex = 99 // deliberately does not match any real log
	bridgeAddr := common.HexToAddress("0x40")
	root := common.HexToHash("0x0e")

	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().CallContract(mock.Anything, mock.MatchedBy(func(msg ethereum.CallMsg) bool {
		return msg.To != nil && *msg.To == bridgeAddr
	}), big.NewInt(int64(bridge.BlockNumber))).Return(rootCallOutput(t, root), nil)

	source, err := NewLERSource(
		StaticClients{bridge.NetworkID: client},
		staticBridgeAddressResolver{addrs: map[uint32]common.Address{bridge.NetworkID: bridgeAddr}})
	require.NoError(t, err)

	result, err := source.OriginLER(t.Context(), bridge)
	require.NoError(t, err)
	require.Equal(t, root, result.LER)
}

// TestLERSourceResolverError checks that a resolver failure surfaces as an error and OriginLER
// never falls back to any other way of locating the bridge contract.
func TestLERSourceResolverError(t *testing.T) {
	bridge := l2ToL1Bridge()
	resolverErr := errors.New("resolving bridge address failed")

	client := mocks.NewBaseEthereumClienter(t)
	source, err := NewLERSource(StaticClients{bridge.NetworkID: client}, staticBridgeAddressResolver{err: resolverErr})
	require.NoError(t, err)

	_, err = source.OriginLER(t.Context(), bridge)
	require.ErrorIs(t, err, resolverErr)
}

// TestNewLERSourceRequiresResolver checks the fail-closed construction-time guard: a nil
// resolver is rejected outright.
func TestNewLERSourceRequiresResolver(t *testing.T) {
	_, err := NewLERSource(StaticClients{}, nil)
	require.Error(t, err)
}

func TestSourcesUnresolvedNetworkIsTransient(t *testing.T) {
	resolver := staticURLs{} // no networks resolved
	gerSource := NewGERSource(resolver, nil, common.Address{}, aggkittypes.FinalizedBlock, nil, 0, nil)
	claimSource := NewClaimSource(resolver)
	lerSource, err := NewLERSource(StaticClients{}, staticBridgeAddressResolver{})
	require.NoError(t, err)

	_, err = gerSource.OriginGER(t.Context(), l1ToL2Bridge())
	require.Error(t, err)
	_, err = claimSource.ClaimFor(t.Context(), l1ToL2Bridge())
	require.Error(t, err)
	_, err = lerSource.OriginLER(t.Context(), l2ToL1Bridge())
	require.Error(t, err)
}
