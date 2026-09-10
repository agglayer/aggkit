package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const testFromAddress = "0x1111111111111111111111111111111111111111"

// mustNewActivitySource builds an ActivitySource for tests with the RPC-based fallback disabled
// (zero-value config, matching ActivitySource's behavior before that fallback existed), failing
// the test immediately if construction errors — it never should for this fixed, RPC-disabled
// configuration. See activity_rpc_test.go for the RPC-fallback-enabled test cases.
func mustNewActivitySource(
	t *testing.T, finder NetworkLister, ethClients EthClientResolver, logger aggkitcommon.Logger,
) *ActivitySource {
	t.Helper()
	source, err := NewActivitySource(finder, ethClients, logger,
		bridgetracker.ActivitySourceBridgeServiceConfig{}, bridgetracker.ActivitySourceRPCConfig{})
	require.NoError(t, err)
	return source
}

// fakeActivityBridgeService emulates the bridge-service endpoints ActivitySource consumes:
// GET /bridge/v1/bridges (paginated, filtered by network_id/from_address) and
// GET /bridge/v1/claims (filtered by network_id/global_index).
type fakeActivityBridgeService struct {
	// bridgesByNetwork holds every bridge served for a network, in page order
	bridgesByNetwork map[uint32][]*bridgeservicetypes.BridgeResponse
	// claimsByGlobalIndex holds the claim served for a given global index (decimal string), if any
	claimsByGlobalIndex map[string]*bridgeservicetypes.ClaimResponse
	// syncStatus is served as-is for GET /bridge/v1/sync-status; a nil value (the default) serves
	// an empty (all zero-value) status, which isNetworkSynced reads as "not synced"
	syncStatus *bridgeservicetypes.SyncStatus
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
	mux.HandleFunc("/bridge/v1/sync-status", func(w http.ResponseWriter, r *http.Request) {
		status := f.syncStatus
		if status == nil {
			status = &bridgeservicetypes.SyncStatus{}
		}
		require.NoError(t, json.NewEncoder(w).Encode(status))
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
		GlobalIndex:        bridgeservicetypes.BigIntString(big.NewInt(globalIndex).String()),
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

	source := mustNewActivitySource(t, lister, nil, testLogger)

	items, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, items, 4)

	globalIndexes := make([]int64, 0, len(items))
	for _, item := range items {
		globalIndexes = append(globalIndexes, item.Bridge.GlobalIndex.ToBigInt().Int64())
	}
	require.ElementsMatch(t, []int64{1, 2, 3, 5}, globalIndexes)
}

// TestActivitySource_BridgesFrom_SkipsUnreachableNetworkAndWarns verifies a network whose bridge
// service cannot be resolved is skipped rather than failing the whole scan, and is reported back
// as an ActivityWarning — every other network's bridges are still returned.
func TestActivitySource_BridgesFrom_SkipsUnreachableNetworkAndWarns(t *testing.T) {
	svc := &fakeActivityBridgeService{
		bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
			1: {bridgeResponse(1, 2, 0, testFromAddress, 1)},
		},
	}
	url := svc.start(t)
	// networkID 2 resolves to an empty bridge service URL, which aggkitBridgeClientFor rejects
	lister := fakeMixedNetworkLister{
		networkIDs: []uint32{1, 2},
		urls:       map[uint32]string{1: url},
	}

	source := mustNewActivitySource(t, lister, nil, testLogger)

	items, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(1), items[0].Bridge.GlobalIndex.ToBigInt().Int64())

	require.Len(t, warnings, 1)
	require.Equal(t, uint32(2), warnings[0].NetworkID)
	require.Contains(t, warnings[0].Message, "network 2")
}

// fakeMixedNetworkLister is a NetworkLister whose GetURL result varies per network, unlike
// fakeNetworkLister's single fixed URL — used to simulate one network being unreachable while
// others resolve fine.
type fakeMixedNetworkLister struct {
	networkIDs []uint32
	urls       map[uint32]string
}

func (f fakeMixedNetworkLister) GetURL(networkID uint32) (bridgeservicefinder.NetworkURLs, error) {
	return bridgeservicefinder.NetworkURLs{BridgeURL: f.urls[networkID]}, nil
}

func (f fakeMixedNetworkLister) NetworkIDs() []uint32 { return f.networkIDs }

func (f fakeMixedNetworkLister) BridgeAddress(context.Context, uint32) (common.Address, error) {
	return common.Address{}, errors.New("not implemented")
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
	source := mustNewActivitySource(t, lister, nil, testLogger)
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
	source := mustNewActivitySource(t, lister, nil, testLogger)
	client, err := source.services.aggkitBridgeClientFor(1)
	require.NoError(t, err)

	known := map[string]struct{}{"2": {}}
	items, err := fetchNewBridgesFrom(t.Context(), client, 1, testFromAddress, 1, known)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(3), items[0].Bridge.GlobalIndex.ToBigInt().Int64())
}

// TestActivitySource_IsClaimed_NoBridgeAddrConfigured verifies IsClaimed errors clearly when
// the destination network has no bridge contract address configured.
func TestActivitySource_IsClaimed_NoBridgeAddrConfigured(t *testing.T) {
	source := mustNewActivitySource(t, fakeNetworkLister{}, StaticClients{}, testLogger)

	bridge := &domain.ScannedBridge{Bridge: bridgeResponse(1, 2, 3, testFromAddress, 1), NetworkID: 1}
	_, err := source.IsClaimed(t.Context(), bridge)
	require.ErrorContains(t, err, "no bridge contract address configured for network 2")
}

// TestActivitySource_IsClaimed_UsesScannedNetworkNotBridgeOriginNetwork verifies IsClaimed binds
// the destination network's contract and calls isClaimed(depositCount, scannedNetworkID) using
// ScannedBridge.NetworkID — never Bridge.OriginNetwork, which is set here to a deliberately
// different value to prove the two are not confused (see domain.ScannedBridge) — and that the
// binding is cached across calls.
func TestActivitySource_IsClaimed_UsesScannedNetworkNotBridgeOriginNetwork(t *testing.T) {
	destAddr := common.HexToAddress("0xdead")
	client := StaticClients{2: nil}

	stub := &stubClaimChecker{claimed: true}
	buildCalls := 0
	lister := fakeNetworkLister{bridgeAddrs: map[uint32]common.Address{2: destAddr}}
	source := mustNewActivitySource(t, lister, client, testLogger)
	source.newContract = func(addr common.Address, _ aggkittypes.BaseEthereumClienter) (claimChecker, error) {
		buildCalls++
		require.Equal(t, destAddr, addr)
		return stub, nil
	}

	// OriginNetwork (99) is the asset-origin decoy: isClaimed must use NetworkID (5, the network
	// the deposit tx was actually sent to) instead
	const scannedNetworkID = uint32(5)
	bridge := &domain.ScannedBridge{Bridge: bridgeResponse(99, 2, 9, testFromAddress, 1), NetworkID: scannedNetworkID}
	claimed, err := source.IsClaimed(t.Context(), bridge)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, uint32(9), stub.lastLeafIndex)
	require.Equal(t, scannedNetworkID, stub.lastSourceNetwork)

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

// listerFromResolver adapts a plain NetworkURLResolver (e.g. fakeBridgeService.start's result,
// see sources_test.go) into a NetworkLister for tests that only exercise IsReadyToClaim, which
// never calls NetworkIDs/BridgeAddress
type listerFromResolver struct {
	NetworkURLResolver
}

func (listerFromResolver) NetworkIDs() []uint32 { return nil }

func (listerFromResolver) BridgeAddress(context.Context, uint32) (common.Address, error) {
	return common.Address{}, nil
}

// TestActivitySource_IsReadyToClaim verifies IsReadyToClaim resolves the covering L1 info tree
// leaf (GET /bridge/v1/l1-info-tree-index) against the origin network's own bridge-service
// instance — per bridgeServiceClients.l1InfoTreeIndexClientFor's routing rule, the only instance
// the real l1-info-tree-index endpoint accepts a non-mainnet network_id from — then whether that
// leaf has been injected on the destination (GET /bridge/v1/injected-l1-info-leaf, always asked
// of the destination's own instance). Reports true only once both resolve, and false (never an
// error) while either is still pending. Origin and destination are deliberately different
// networks with separate fake servers, so a regression back to querying the destination for
// l1-info-tree-index (#1831 review finding) fails these tests instead of just misbehaving in
// production against the real endpoint's network_id validation.
func TestActivitySource_IsReadyToClaim(t *testing.T) {
	// bridge is scanned from network 2 (NetworkID, the deposit's own network) with destination
	// network 1
	bridge := &domain.ScannedBridge{Bridge: bridgeResponse(2, 1, 5, testFromAddress, 1), NetworkID: 2}

	t.Run("covering leaf resolved and injected -> ready", func(t *testing.T) {
		leafIndex := uint32(7)
		originSvc := &fakeBridgeService{l1InfoTreeIndex: &leafIndex}
		destSvc := &fakeBridgeService{injectedLeaf: map[string]any{"global_exit_root": "0xger"}}
		urls := originSvc.startAt(t, 2).merge(destSvc.startAt(t, 1))
		lister := listerFromResolver{urls}
		source := mustNewActivitySource(t, lister, nil, testLogger)

		ready, err := source.IsReadyToClaim(t.Context(), bridge)
		require.NoError(t, err)
		require.True(t, ready)
		require.Equal(t, "7", destSvc.lastLeafIndexQuery)
		require.Equal(t, "1", destSvc.lastNetworkIDQuery, "queried against the destination network")
	})

	t.Run("not covered by any L1 info tree leaf yet -> not ready, no error", func(t *testing.T) {
		originSvc := &fakeBridgeService{} // l1InfoTreeIndex nil -> not covered yet
		destSvc := &fakeBridgeService{}
		urls := originSvc.startAt(t, 2).merge(destSvc.startAt(t, 1))
		lister := listerFromResolver{urls}
		source := mustNewActivitySource(t, lister, nil, testLogger)

		ready, err := source.IsReadyToClaim(t.Context(), bridge)
		require.NoError(t, err)
		require.False(t, ready)
	})

	t.Run("covered but not injected on the destination yet -> not ready, no error", func(t *testing.T) {
		leafIndex := uint32(7)
		originSvc := &fakeBridgeService{l1InfoTreeIndex: &leafIndex}
		destSvc := &fakeBridgeService{} // injectedLeaf nil -> not injected yet
		urls := originSvc.startAt(t, 2).merge(destSvc.startAt(t, 1))
		lister := listerFromResolver{urls}
		source := mustNewActivitySource(t, lister, nil, testLogger)

		ready, err := source.IsReadyToClaim(t.Context(), bridge)
		require.NoError(t, err)
		require.False(t, ready)
	})

	t.Run("origin network unresolvable -> genuine error, destination never queried", func(t *testing.T) {
		destSvc := &fakeBridgeService{}
		urls := destSvc.startAt(t, 1) // network 2 (the origin) deliberately absent
		lister := listerFromResolver{urls}
		source := mustNewActivitySource(t, lister, nil, testLogger)

		_, err := source.IsReadyToClaim(t.Context(), bridge)
		require.Error(t, err)
	})

	t.Run("destination network unresolvable -> genuine error", func(t *testing.T) {
		leafIndex := uint32(7)
		originSvc := &fakeBridgeService{l1InfoTreeIndex: &leafIndex}
		urls := originSvc.startAt(t, 2) // network 1 (the destination) deliberately absent
		lister := listerFromResolver{urls}
		source := mustNewActivitySource(t, lister, nil, testLogger)

		_, err := source.IsReadyToClaim(t.Context(), bridge)
		require.Error(t, err)
	})
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
	source := mustNewActivitySource(t, lister, nil, testLogger)

	found := &domain.ScannedBridge{Bridge: bridgeResponse(1, 2, 0, testFromAddress, 1), NetworkID: 1}
	got, err := source.ClaimInfo(t.Context(), found)
	require.NoError(t, err)
	require.Equal(t, claim, got)

	notIndexedYet := &domain.ScannedBridge{Bridge: bridgeResponse(1, 2, 0, testFromAddress, 999), NetworkID: 1}
	got, err = source.ClaimInfo(t.Context(), notIndexedYet)
	require.NoError(t, err)
	require.Nil(t, got)
}

// stubRPCScanner is an injectable activityBridgeRPCScanner for tests: it returns bridges[networkID]
// (nil if the network is absent) or err, never both.
type stubRPCScanner struct {
	bridges map[uint32][]*domain.ScannedBridge
	err     error
}

func (s stubRPCScanner) BridgesFrom(
	_ context.Context, networkID uint32, _ common.Address,
) ([]*domain.ScannedBridge, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.bridges[networkID], nil
}

// activitySourceWithRPC builds an ActivitySource exactly like mustNewActivitySource, except its
// rpc field is rpc instead of nil — used to exercise BridgesFrom's REST/RPC merge logic (see
// scanNetwork) without a full RPC mock (activity_rpc_test.go already covers *activityRPCScanner's
// own scanning logic directly).
func activitySourceWithRPC(finder NetworkLister, rpc activityBridgeRPCScanner) *ActivitySource {
	return &ActivitySource{
		logger:                testLogger,
		services:              newBridgeServiceClients(finder),
		finder:                finder,
		pageSize:              bridgetracker.DefaultActivitySourceBridgeServicePageSize,
		rpc:                   rpc,
		contractClaimCheckers: newContractClaimCheckers(finder, nil),
	}
}

// TestActivitySource_BridgesFrom_RPCMerge covers agglayer/aggkit#1837's merge/warning matrix
// between the bridge-service (REST) scan and the RPC-based fallback for one network.
func TestActivitySource_BridgesFrom_RPCMerge(t *testing.T) {
	other := "0x2222222222222222222222222222222222222222"

	t.Run("REST ok, synced, RPC finds an extra bridge -> merged silently, no warning", func(t *testing.T) {
		svc := &fakeActivityBridgeService{
			bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
				1: {bridgeResponse(1, 2, 0, testFromAddress, 1)},
			},
			syncStatus: &bridgeservicetypes.SyncStatus{L2Info: &bridgeservicetypes.NetworkSyncInfo{IsSynced: true}},
		}
		url := svc.start(t)
		lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
		rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
			1: {
				{Bridge: bridgeResponse(1, 2, 0, testFromAddress, 1), NetworkID: 1}, // duplicate of the REST result
				{Bridge: bridgeResponse(1, 2, 1, testFromAddress, 2), NetworkID: 1}, // new
			},
		}}
		source := activitySourceWithRPC(lister, rpc)

		items, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Empty(t, warnings)
		globalIndexes := make([]int64, 0, len(items))
		for _, item := range items {
			globalIndexes = append(globalIndexes, item.Bridge.GlobalIndex.ToBigInt().Int64())
		}
		require.ElementsMatch(t, []int64{1, 2}, globalIndexes)
	})

	t.Run("REST ok, not synced, RPC finds an extra bridge -> merged with a warning", func(t *testing.T) {
		svc := &fakeActivityBridgeService{
			bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
				1: {bridgeResponse(1, 2, 0, testFromAddress, 1)},
			},
			syncStatus: &bridgeservicetypes.SyncStatus{L2Info: &bridgeservicetypes.NetworkSyncInfo{IsSynced: false}},
		}
		url := svc.start(t)
		lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
		rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
			1: {{Bridge: bridgeResponse(1, 2, 1, testFromAddress, 2), NetworkID: 1}},
		}}
		source := activitySourceWithRPC(lister, rpc)

		items, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Len(t, items, 2)
		require.Len(t, warnings, 1)
		require.Equal(t, uint32(1), warnings[0].NetworkID)
		require.Contains(t, warnings[0].Message, "not fully synchronized")
	})

	t.Run("REST ok, RPC finds nothing new -> no warning even when not synced", func(t *testing.T) {
		svc := &fakeActivityBridgeService{
			bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
				1: {bridgeResponse(1, 2, 0, testFromAddress, 1)},
			},
			syncStatus: &bridgeservicetypes.SyncStatus{L2Info: &bridgeservicetypes.NetworkSyncInfo{IsSynced: false}},
		}
		url := svc.start(t)
		lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
		rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
			1: {{Bridge: bridgeResponse(1, 2, 0, testFromAddress, 1), NetworkID: 1}}, // duplicate only
		}}
		source := activitySourceWithRPC(lister, rpc)

		items, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Empty(t, warnings)
	})

	t.Run("REST unreachable, RPC ok -> RPC-only results, warns that historical activity may be unavailable", func(t *testing.T) {
		// networkID 2 resolves to an empty bridge service URL, which aggkitBridgeClientFor rejects
		lister := fakeMixedNetworkLister{networkIDs: []uint32{2}, urls: map[uint32]string{}}
		rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
			2: {{Bridge: bridgeResponse(2, 1, 0, testFromAddress, 3), NetworkID: 2}},
		}}
		source := activitySourceWithRPC(lister, rpc)

		items, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, int64(3), items[0].Bridge.GlobalIndex.ToBigInt().Int64())
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0].Message, "historical activity may not be available")
	})

	t.Run("REST unreachable, RPC also fails -> unchanged legacy behavior: skipped, warns about the REST error", func(t *testing.T) {
		lister := fakeMixedNetworkLister{networkIDs: []uint32{2}, urls: map[uint32]string{}}
		rpc := stubRPCScanner{err: errors.New("rpc down")}
		source := activitySourceWithRPC(lister, rpc)

		items, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Empty(t, items)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0].Message, "fetching bridges from")
		require.NotContains(t, warnings[0].Message, "historical activity may not be available")
	})

	t.Run("REST ok, RPC fails -> REST results returned untouched, no warning", func(t *testing.T) {
		svc := &fakeActivityBridgeService{
			bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
				1: {bridgeResponse(1, 2, 0, testFromAddress, 1), bridgeResponse(1, 2, 3, other, 4)},
			},
		}
		url := svc.start(t)
		lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
		rpc := stubRPCScanner{err: errors.New("rpc down")}
		source := activitySourceWithRPC(lister, rpc)

		items, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Len(t, items, 1) // other's bridge was already filtered server-side by from_address
		require.Empty(t, warnings)
	})
}
