package sources

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
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

var testTxHash = common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234")

// l1ToL2Bridge is the BridgeInfo of an L1->L2 bridge used across the source tests
func l1ToL2Bridge() *bridgetracker.BridgeInfo {
	return &bridgetracker.BridgeInfo{
		Key:                bridgetracker.BridgeKey{NetworkID: 0, TxHash: testTxHash},
		LeafType:           trackertypes.BridgeLeafTypeAsset,
		DestinationNetwork: 1,
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
		Index:       3,
	}
}

func newBridgeEventSource(t *testing.T, client *mocks.BaseEthereumClienter) *BridgeEventSource {
	t.Helper()

	source, err := NewBridgeEventSource(StaticClients{0: client})
	require.NoError(t, err)
	return source
}

func TestBridgeEventSourceFindBridge(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(&gethtypes.Receipt{
		Status: gethtypes.ReceiptStatusSuccessful,
		Logs:   []*gethtypes.Log{bridgeEventLog(t, 1, 7)},
	}, nil)

	source := newBridgeEventSource(t, client)
	info, err := source.FindBridge(t.Context(), 0, testTxHash)
	require.NoError(t, err)
	require.Equal(t, l1ToL2Bridge(), info)
}

func TestBridgeEventSourceNotFoundCases(t *testing.T) {
	testCases := []struct {
		name    string
		receipt *gethtypes.Receipt
		err     error
	}{
		{name: "tx does not exist", err: ethereum.NotFound},
		{name: "tx reverted", receipt: &gethtypes.Receipt{Status: gethtypes.ReceiptStatusFailed}},
		{name: "no BridgeEvent log", receipt: &gethtypes.Receipt{
			Status: gethtypes.ReceiptStatusSuccessful,
			Logs:   []*gethtypes.Log{{Topics: []common.Hash{common.HexToHash("0x01")}}},
		}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := mocks.NewBaseEthereumClienter(t)
			client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(tc.receipt, tc.err)

			source := newBridgeEventSource(t, client)
			_, err := source.FindBridge(t.Context(), 0, testTxHash)
			require.ErrorIs(t, err, bridgetracker.ErrBridgeTxNotFound)
		})
	}
}

func TestBridgeEventSourceUnknownNetwork(t *testing.T) {
	source := newBridgeEventSource(t, mocks.NewBaseEthereumClienter(t))

	_, err := source.FindBridge(t.Context(), 5, testTxHash)
	require.ErrorContains(t, err, "network 5")
	require.NotErrorIs(t, err, bridgetracker.ErrBridgeTxNotFound,
		"a resolver failure is transient, not a terminal not-found")
}

// fakeBridgeService emulates the aggkit bridge service endpoints the sources consume
type fakeBridgeService struct {
	// l1InfoTreeIndex is served by /bridge/v1/l1-info-tree-index; nil -> not covered (500)
	l1InfoTreeIndex *uint32
	// injectedLeaf is served by /bridge/v1/injected-l1-info-leaf; nil -> not injected (500)
	injectedLeaf map[string]any
	// claimsCount is served by /bridge/v1/claims
	claimsCount int
	// claimTxHash and claimBlockNum populate the single claim served when claimsCount > 0
	claimTxHash   string
	claimBlockNum uint64

	// lastLeafIndexQuery records the leaf_index of the last injected-l1-info-leaf request
	lastLeafIndexQuery string
	// lastNetworkIDQuery records the network_id of the last injected-l1-info-leaf request
	lastNetworkIDQuery string
	// lastGlobalIndexQuery records the global_index of the last claims request
	lastGlobalIndexQuery string
}

func (f *fakeBridgeService) start(t *testing.T) NetworkURLResolver {
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
		fmt.Fprintf(w, `{"claims":[{"tx_hash":"%s","block_num":%d}],"count":%d}`,
			f.claimTxHash, f.claimBlockNum, f.claimsCount)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return staticURLs{1: bridgeservicefinder.NetworkURLs{BridgeURL: server.URL}}
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

func TestGERSourceOriginGER(t *testing.T) {
	fake := &fakeBridgeService{}
	source := NewGERSource(fake.start(t))

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
	source := NewGERSource(fake.start(t))

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

	// injected -> GERData with the leaf roots
	fake.injectedLeaf = map[string]any{
		"l1_info_tree_index": 42,
		"global_exit_root":   "0x0a",
		"mainnet_exit_root":  "0x0b",
		"rollup_exit_root":   "0x0c",
	}
	injected, err = source.InjectedGER(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.NotNil(t, injected)
	require.Equal(t, uint32(1), injected.NetworkID)
	require.Equal(t, common.HexToHash("0x0a"), *injected.GER)
	require.Equal(t, common.HexToHash("0x0b"), *injected.MER)
	require.Equal(t, common.HexToHash("0x0c"), *injected.RER)
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
	claim, err = source.ClaimFor(t.Context(), l1ToL2Bridge())
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.Equal(t, common.HexToHash("0x0d"), claim.ClaimTx)
	require.Equal(t, uint64(50), claim.BlockNumber)
}

func TestSourcesUnresolvedNetworkIsTransient(t *testing.T) {
	resolver := staticURLs{} // no networks resolved
	gerSource := NewGERSource(resolver)
	claimSource := NewClaimSource(resolver)

	_, err := gerSource.OriginGER(t.Context(), l1ToL2Bridge())
	require.Error(t, err)
	_, err = claimSource.ClaimFor(t.Context(), l1ToL2Bridge())
	require.Error(t, err)
}
