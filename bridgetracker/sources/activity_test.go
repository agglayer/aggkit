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

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const testFromAddress = "0x1111111111111111111111111111111111111111"

// fakeActivityBridgeService emulates the bridge-service endpoints ActivitySource consumes:
// GET /bridge/v1/bridges (paginated, filtered by network_id/from_address) and
// GET /bridge/v1/claims (filtered by network_id/global_index).
type fakeActivityBridgeService struct {
	// bridgesByNetwork holds every bridge served for a network, in page order
	bridgesByNetwork map[uint32][]*bridgeservicetypes.BridgeResponse
	// claimsByGlobalIndex holds the claim served for a given global index (decimal string), if any
	claimsByGlobalIndex map[string]*bridgeservicetypes.ClaimResponse
}

func (f *fakeActivityBridgeService) start(t *testing.T) string {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/bridge/v1/bridges", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		networkID, err := strconv.ParseUint(q.Get("network_id"), 10, 32)
		require.NoError(t, err)
		pageNumber, err := strconv.Atoi(q.Get("page_number"))
		require.NoError(t, err)
		pageSize, err := strconv.Atoi(q.Get("page_size"))
		require.NoError(t, err)

		var matching []*bridgeservicetypes.BridgeResponse
		for _, b := range f.bridgesByNetwork[uint32(networkID)] {
			if from := q.Get("from_address"); from != "" && (b.FromAddress == nil || string(*b.FromAddress) != from) {
				continue
			}
			matching = append(matching, b)
		}

		start := (pageNumber - 1) * pageSize
		end := min(start+pageSize, len(matching))
		if start > len(matching) {
			start = len(matching)
		}
		page := matching[start:end]

		require.NoError(t, json.NewEncoder(w).Encode(bridgeservicetypes.BridgesResult{
			Bridges: page, Count: len(matching),
		}))
	})
	mux.HandleFunc("/bridge/v1/claims", func(w http.ResponseWriter, r *http.Request) {
		globalIndex := r.URL.Query().Get("global_index")
		claim, ok := f.claimsByGlobalIndex[globalIndex]
		if !ok {
			require.NoError(t, json.NewEncoder(w).Encode(bridgeservicetypes.ClaimsResult{Count: 0}))
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(bridgeservicetypes.ClaimsResult{
			Claims: []*bridgeservicetypes.ClaimResponse{claim}, Count: 1,
		}))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

// fakeNetworkLister is a fixed NetworkLister for tests: every networkID resolves to the same
// bridge service base URL. bridgeAddrs backs BridgeAddress; a networkID absent from it errors
// (bridgeAddrErr if set, a generic "not configured" error otherwise), mirroring
// bridgeservicefinder's own behaviour when neither an override nor the on-chain default applies.
type fakeNetworkLister struct {
	networkIDs    []uint32
	url           string
	bridgeAddrs   map[uint32]common.Address
	bridgeAddrErr error
}

func (f fakeNetworkLister) GetURL(uint32) (bridgeservicefinder.NetworkURLs, error) {
	return bridgeservicefinder.NetworkURLs{BridgeURL: f.url}, nil
}

func (f fakeNetworkLister) NetworkIDs() []uint32 { return f.networkIDs }

func (f fakeNetworkLister) BridgeAddress(_ context.Context, networkID uint32) (common.Address, error) {
	if addr, ok := f.bridgeAddrs[networkID]; ok {
		return addr, nil
	}
	if f.bridgeAddrErr != nil {
		return common.Address{}, f.bridgeAddrErr
	}
	return common.Address{}, fmt.Errorf("no bridge contract address configured for network %d", networkID)
}

func bridgeResponse(networkID, destNetwork, depositCount uint32, from string, globalIndex int64) *bridgeservicetypes.BridgeResponse {
	fromAddr := bridgeservicetypes.Address(from)
	return &bridgeservicetypes.BridgeResponse{
		OriginNetwork:      networkID,
		DestinationNetwork: destNetwork,
		DepositCount:       depositCount,
		FromAddress:        &fromAddr,
		GlobalIndex:        big.NewInt(globalIndex),
		TxHash:             bridgeservicetypes.Hash(fmt.Sprintf("0x%d", globalIndex)),
	}
}

// TestActivitySource_BridgesFrom_PaginatesAndScansEveryNetwork verifies BridgesFrom pages
// through each network until a short page, scans every network the lister reports, and filters
// by from_address.
func TestActivitySource_BridgesFrom_PaginatesAndScansEveryNetwork(t *testing.T) {
	other := "0x2222222222222222222222222222222222222222"
	svc := &fakeActivityBridgeService{
		bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
			1: {
				bridgeResponse(1, 2, 0, testFromAddress, 1),
				bridgeResponse(1, 2, 1, testFromAddress, 2),
				bridgeResponse(1, 2, 2, testFromAddress, 3),
				bridgeResponse(1, 2, 3, other, 4), // different sender, must be filtered out
			},
			2: {
				bridgeResponse(2, 1, 0, testFromAddress, 5),
			},
		},
	}
	url := svc.start(t)
	lister := fakeNetworkLister{networkIDs: []uint32{1, 2}, url: url}

	source := NewActivitySource(lister, nil)

	items, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
	require.NoError(t, err)
	require.Len(t, items, 4)

	globalIndexes := make([]int64, 0, len(items))
	for _, item := range items {
		globalIndexes = append(globalIndexes, item.GlobalIndex.Int64())
	}
	require.ElementsMatch(t, []int64{1, 2, 3, 5}, globalIndexes)
}

// TestFetchNewBridgesFrom_Pagination exercises the pagination loop directly with a small page
// size, so a short page (fewer results than requested) stops the loop.
func TestFetchNewBridgesFrom_Pagination(t *testing.T) {
	svc := &fakeActivityBridgeService{
		bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
			1: {
				bridgeResponse(1, 2, 0, testFromAddress, 1),
				bridgeResponse(1, 2, 1, testFromAddress, 2),
				bridgeResponse(1, 2, 2, testFromAddress, 3),
			},
		},
	}
	url := svc.start(t)
	lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
	source := NewActivitySource(lister, nil)
	client, err := source.services.aggkitBridgeClientFor(1)
	require.NoError(t, err)

	items, err := fetchNewBridgesFrom(t.Context(), client, 1, testFromAddress, 2, nil)
	require.NoError(t, err)
	require.Len(t, items, 3)
}

// TestFetchNewBridgesFrom_StopsAtFirstKnownBridge verifies pagination stops as soon as an
// already-known bridge is reached, without walking further pages, and returns only the bridges
// found before it (the newer ones, per the server's newest-first order).
func TestFetchNewBridgesFrom_StopsAtFirstKnownBridge(t *testing.T) {
	// bridgesByNetwork is given newest-first (global index 3, then 2, then 1), matching the real
	// bridge service's own deposit_count DESC order
	svc := &fakeActivityBridgeService{
		bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
			1: {
				bridgeResponse(1, 2, 2, testFromAddress, 3),
				bridgeResponse(1, 2, 1, testFromAddress, 2), // already known: pagination stops here
				bridgeResponse(1, 2, 0, testFromAddress, 1), // must never be fetched
			},
		},
	}
	url := svc.start(t)
	lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
	source := NewActivitySource(lister, nil)
	client, err := source.services.aggkitBridgeClientFor(1)
	require.NoError(t, err)

	known := map[string]struct{}{"2": {}}
	items, err := fetchNewBridgesFrom(t.Context(), client, 1, testFromAddress, 1, known)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(3), items[0].GlobalIndex.Int64())
}

// TestActivitySource_IsClaimed_NoBridgeAddrConfigured verifies IsClaimed errors clearly when
// the destination network has no bridge contract address configured.
func TestActivitySource_IsClaimed_NoBridgeAddrConfigured(t *testing.T) {
	source := NewActivitySource(fakeNetworkLister{}, StaticClients{})

	bridge := bridgeResponse(1, 2, 3, testFromAddress, 1)
	_, err := source.IsClaimed(t.Context(), bridge)
	require.ErrorContains(t, err, "no bridge contract address configured for network 2")
}

// TestActivitySource_IsClaimed_CallsContractWithDepositCountAndOriginNetwork verifies IsClaimed
// binds the destination network's contract and calls isClaimed(depositCount, originNetwork), and
// that the binding is cached across calls.
func TestActivitySource_IsClaimed_CallsContractWithDepositCountAndOriginNetwork(t *testing.T) {
	destAddr := common.HexToAddress("0xdead")
	client := StaticClients{2: nil}

	stub := &stubClaimChecker{claimed: true}
	buildCalls := 0
	lister := fakeNetworkLister{bridgeAddrs: map[uint32]common.Address{2: destAddr}}
	source := NewActivitySource(lister, client)
	source.newContract = func(addr common.Address, _ aggkittypes.BaseEthereumClienter) (claimChecker, error) {
		buildCalls++
		require.Equal(t, destAddr, addr)
		return stub, nil
	}

	bridge := bridgeResponse(5, 2, 9, testFromAddress, 1)
	claimed, err := source.IsClaimed(t.Context(), bridge)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, uint32(9), stub.lastLeafIndex)
	require.Equal(t, uint32(5), stub.lastSourceNetwork)

	// A second call for the same destination network reuses the cached binding
	_, err = source.IsClaimed(t.Context(), bridge)
	require.NoError(t, err)
	require.Equal(t, 1, buildCalls)
}

// stubClaimChecker is an injectable claimChecker for tests
type stubClaimChecker struct {
	claimed           bool
	err               error
	lastLeafIndex     uint32
	lastSourceNetwork uint32
}

func (s *stubClaimChecker) IsClaimed(_ *bind.CallOpts, leafIndex, sourceBridgeNetwork uint32) (bool, error) {
	s.lastLeafIndex = leafIndex
	s.lastSourceNetwork = sourceBridgeNetwork
	return s.claimed, s.err
}

// TestActivitySource_ClaimInfo verifies ClaimInfo fetches the raw claim record by global index,
// and returns nil (not an error) when the destination bridge service has not indexed it yet.
func TestActivitySource_ClaimInfo(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx", GlobalIndex: "1"}
	svc := &fakeActivityBridgeService{
		claimsByGlobalIndex: map[string]*bridgeservicetypes.ClaimResponse{"1": claim},
	}
	url := svc.start(t)
	lister := fakeNetworkLister{networkIDs: []uint32{2}, url: url}
	source := NewActivitySource(lister, nil)

	found := bridgeResponse(1, 2, 0, testFromAddress, 1)
	got, err := source.ClaimInfo(t.Context(), found)
	require.NoError(t, err)
	require.Equal(t, claim, got)

	notIndexedYet := bridgeResponse(1, 2, 0, testFromAddress, 999)
	got, err = source.ClaimInfo(t.Context(), notIndexedYet)
	require.NoError(t, err)
	require.Nil(t, got)
}
