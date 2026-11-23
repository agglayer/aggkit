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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
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
	// syncStatusCalls counts GET /bridge/v1/sync-status requests received, so tests can assert it
	// is (not) called (see scanNetwork's "skip when the RPC fallback is disabled" behavior)
	syncStatusCalls atomic.Int32

	// mu guards bridgesByNetwork (for tests that change it between scans), bridgesStatus and
	// pages
	mu sync.Mutex
	// bridgesStatus, when non-zero, is the HTTP status GET /bridge/v1/bridges answers with
	// instead of the listing (e.g. 503 while the bridge service resolves a reorg)
	bridgesStatus int
	// pages counts GET /bridge/v1/bridges requests per page number
	pages map[int]int
	// beforeBridges, if set, runs (holding mu) before each GET /bridge/v1/bridges request is
	// served, with the requested page number: lets a test change the listing in the middle of a
	// scan by writing bridgesByNetwork directly
	beforeBridges func(page int)
}

// setBridges replaces the listing served for networkID
func (f *fakeActivityBridgeService) setBridges(networkID uint32, bridges ...*bridgeservicetypes.BridgeResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bridgesByNetwork == nil {
		f.bridgesByNetwork = make(map[uint32][]*bridgeservicetypes.BridgeResponse)
	}
	f.bridgesByNetwork[networkID] = bridges
}

// setBridgesStatus makes GET /bridge/v1/bridges answer status (0 restores the listing)
func (f *fakeActivityBridgeService) setBridgesStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bridgesStatus = status
}

// pageRequests returns how many times each page of GET /bridge/v1/bridges was requested since
// the last call, and resets the counters
func (f *fakeActivityBridgeService) pageRequests() map[int]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	pages := f.pages
	f.pages = nil
	return pages
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

		f.mu.Lock()
		defer f.mu.Unlock()
		if f.pages == nil {
			f.pages = make(map[int]int)
		}
		f.pages[pageNumber]++
		if f.beforeBridges != nil {
			f.beforeBridges(pageNumber)
		}
		if f.bridgesStatus != 0 {
			w.WriteHeader(f.bridgesStatus)
			_, _ = w.Write([]byte(`{"error":"a syncer is temporarily inconsistent (reorg being resolved), retry later"}`))
			return
		}

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
		f.syncStatusCalls.Add(1)
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

// knownBridge is the domain.KnownBridge of b as a cache holding it from source would report it
func knownBridge(b *bridgeservicetypes.BridgeResponse, source domain.ActivitySourceKind, final bool) domain.KnownBridge {
	return domain.KnownBridge{
		TxHash: b.TxHash, BlockNum: b.BlockNum, BlockTimestamp: b.BlockTimestamp, DepositCount: b.DepositCount,
		Source: source, NetworkID: b.OriginNetwork, Final: final,
	}
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

	items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, items, 4)

	globalIndexes := make([]int64, 0, len(items))
	for _, item := range items {
		globalIndexes = append(globalIndexes, item.Bridge.GlobalIndex.ToBigInt().Int64())
		require.Equal(t, domain.ActivitySourceBridgeService, item.Source, "every bridge came from the REST scan")
	}
	require.ElementsMatch(t, []int64{1, 2, 3, 5}, globalIndexes)
}

// TestActivitySource_BridgesFrom_SkipsSyncStatusWhenRPCDisabled verifies GET /bridge/v1/sync-status
// is never called when the RPC-based fallback is disabled (mustNewActivitySource's default): that
// call only ever informs the "not fully synchronized" warning, which cannot fire without the RPC
// fallback enabled, so making it unconditionally would add a needless round trip (and a needless
// dependency on the endpoint existing) to every activity request for deployments that never
// opted into the fallback.
func TestActivitySource_BridgesFrom_SkipsSyncStatusWhenRPCDisabled(t *testing.T) {
	svc := &fakeActivityBridgeService{
		bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
			1: {bridgeResponse(1, 2, 0, testFromAddress, 1)},
		},
	}
	url := svc.start(t)
	lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
	source := mustNewActivitySource(t, lister, nil, testLogger)

	_, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, int32(0), svc.syncStatusCalls.Load())
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

	items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(1), items[0].Bridge.GlobalIndex.ToBigInt().Int64())

	require.Len(t, warnings, 1)
	require.Equal(t, uint32(2), warnings[0].NetworkID)
	require.Contains(t, warnings[0].Message, "network 2")
}

// TestActivitySource_BridgesFrom_BoundsConcurrentNetworkScans pins that BridgesFrom never scans
// more than ActivitySourceBridgeServiceConfig.MaxConcurrentNetworkScans networks at once,
// regardless of how many the finder reports: unbounded per-request concurrency could otherwise
// fan out one goroutine (two outbound calls apiece, see scanNetwork) per configured network, all
// at once. The fake bridge service gates every GET /bridge/v1/bridges request on a shared
// counter/channel pair, so the scans of every network but MaxConcurrentNetworkScans of them stay
// parked until the test releases them.
func TestActivitySource_BridgesFrom_BoundsConcurrentNetworkScans(t *testing.T) {
	t.Parallel()

	const (
		maxConcurrent = 2
		networkCount  = 5
	)

	var (
		mu          sync.Mutex
		inFlight    int
		maxObserved int
	)
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/bridge/v1/bridges", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > maxObserved {
			maxObserved = inFlight
		}
		mu.Unlock()

		<-release

		mu.Lock()
		inFlight--
		mu.Unlock()

		require.NoError(t, json.NewEncoder(w).Encode(bridgeservicetypes.BridgesResult{}))
	})
	mux.HandleFunc("/bridge/v1/sync-status", func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(bridgeservicetypes.SyncStatus{}))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	networkIDs := make([]uint32, networkCount)
	for i := range networkIDs {
		networkIDs[i] = uint32(i + 1)
	}
	lister := fakeNetworkLister{networkIDs: networkIDs, url: server.URL}

	source, err := NewActivitySource(lister, nil, testLogger,
		bridgetracker.ActivitySourceBridgeServiceConfig{MaxConcurrentNetworkScans: maxConcurrent},
		bridgetracker.ActivitySourceRPCConfig{})
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		_, _, _, _ = source.BridgesFrom(context.Background(), common.HexToAddress(testFromAddress), nil)
		close(done)
	}()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return inFlight == maxConcurrent
	}, time.Second, time.Millisecond, "exactly maxConcurrent scans should be in flight, the rest parked")

	// give any bound violation a chance to show up before asserting on maxObserved
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	observed := maxObserved
	mu.Unlock()
	require.Equal(t, maxConcurrent, observed, "no more than MaxConcurrentNetworkScans networks scan at once")

	close(release)
	<-done
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

// TestScanBridgeService_Pagination verifies a first scan (nothing known) pages through the whole
// listing until a short page and returns every bridge
func TestScanBridgeService_Pagination(t *testing.T) {
	svc := &fakeActivityBridgeService{
		bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
			1: {
				bridgeResponse(1, 2, 2, testFromAddress, 3),
				bridgeResponse(1, 2, 1, testFromAddress, 2),
				bridgeResponse(1, 2, 0, testFromAddress, 1),
			},
		},
	}
	url := svc.start(t)
	lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
	source := mustNewActivitySource(t, lister, nil, testLogger)
	client, err := source.services.aggkitBridgeClientFor(1)
	require.NoError(t, err)

	scan, err := scanBridgeService(t.Context(), client, 1, testFromAddress, 2, nil, testLogger)
	require.NoError(t, err)
	require.Len(t, scan.bridges, 3)
	require.Empty(t, scan.removed)
	require.Zero(t, scan.corrected)
}

// TestScanBridgeService_StopsAtFirstKnownFinalBridge verifies pagination stops as soon as an
// already-known FINAL bridge is reached, without walking further pages, and returns only the
// bridges found before it (the newer ones, per the server's newest-first order).
func TestScanBridgeService_StopsAtFirstKnownFinalBridge(t *testing.T) {
	// bridgesByNetwork is given newest-first (global index 3, then 2, then 1), matching the real
	// bridge service's own deposit_count DESC order
	known2 := bridgeResponse(1, 2, 1, testFromAddress, 2)
	svc := &fakeActivityBridgeService{
		bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
			1: {
				bridgeResponse(1, 2, 2, testFromAddress, 3),
				known2, // already known and final: pagination stops here
				bridgeResponse(1, 2, 0, testFromAddress, 1), // must never be fetched
			},
		},
	}
	url := svc.start(t)
	lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
	source := mustNewActivitySource(t, lister, nil, testLogger)
	client, err := source.services.aggkitBridgeClientFor(1)
	require.NoError(t, err)

	known := map[string]domain.KnownBridge{"2": knownBridge(known2, domain.ActivitySourceBridgeService, true)}
	scan, err := scanBridgeService(t.Context(), client, 1, testFromAddress, 1, known, testLogger)
	require.NoError(t, err)
	require.Len(t, scan.bridges, 1)
	require.Equal(t, int64(3), scan.bridges[0].Bridge.GlobalIndex.ToBigInt().Int64())
	require.Equal(t, map[int]int{1: 2, 2: 1}, svc.pageRequests(), "page 3 is never read; page 1 is re-read")
}

// TestScanBridgeService_UpgradesSourceOnceBridgeServiceCatchesUp verifies a bridge known only
// from the RPC fallback (Source: domain.ActivitySourceRPC — the bridge service had not indexed
// it yet) is re-emitted, relabeled domain.ActivitySourceBridgeService, once the bridge service
// itself reports it — so the cache is upgraded to reflect it is now the source of record — while
// pagination still stops at the first known final bridge below it.
func TestScanBridgeService_UpgradesSourceOnceBridgeServiceCatchesUp(t *testing.T) {
	finalBridge := bridgeResponse(1, 2, 0, testFromAddress, 1)
	svc := &fakeActivityBridgeService{
		bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
			1: {
				bridgeResponse(1, 2, 1, testFromAddress, 2), // known, but only via the RPC fallback so far
				finalBridge, // known and final: the scan stops here
			},
		},
	}
	url := svc.start(t)
	lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
	source := mustNewActivitySource(t, lister, nil, testLogger)
	client, err := source.services.aggkitBridgeClientFor(1)
	require.NoError(t, err)

	known := map[string]domain.KnownBridge{
		"2": {Source: domain.ActivitySourceRPC, NetworkID: 1, DepositCount: 1, TxHash: "0x2"},
		"1": knownBridge(finalBridge, domain.ActivitySourceBridgeService, true),
	}
	scan, err := scanBridgeService(t.Context(), client, 1, testFromAddress, 10, known, testLogger)
	require.NoError(t, err)
	require.Len(t, scan.bridges, 1, "the older, final bridge (index 1) is not returned again")
	require.Equal(t, int64(2), scan.bridges[0].Bridge.GlobalIndex.ToBigInt().Int64())
	require.Equal(t, domain.ActivitySourceBridgeService, scan.bridges[0].Source)
	require.Empty(t, scan.removed)
}

// TestInvalidatedBridges verifies invalidatedBridges reports exactly the known entries that:
// belong to the network being checked, were last cached from the RPC fallback, have a BlockNum
// still inside the just-scanned window, and are absent from foundThisCall — leaving alone any
// entry from a different network, sourced from the bridge service (presumed reorg-safe already),
// or that has simply aged out of the window (BlockNum below the window's own FromBlock, which
// just means it is no longer being watched, not that anything is wrong).
func TestInvalidatedBridges(t *testing.T) {
	known := map[string]domain.KnownBridge{
		"1": {NetworkID: 1, Source: domain.ActivitySourceRPC, BlockNum: 150},           // in window, missing -> invalidated
		"2": {NetworkID: 1, Source: domain.ActivitySourceRPC, BlockNum: 150},           // in window, found -> kept
		"3": {NetworkID: 1, Source: domain.ActivitySourceRPC, BlockNum: 50},            // aged out of window -> kept
		"4": {NetworkID: 1, Source: domain.ActivitySourceBridgeService, BlockNum: 150}, // bridge-sourced -> never invalidated
		"5": {NetworkID: 2, Source: domain.ActivitySourceRPC, BlockNum: 150},           // different network -> kept
	}
	foundThisCall := map[string]struct{}{"2": {}}

	invalidated := invalidatedBridges(1, known, 100, foundThisCall)
	require.ElementsMatch(t, []string{"1"}, invalidated)
}

// TestIsNetworkSynced verifies isNetworkSynced reads only its own side of the sync-status pair
// for a given network, so an error on the other side never leaks into it: network 0 (mainnet)
// reads L1Info and network N reads L2Info independently. An erroring entry keeps IsSynced false
// (see bridgeservice.computeSyncStatus), so isNetworkSynced still reports it as not synced.
func TestIsNetworkSynced(t *testing.T) {
	status := &bridgeservicetypes.SyncStatus{
		L1Info: &bridgeservicetypes.NetworkSyncInfo{IsSynced: true},
		L2Info: &bridgeservicetypes.NetworkSyncInfo{IsSynced: false, Error: "boom"},
	}
	require.True(t, isNetworkSynced(MainnetNetworkID, status),
		"network 0 (mainnet) reads L1Info, unaffected by L2Info's error")
	require.False(t, isNetworkSynced(5, status), "network 5 reads L2Info, which errored and so is not synced")

	require.False(t, isNetworkSynced(MainnetNetworkID, nil), "a nil status is conservatively not synced")
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
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx", GlobalIndex: "1", DestinationNetwork: 2}
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
// (nil if the network is absent) or err, never both. fromBlock is the window's own FromBlock this
// stub reports (see activityRPCScanResult) — relevant only to invalidation tests, defaults to 0.
type stubRPCScanner struct {
	bridges   map[uint32][]*domain.ScannedBridge
	err       error
	fromBlock uint64
}

func (s stubRPCScanner) BridgesFrom(
	_ context.Context, networkID uint32, _ common.Address,
) (activityRPCScanResult, error) {
	if s.err != nil {
		return activityRPCScanResult{}, s.err
	}
	return activityRPCScanResult{Bridges: s.bridges[networkID], FromBlock: s.fromBlock}, nil
}

// activitySourceWithRPC builds an ActivitySource exactly like mustNewActivitySource, except its
// rpc field is rpc instead of nil — used to exercise BridgesFrom's REST/RPC merge logic (see
// scanNetwork) without a full RPC mock (activity_rpc_test.go already covers *activityRPCScanner's
// own scanning logic directly).
func activitySourceWithRPC(finder NetworkLister, rpc activityBridgeRPCScanner) *ActivitySource {
	return &ActivitySource{
		logger:                testLogger,
		services:              newBridgeServiceClients(finder, 0),
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
				// duplicate of the REST result: found by both in the very same call
				{Bridge: bridgeResponse(1, 2, 0, testFromAddress, 1), NetworkID: 1, Source: domain.ActivitySourceRPC},
				{Bridge: bridgeResponse(1, 2, 1, testFromAddress, 2), NetworkID: 1, Source: domain.ActivitySourceRPC}, // new
			},
		}}
		source := activitySourceWithRPC(lister, rpc)

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Empty(t, warnings)
		sourceByGlobalIndex := make(map[int64]domain.ActivitySourceKind, len(items))
		for _, item := range items {
			sourceByGlobalIndex[item.Bridge.GlobalIndex.ToBigInt().Int64()] = item.Source
		}
		require.Equal(t, map[int64]domain.ActivitySourceKind{
			1: domain.ActivitySourceBridgeService, // both reported it: the bridge service wins
			2: domain.ActivitySourceRPC,           // only the RPC fallback reported it
		}, sourceByGlobalIndex)
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
			1: {{Bridge: bridgeResponse(1, 2, 1, testFromAddress, 2), NetworkID: 1, Source: domain.ActivitySourceRPC}},
		}}
		source := activitySourceWithRPC(lister, rpc)

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
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
			// duplicate only
			1: {{Bridge: bridgeResponse(1, 2, 0, testFromAddress, 1), NetworkID: 1, Source: domain.ActivitySourceRPC}},
		}}
		source := activitySourceWithRPC(lister, rpc)

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Empty(t, warnings)
	})

	t.Run("bridge service catches up on a bridge only known via the RPC fallback -> Source upgrades to bridge", func(t *testing.T) {
		svc := &fakeActivityBridgeService{
			bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
				1: {bridgeResponse(1, 2, 0, testFromAddress, 1)}, // now indexed by the bridge service too
			},
			syncStatus: &bridgeservicetypes.SyncStatus{L2Info: &bridgeservicetypes.NetworkSyncInfo{IsSynced: true}},
		}
		url := svc.start(t)
		lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
		source := activitySourceWithRPC(lister, stubRPCScanner{}) // RPC finds nothing new this time
		known := map[string]domain.KnownBridge{"1": {Source: domain.ActivitySourceRPC}}

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), known)
		require.NoError(t, err)
		require.Empty(t, warnings)
		require.Len(t, items, 1)
		require.Equal(t, domain.ActivitySourceBridgeService, items[0].Source)
	})

	t.Run("REST ok (unrelated network), RPC bridge already known but at a different block (reorg) -> re-surfaced", func(t *testing.T) {
		svc := &fakeActivityBridgeService{
			bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
				1: {bridgeResponse(1, 2, 0, testFromAddress, 1)},
			},
			syncStatus: &bridgeservicetypes.SyncStatus{L2Info: &bridgeservicetypes.NetworkSyncInfo{IsSynced: true}},
		}
		url := svc.start(t)
		lister := fakeNetworkLister{networkIDs: []uint32{1}, url: url}
		moved := bridgeResponse(1, 2, 1, testFromAddress, 2)
		moved.BlockNum = 200
		rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
			1: {{Bridge: moved, NetworkID: 1, Source: domain.ActivitySourceRPC}},
		}}
		source := activitySourceWithRPC(lister, rpc)
		// caller's cache has global index 2 (not reported by REST this call) at its original,
		// now reorged-out block
		known := map[string]domain.KnownBridge{"2": {TxHash: moved.TxHash, BlockNum: 100}}

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), known)
		require.NoError(t, err)
		require.Len(t, items, 2) // the REST bridge (index 1) plus the reconciled, moved one (index 2)
		var reconciled *domain.ScannedBridge
		for _, item := range items {
			if item.Bridge.GlobalIndex.ToBigInt().Int64() == 2 {
				reconciled = item
			}
		}
		require.NotNil(t, reconciled, "the moved bridge must be re-surfaced so ActivityCache overwrites the stale cached BlockNum")
		require.Equal(t, uint64(200), reconciled.Bridge.BlockNum)
		require.Equal(t, domain.ActivitySourceRPC, reconciled.Source)
		require.Empty(t, warnings, "a fully synced bridge service absorbs the reconciled bridge silently")
	})

	t.Run("REST unreachable, RPC ok -> RPC-only results, warns that historical activity may be unavailable", func(t *testing.T) {
		// networkID 2 resolves to an empty bridge service URL, which aggkitBridgeClientFor rejects
		lister := fakeMixedNetworkLister{networkIDs: []uint32{2}, urls: map[uint32]string{}}
		rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
			2: {{Bridge: bridgeResponse(2, 1, 0, testFromAddress, 3), NetworkID: 2, Source: domain.ActivitySourceRPC}},
		}}
		source := activitySourceWithRPC(lister, rpc)

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, int64(3), items[0].Bridge.GlobalIndex.ToBigInt().Int64())
		require.Equal(t, domain.ActivitySourceRPC, items[0].Source)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0].Message, "historical activity may not be available")
	})

	t.Run("REST unreachable, RPC ok, RPC bridge already in known at the same location -> filtered out, not re-added", func(t *testing.T) {
		lister := fakeMixedNetworkLister{networkIDs: []uint32{2}, urls: map[uint32]string{}}
		unchanged := bridgeResponse(2, 1, 0, testFromAddress, 3)
		rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
			2: {
				// already known, unchanged
				{Bridge: unchanged, NetworkID: 2, Source: domain.ActivitySourceRPC},
				{Bridge: bridgeResponse(2, 1, 1, testFromAddress, 4), NetworkID: 2, Source: domain.ActivitySourceRPC}, // new
			},
		}}
		source := activitySourceWithRPC(lister, rpc)
		// caller's cache has global index 3 at exactly the same TxHash/BlockNum the RPC scan
		// still reports it at
		known := map[string]domain.KnownBridge{
			"3": {TxHash: unchanged.TxHash, BlockNum: unchanged.BlockNum},
		}

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), known)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, int64(4), items[0].Bridge.GlobalIndex.ToBigInt().Int64())
		require.Len(t, warnings, 1)
	})

	t.Run("REST unreachable, RPC ok, RPC bridge already known but at a different block (reorg) -> re-surfaced so the cache is corrected", func(t *testing.T) {
		lister := fakeMixedNetworkLister{networkIDs: []uint32{2}, urls: map[uint32]string{}}
		moved := bridgeResponse(2, 1, 0, testFromAddress, 3)
		moved.BlockNum = 200 // the RPC scan now finds global index 3 re-included at a later block
		rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
			2: {{Bridge: moved, NetworkID: 2, Source: domain.ActivitySourceRPC}},
		}}
		source := activitySourceWithRPC(lister, rpc)
		// caller's cache still has global index 3 at its original (now reorged-out) block
		known := map[string]domain.KnownBridge{
			"3": {TxHash: moved.TxHash, BlockNum: 100},
		}

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), known)
		require.NoError(t, err)
		require.Len(t, items, 1, "the moved bridge must be re-surfaced so ActivityCache overwrites the stale cached BlockNum")
		require.Equal(t, uint64(200), items[0].Bridge.BlockNum)
		require.Len(t, warnings, 1)
	})

	t.Run("REST unreachable, RPC also fails -> unchanged legacy behavior: skipped, warns about the REST error", func(t *testing.T) {
		lister := fakeMixedNetworkLister{networkIDs: []uint32{2}, urls: map[uint32]string{}}
		rpc := stubRPCScanner{err: errors.New("rpc down")}
		source := activitySourceWithRPC(lister, rpc)

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
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

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Len(t, items, 1) // other's bridge was already filtered server-side by from_address
		require.Empty(t, warnings)
	})

	t.Run("RPC bridge previously known within the scanned window has disappeared -> reported invalidated", func(t *testing.T) {
		// networkID 2 resolves to an empty bridge service URL; REST being unreachable is
		// irrelevant here, invalidation only depends on the RPC scan's own result
		lister := fakeMixedNetworkLister{networkIDs: []uint32{2}, urls: map[uint32]string{}}
		rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{}, fromBlock: 100} // scans from 100, finds nothing
		source := activitySourceWithRPC(lister, rpc)
		known := map[string]domain.KnownBridge{
			// still within [100, ...) but no longer found there: reorged out, nothing has
			// re-included it (yet)
			"9": {NetworkID: 2, Source: domain.ActivitySourceRPC, BlockNum: 150},
		}

		items, invalidated, _, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), known)
		require.NoError(t, err)
		require.Empty(t, items)
		require.Equal(t, []string{"9"}, invalidated)
	})

	t.Run("RPC bridge previously known has aged out of the scanned window -> not invalidated", func(t *testing.T) {
		lister := fakeMixedNetworkLister{networkIDs: []uint32{2}, urls: map[uint32]string{}}
		rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{}, fromBlock: 200} // window moved past block 150
		source := activitySourceWithRPC(lister, rpc)
		known := map[string]domain.KnownBridge{
			"9": {NetworkID: 2, Source: domain.ActivitySourceRPC, BlockNum: 150}, // below the window's FromBlock now
		}

		_, invalidated, _, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), known)
		require.NoError(t, err)
		require.Empty(t, invalidated, "aging out of the window is expected, not a sign the bridge was reorged away")
	})
}

// rawBridgeService serves GET /bridge/v1/bridges with raw bodies, so a test controls exactly what
// is decoded: bridgePages[page] holds the bodies answered for that page_number, one per request
// in order, the last one repeating. GET /bridge/v1/claims answers claims, as is
func rawBridgeService(t *testing.T, bridgePages map[int][]string, claims string) string {
	t.Helper()

	var mu sync.Mutex
	requests := make(map[int]int)
	mux := http.NewServeMux()
	mux.HandleFunc("/bridge/v1/bridges", func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.URL.Query().Get("page_number"))
		require.NoError(t, err)
		mu.Lock()
		bodies := bridgePages[page]
		require.NotEmpty(t, bodies, "unexpected request of page %d", page)
		body := bodies[min(requests[page], len(bodies)-1)]
		requests[page]++
		mu.Unlock()
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/bridge/v1/claims", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(claims))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

// bridgesBody is the bridges endpoint response listing bridges (a nil entry is a JSON null),
// reporting count of them
func bridgesBody(t *testing.T, count int, bridges ...*bridgeservicetypes.BridgeResponse) string {
	t.Helper()
	body, err := json.Marshal(bridgeservicetypes.BridgesResult{Bridges: bridges, Count: count})
	require.NoError(t, err)
	return string(body)
}

// TestScanBridgeService_ValidatesTheResponse feeds scanBridgeService, through the real client,
// bridge listings with a null entry: the scan fails as a whole, whatever the position of the entry
// (after a valid one, on a later page, on the page 1 read again at the end of the pass), and
// returns nothing of the pages it read before
func TestScanBridgeService_ValidatesTheResponse(t *testing.T) {
	const networkID = uint32(1)
	b3 := bridgeResponse(networkID, 2, 2, testFromAddress, 3)
	b2 := bridgeResponse(networkID, 2, 1, testFromAddress, 2)
	b1 := bridgeResponse(networkID, 2, 0, testFromAddress, 1)

	tests := []struct {
		name     string
		pageSize uint32
		pages    map[int][]string
		// wantContext is what the error says about where the null entry is
		wantContext string
	}{
		{
			name:        "null entry alone",
			pageSize:    10,
			pages:       map[int][]string{1: {`{"bridges":[null],"count":1}`}},
			wantContext: "network 1 page 1",
		},
		{
			name:        "null entry after a valid one",
			pageSize:    10,
			pages:       map[int][]string{1: {bridgesBody(t, 2, b2, nil)}},
			wantContext: "bridges[1]",
		},
		{
			name:        "null entry before a valid one",
			pageSize:    10,
			pages:       map[int][]string{1: {bridgesBody(t, 2, nil, b2)}},
			wantContext: "bridges[0]",
		},
		{
			name:     "null entry on page 2",
			pageSize: 2,
			pages: map[int][]string{
				1: {bridgesBody(t, 3, b3, b2)},
				2: {bridgesBody(t, 3, nil)},
			},
			wantContext: "network 1 page 2",
		},
		{
			name:     "null entry on page 1 read again at the end of the pass",
			pageSize: 2,
			pages: map[int][]string{
				1: {bridgesBody(t, 3, b3, b2), bridgesBody(t, 3, nil, b2)},
				2: {bridgesBody(t, 3, b1)},
			},
			wantContext: "network 1 page 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := rawBridgeService(t, tt.pages, "")
			source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{networkID}, url: url}, nil, testLogger)
			svc, err := source.services.aggkitBridgeClientFor(networkID)
			require.NoError(t, err)

			scan, err := scanBridgeService(t.Context(), svc, networkID, testFromAddress, tt.pageSize, nil, testLogger)
			requireInvalidResponse(t, err)
			require.ErrorContains(t, err, tt.wantContext)
			require.Empty(t, scan.bridges)
			require.Empty(t, scan.listed)
			require.Empty(t, scan.removed)
		})
	}

	t.Run("valid listing", func(t *testing.T) {
		url := rawBridgeService(t, map[int][]string{
			1: {bridgesBody(t, 3, b3, b2)},
			2: {bridgesBody(t, 3, b1)},
		}, "")
		source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{networkID}, url: url}, nil, testLogger)
		svc, err := source.services.aggkitBridgeClientFor(networkID)
		require.NoError(t, err)

		scan, err := scanBridgeService(t.Context(), svc, networkID, testFromAddress, 2, nil, testLogger)
		require.NoError(t, err)
		require.Len(t, scan.bridges, 3)
		require.Equal(t, b3, scan.bridges[0].Bridge)
		require.Equal(t, b2, scan.bridges[1].Bridge)
		require.Equal(t, b1, scan.bridges[2].Bridge)
	})
}

// TestCheckBridgesPage verifies a page with a null entry is rejected, with where the entry is
func TestCheckBridgesPage(t *testing.T) {
	valid := bridgeResponse(3, 1, 0, testFromAddress, 1)

	require.NoError(t, checkBridgesPage(3, 2, nil))
	require.NoError(t, checkBridgesPage(3, 2, []*bridgeservicetypes.BridgeResponse{valid, valid}))

	err := checkBridgesPage(3, 2, []*bridgeservicetypes.BridgeResponse{valid, nil})
	requireInvalidResponse(t, err)
	require.ErrorContains(t, err, "network 3 page 2 entry 1")
}

// TestActivitySource_BridgesFrom_MalformedNetworkDoesNotAffectHealthyOne verifies a network whose
// bridge service answers a listing with a null entry degrades alone: its error is reported as a
// warning, what is cached of it is kept (nothing is invalidated), and the healthy network's
// bridges are returned
func TestActivitySource_BridgesFrom_MalformedNetworkDoesNotAffectHealthyOne(t *testing.T) {
	malformedURL := rawBridgeService(t, map[int][]string{1: {`{"bridges":[null],"count":1}`}}, "")
	healthy := &fakeActivityBridgeService{
		bridgesByNetwork: map[uint32][]*bridgeservicetypes.BridgeResponse{
			2: {bridgeResponse(2, 1, 0, testFromAddress, 5)},
		},
	}
	lister := fakeMixedNetworkLister{
		networkIDs: []uint32{1, 2},
		urls:       map[uint32]string{1: malformedURL, 2: healthy.start(t)},
	}
	source := mustNewActivitySource(t, lister, nil, testLogger)

	cached := bridgeResponse(1, 2, 0, testFromAddress, 1)
	known := map[string]domain.KnownBridge{
		string(cached.GlobalIndex): knownBridge(cached, domain.ActivitySourceBridgeService, false),
	}

	items, invalidated, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), known)
	require.NoError(t, err)

	require.Len(t, items, 1, "the healthy network's bridge is returned")
	require.Equal(t, uint32(2), items[0].NetworkID)
	require.Equal(t, int64(5), items[0].Bridge.GlobalIndex.ToBigInt().Int64())
	require.Empty(t, invalidated, "what is cached of the malformed network is kept")

	require.Len(t, warnings, 1)
	require.Equal(t, uint32(1), warnings[0].NetworkID)
	require.Contains(t, warnings[0].Message, "network 1")
	require.Contains(t, warnings[0].Message, client.ErrInvalidResponse.Error())
}

// TestActivitySource_ClaimInfo_ValidatesTheResponse feeds ClaimInfo, through the real client, the
// claims responses a bridge service can answer with
func TestActivitySource_ClaimInfo_ValidatesTheResponse(t *testing.T) {
	const destination = uint32(2)
	// the bridge of deposit count 7 sent from mainnet, as the bridge service lists it
	bridge := &domain.ScannedBridge{
		Bridge:    bridgeResponse(0, destination, 7, testFromAddress, 0),
		NetworkID: 0,
	}
	bridge.Bridge.GlobalIndex = mainnetBridgeGlobalIndex
	right := claimJSON(quoted(mainnetBridgeGlobalIndex), destination, "0x0d", 50)
	other := claimJSON(quoted("999"), destination, "0x0e", 60)

	tests := []struct {
		name        string
		body        string
		wantTx      string
		wantInvalid bool
	}{
		{name: "valid claim", body: claimsBody(1, right), wantTx: "0x0d"},
		{name: "no claim", body: claimsBody(0)},
		{name: "empty list with count 1", body: claimsBody(1), wantInvalid: true},
		{name: "null claim", body: claimsBody(1, "null"), wantInvalid: true},
		{name: "null claim after a valid one", body: claimsBody(2, right, "null"), wantInvalid: true},
		{name: "global index of another claim", body: claimsBody(1, other), wantInvalid: true},
		{name: "requested claim is not the first", body: claimsBody(2, other, right), wantTx: "0x0d"},
		{
			name:        "right global index on another destination network",
			body:        claimsBody(1, claimJSON(quoted(mainnetBridgeGlobalIndex), 5, "0x0d", 50)),
			wantInvalid: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := rawBridgeService(t, nil, tt.body)
			source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{destination}, url: url}, nil, testLogger)

			claim, err := source.ClaimInfo(t.Context(), bridge)

			switch {
			case tt.wantInvalid:
				requireInvalidResponse(t, err)
				require.Nil(t, claim)
			case tt.wantTx == "":
				require.NoError(t, err)
				require.Nil(t, claim)
			default:
				require.NoError(t, err)
				require.NotNil(t, claim)
				require.Equal(t, bridgeservicetypes.Hash(tt.wantTx), claim.TxHash)
			}
		})
	}

	t.Run("legacy zkEVM claim identified by its bare deposit count", func(t *testing.T) {
		legacy := &domain.ScannedBridge{Bridge: bridgeResponse(0, 1, 7, testFromAddress, 0), NetworkID: 0}
		legacy.Bridge.GlobalIndex = mainnetBridgeGlobalIndex
		url := rawBridgeService(t, nil, claimsBody(1, claimJSON(quoted("7"), 1, "0x0d", 50)))
		source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{1}, url: url}, nil, testLogger)

		claim, err := source.ClaimInfo(t.Context(), legacy)
		require.NoError(t, err)
		require.NotNil(t, claim)
		require.Equal(t, bridgeservicetypes.Hash("0x0d"), claim.TxHash)

		// the same claim is not the one of a bridge to any other network
		notLegacy := &domain.ScannedBridge{Bridge: bridgeResponse(0, 2, 7, testFromAddress, 0), NetworkID: 0}
		notLegacy.Bridge.GlobalIndex = mainnetBridgeGlobalIndex
		url = rawBridgeService(t, nil, claimsBody(1, claimJSON(quoted("7"), 2, "0x0d", 50)))
		source = mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{2}, url: url}, nil, testLogger)

		claim, err = source.ClaimInfo(t.Context(), notLegacy)
		requireInvalidResponse(t, err)
		require.Nil(t, claim)
	})

	for _, globalIndex := range []bridgeservicetypes.BigIntString{"garbage", "-1"} {
		t.Run(fmt.Sprintf("a bridge with the invalid global index %q has no claim to look for", globalIndex),
			func(t *testing.T) {
				invalid := &domain.ScannedBridge{Bridge: bridgeResponse(0, destination, 7, testFromAddress, 0), NetworkID: 0}
				invalid.Bridge.GlobalIndex = globalIndex
				url := rawBridgeService(t, nil, claimsBody(1, claimJSON(quoted("0"), destination, "0x0d", 50)))
				source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{destination}, url: url}, nil, testLogger)

				claim, err := source.ClaimInfo(t.Context(), invalid)
				require.ErrorContains(t, err, "invalid global index")
				require.Nil(t, claim)
			})
	}
}
