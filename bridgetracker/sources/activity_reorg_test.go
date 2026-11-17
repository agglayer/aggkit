package sources

import (
	"net/http"
	"testing"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/metrics"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/ethereum/go-ethereum/common"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// reorgScanFixture is one network (1) whose bridge service lists, newest first, a non-final
// head (global indexes 5, 4, 3) on top of a final tail (2, 1), all sent by testFromAddress
type reorgScanFixture struct {
	svc     *fakeActivityBridgeService
	source  *ActivitySource
	bridges map[int64]*bridgeservicetypes.BridgeResponse
	known   map[string]domain.KnownBridge
}

func newReorgScanFixture(t *testing.T, pageSize uint32) *reorgScanFixture {
	t.Helper()
	f := &reorgScanFixture{bridges: make(map[int64]*bridgeservicetypes.BridgeResponse)}
	var listing []*bridgeservicetypes.BridgeResponse
	for gi := int64(5); gi >= 1; gi-- {
		b := bridgeResponse(1, 2, uint32(gi-1), testFromAddress, gi)
		b.BlockNum = uint64(100 * gi)
		b.BlockTimestamp = uint64(1_700_000_000 + gi)
		f.bridges[gi] = b
		listing = append(listing, b)
	}
	f.svc = &fakeActivityBridgeService{}
	f.svc.setBridges(1, listing...)
	url := f.svc.start(t)
	f.source = mustNewActivitySource(t, fakeNetworkLister{networkIDs: []uint32{1}, url: url}, nil, testLogger)
	f.source.pageSize = pageSize
	f.known = map[string]domain.KnownBridge{
		"5": knownBridge(f.bridges[5], domain.ActivitySourceBridgeService, false),
		"4": knownBridge(f.bridges[4], domain.ActivitySourceBridgeService, false),
		"3": knownBridge(f.bridges[3], domain.ActivitySourceBridgeService, false),
		"2": knownBridge(f.bridges[2], domain.ActivitySourceBridgeService, true),
		"1": knownBridge(f.bridges[1], domain.ActivitySourceBridgeService, true),
	}
	return f
}

func (f *reorgScanFixture) scan(t *testing.T) ([]*domain.ScannedBridge, []string, []domain.ActivityWarning) {
	t.Helper()
	items, invalidated, warnings, err := f.source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), f.known)
	require.NoError(t, err)
	return items, invalidated, warnings
}

func byGlobalIndex(items []*domain.ScannedBridge) map[int64]*domain.ScannedBridge {
	out := make(map[int64]*domain.ScannedBridge, len(items))
	for _, item := range items {
		out[item.Bridge.GlobalIndex.ToBigInt().Int64()] = item
	}
	return out
}

func moved(b *bridgeservicetypes.BridgeResponse, blockNum uint64, txHash string) *bridgeservicetypes.BridgeResponse {
	c := *b
	c.BlockNum = blockNum
	c.BlockTimestamp = b.BlockTimestamp + 12
	c.TxHash = bridgeservicetypes.Hash(txHash)
	return &c
}

// TestBridgesFrom_RereadsNonFinalHeadAndStopsAtFinal verifies every non-final known bridge is
// returned again with the data served now (even unchanged), the final ones are not, and the scan
// never reads past the first known final bridge
func TestBridgesFrom_RereadsNonFinalHeadAndStopsAtFinal(t *testing.T) {
	f := newReorgScanFixture(t, 1)

	items, invalidated, warnings := f.scan(t)
	require.Empty(t, warnings)
	require.Empty(t, invalidated)
	require.ElementsMatch(t, []int64{5, 4, 3}, keysOf(byGlobalIndex(items)))
	// pages 1..4 (5, 4, 3, then 2: known final, stop), page 5 (global index 1) never read; page 1
	// read again at the end of the multi-page scan
	require.Equal(t, map[int]int{1: 2, 2: 1, 3: 1, 4: 1}, f.svc.pageRequests())
}

// TestBridgesFrom_FinalBridgeNeverReread verifies an address whose known bridges are all final
// costs exactly one page read: the first bridge listed is known and final
func TestBridgesFrom_FinalBridgeNeverReread(t *testing.T) {
	f := newReorgScanFixture(t, 2)
	for _, key := range []string{"5", "4", "3"} {
		k := f.known[key]
		k.Final = true
		f.known[key] = k
	}

	items, invalidated, warnings := f.scan(t)
	require.Empty(t, items)
	require.Empty(t, invalidated)
	require.Empty(t, warnings)
	require.Equal(t, map[int]int{1: 1}, f.svc.pageRequests())
}

// TestBridgesFrom_KeepsReadingPastFinalWhileANonFinalIsBelow verifies the scan does not stop at
// a known final bridge while a known non-final one (e.g. one whose finality could not be resolved
// yet) sits further down the listing: it must still be re-read
func TestBridgesFrom_KeepsReadingPastFinalWhileANonFinalIsBelow(t *testing.T) {
	f := newReorgScanFixture(t, 1)
	k := f.known["2"]
	k.Final = false
	f.known["2"] = k
	k = f.known["4"]
	k.Final = true
	f.known["4"] = k

	items, invalidated, _ := f.scan(t)
	require.Empty(t, invalidated)
	require.ElementsMatch(t, []int64{5, 3, 2}, keysOf(byGlobalIndex(items)))
	require.Equal(t, map[int]int{1: 2, 2: 1, 3: 1, 4: 1, 5: 1}, f.svc.pageRequests(),
		"read down to global index 1 (final, nothing pending below), never further")
}

// TestBridgesFrom_BridgeRemoved verifies a non-final bridge the listing no longer has, although
// the scan went past its position, is invalidated (and only that one)
func TestBridgesFrom_BridgeRemoved(t *testing.T) {
	f := newReorgScanFixture(t, 10)
	f.svc.setBridges(1, f.bridges[5], f.bridges[3], f.bridges[2], f.bridges[1])

	items, invalidated, warnings := f.scan(t)
	require.Empty(t, warnings)
	require.Equal(t, []string{"4"}, invalidated)
	require.ElementsMatch(t, []int64{5, 3}, keysOf(byGlobalIndex(items)))
}

// TestBridgesFrom_NewestBridgeRemoved verifies the newest bridge of the address being reorged out
// (nothing above it any more) is invalidated too
func TestBridgesFrom_NewestBridgeRemoved(t *testing.T) {
	f := newReorgScanFixture(t, 10)
	f.svc.setBridges(1, f.bridges[4], f.bridges[3], f.bridges[2], f.bridges[1])

	_, invalidated, _ := f.scan(t)
	require.Equal(t, []string{"5"}, invalidated)
}

// TestBridgesFrom_BridgeReincludedInAnotherBlock verifies a non-final bridge re-included at a
// different block/tx is returned once, with the new location, and not invalidated
func TestBridgesFrom_BridgeReincludedInAnotherBlock(t *testing.T) {
	f := newReorgScanFixture(t, 10)
	reincluded := moved(f.bridges[4], 450, "0x4b")
	f.svc.setBridges(1, f.bridges[5], reincluded, f.bridges[3], f.bridges[2], f.bridges[1])

	items, invalidated, _ := f.scan(t)
	require.Empty(t, invalidated)
	got := byGlobalIndex(items)
	require.Len(t, items, 3, "no duplicate: the correction shares the global index")
	require.Equal(t, uint64(450), got[4].Bridge.BlockNum)
	require.Equal(t, bridgeservicetypes.Hash("0x4b"), got[4].Bridge.TxHash)
}

// TestBridgesFrom_BridgeReplacedBySameDepositCount verifies a different bridge of the same sender
// taking a reorged-out bridge's deposit count (so its global index) is returned as a correction
// of that entry, and that a deposit count taken by ANOTHER sender's bridge invalidates the entry
func TestBridgesFrom_BridgeReplacedBySameDepositCount(t *testing.T) {
	t.Run("same sender", func(t *testing.T) {
		f := newReorgScanFixture(t, 10)
		replacement := moved(f.bridges[5], 510, "0x5b")
		replacement.Amount = "42"
		f.svc.setBridges(1, replacement, f.bridges[4], f.bridges[3], f.bridges[2], f.bridges[1])

		items, invalidated, _ := f.scan(t)
		require.Empty(t, invalidated)
		got := byGlobalIndex(items)
		require.Len(t, items, 3)
		require.Equal(t, bridgeservicetypes.Hash("0x5b"), got[5].Bridge.TxHash)
		require.Equal(t, bridgeservicetypes.BigIntString("42"), got[5].Bridge.Amount)
	})

	t.Run("another sender", func(t *testing.T) {
		f := newReorgScanFixture(t, 10)
		other := moved(f.bridges[5], 510, "0x5c")
		otherAddr := bridgeservicetypes.Address("0x2222222222222222222222222222222222222222")
		other.FromAddress = &otherAddr
		f.svc.setBridges(1, other, f.bridges[4], f.bridges[3], f.bridges[2], f.bridges[1])

		_, invalidated, _ := f.scan(t)
		require.Equal(t, []string{"5"}, invalidated)
	})
}

// TestBridgesFrom_ServiceUnavailableKeepsState verifies a 503 from the bridge service (it is
// resolving a reorg) is never read as the bridges being gone: nothing returned, nothing
// invalidated, a warning saying the last known activity is kept
func TestBridgesFrom_ServiceUnavailableKeepsState(t *testing.T) {
	f := newReorgScanFixture(t, 10)
	f.svc.setBridgesStatus(http.StatusServiceUnavailable)

	items, invalidated, warnings := f.scan(t)
	require.Empty(t, items)
	require.Empty(t, invalidated)
	require.Len(t, warnings, 1)
	require.Equal(t, uint32(1), warnings[0].NetworkID)
	require.Contains(t, warnings[0].Message, "may be resolving a reorg")
	require.Contains(t, warnings[0].Message, "keeping its last known activity")

	t.Run("with the RPC fallback enabled, bridge-service entries are not invalidated by it", func(t *testing.T) {
		rpc := stubRPCScanner{fromBlock: 1, bridges: map[uint32][]*domain.ScannedBridge{
			// the RPC fallback sees global index 5 elsewhere: ignored, the bridge service is the
			// source of record for it
			1: {{Bridge: moved(f.bridges[5], 999, "0x5d"), NetworkID: 1, Source: domain.ActivitySourceRPC}},
		}}
		f.source.rpc = rpc

		items, invalidated, warnings := f.scan(t)
		require.Empty(t, items)
		require.Empty(t, invalidated)
		require.Len(t, warnings, 1)
		require.Contains(t, warnings[0].Message, "may be resolving a reorg")
	})
}

// TestBridgesFrom_LongScanRereadsPageOne verifies a bridge created while a multi-page scan was
// paging (landing on page 1, already read) is picked up by re-reading page 1 at the end
func TestBridgesFrom_LongScanRereadsPageOne(t *testing.T) {
	f := newReorgScanFixture(t, 2)
	f.known = nil // first scan
	newest := bridgeResponse(1, 2, 5, testFromAddress, 6)
	inserted := false
	f.svc.mu.Lock()
	f.svc.beforeBridges = func(page int) {
		if page == 3 && !inserted {
			inserted = true
			f.svc.bridgesByNetwork[1] = append([]*bridgeservicetypes.BridgeResponse{newest}, f.svc.bridgesByNetwork[1]...)
		}
	}
	f.svc.mu.Unlock()

	items, _, _ := f.scan(t)
	require.ElementsMatch(t, []int64{6, 5, 4, 3, 2, 1}, keysOf(byGlobalIndex(items)))
	require.Len(t, items, 6, "the bridge shifted to the next page by the insertion is not duplicated")
}

// TestBridgesFrom_ShiftedListingIsScannedAgain verifies a scan during which the listing changed
// (a head bridge was reorged out while paging, shifting a bridge onto a page already read, which
// re-reading page 1 does not catch) runs once more instead of missing that bridge for good
func TestBridgesFrom_ShiftedListingIsScannedAgain(t *testing.T) {
	f := newReorgScanFixture(t, 1)
	f.known = nil // first scan
	removedOnce := false
	f.svc.mu.Lock()
	f.svc.beforeBridges = func(page int) {
		if page == 3 && !removedOnce {
			removedOnce = true
			f.svc.bridgesByNetwork[1] = f.svc.bridgesByNetwork[1][1:] // global index 5 reorged out
		}
	}
	f.svc.mu.Unlock()

	items, _, _ := f.scan(t)
	require.ElementsMatch(t, []int64{4, 3, 2, 1}, keysOf(byGlobalIndex(items)),
		"global index 3 shifted onto page 2 after it was read, the second pass finds it")
}

// TestBridgesFrom_HeadReplacedWithSameCountIsScannedAgain verifies a reorg that replaces the head
// of the listing while the scan pages, without changing its count, is caught by the page 1
// re-read: with page size 1, [5, 4, 3, 2, 1] becomes [7, 6, 3, 2, 1] after pages 1 and 2 were
// read, so 6 never shows up in that pass; the pass is run again and finds it
func TestBridgesFrom_HeadReplacedWithSameCountIsScannedAgain(t *testing.T) {
	f := newReorgScanFixture(t, 1)
	f.known = nil // first scan
	b7 := bridgeResponse(1, 2, 4, testFromAddress, 7)
	b6 := bridgeResponse(1, 2, 3, testFromAddress, 6)
	b7.BlockNum, b6.BlockNum = 710, 610
	replaced := false
	f.svc.mu.Lock()
	f.svc.beforeBridges = func(page int) {
		if page == 3 && !replaced {
			replaced = true
			f.svc.bridgesByNetwork[1] = append([]*bridgeservicetypes.BridgeResponse{b7, b6},
				f.svc.bridgesByNetwork[1][2:]...)
		}
	}
	f.svc.mu.Unlock()

	items, invalidated, warnings := f.scan(t)
	require.Empty(t, warnings)
	require.Empty(t, invalidated)
	require.ElementsMatch(t, []int64{7, 6, 3, 2, 1}, keysOf(byGlobalIndex(items)),
		"6 was shifted onto a page already read; the second pass finds it and 5, 4 are not returned")
}

// TestBridgesFrom_ListingShiftedInBothPassesKeepsTheCache verifies that when the listing changes
// during both passes, the scan fails like any bridge-service error: nothing of the network is
// returned or invalidated (a partial pass could invalidate a bridge that still exists), a warning is
// reported, and the next refresh, with a stable listing, corrects the cache
func TestBridgesFrom_ListingShiftedInBothPassesKeepsTheCache(t *testing.T) {
	f := newReorgScanFixture(t, 1)
	removals := 0
	f.svc.mu.Lock()
	f.svc.beforeBridges = func(page int) {
		if page == 3 && removals < 2 {
			removals++
			f.svc.bridgesByNetwork[1] = f.svc.bridgesByNetwork[1][1:] // the head bridge is reorged out
		}
	}
	f.svc.mu.Unlock()

	items, invalidated, warnings := f.scan(t)
	require.Equal(t, 2, removals, "the listing changed in both passes")
	require.Empty(t, items)
	require.Empty(t, invalidated, "nothing is invalidated from an inconsistent listing")
	require.Len(t, warnings, 1)
	require.Equal(t, uint32(1), warnings[0].NetworkID)
	require.Contains(t, warnings[0].Message, "kept changing while it was read")

	// stable listing (3, 2, 1): global indexes 5 and 4 are now proven gone
	items, invalidated, warnings = f.scan(t)
	require.Empty(t, warnings)
	require.Equal(t, []string{"4", "5"}, invalidated)
	require.ElementsMatch(t, []int64{3}, keysOf(byGlobalIndex(items)))
}

// TestBridgesFrom_RPCOnlyEntryRemovedOnceIndexedPast verifies an entry only found through the RPC
// fallback is kept while the bridge service has not indexed that far, and invalidated once the
// bridge service lists a later deposit of the same sender without it
func TestBridgesFrom_RPCOnlyEntryRemovedOnceIndexedPast(t *testing.T) {
	f := newReorgScanFixture(t, 10)
	rpcOnly := bridgeResponse(1, 2, 3, testFromAddress, 9) // deposit count 3, between 4 and 5
	f.known = map[string]domain.KnownBridge{
		"9": knownBridge(rpcOnly, domain.ActivitySourceRPC, false),
		"2": knownBridge(f.bridges[2], domain.ActivitySourceBridgeService, true),
	}
	f.svc.setBridges(1, f.bridges[3], f.bridges[2], f.bridges[1]) // indexed up to deposit count 2

	_, invalidated, _ := f.scan(t)
	require.Empty(t, invalidated, "not indexed that far yet: kept")

	f.svc.setBridges(1, f.bridges[5], f.bridges[3], f.bridges[2], f.bridges[1]) // deposit count 4 listed
	_, invalidated, _ = f.scan(t)
	require.Equal(t, []string{"9"}, invalidated)
}

// TestBridgesFrom_ReorgMetrics verifies the activity reorg counters
func TestBridgesFrom_ReorgMetrics(t *testing.T) {
	prometheus.Init()
	metrics.Register(true)
	before := map[string]float64{}
	for _, name := range []string{
		"activity_reorgs_detected_total", "activity_entries_corrected_total", "activity_entries_removed_total",
	} {
		before[name] = counterValue(t, name)
	}

	f := newReorgScanFixture(t, 10)
	f.svc.setBridges(1, moved(f.bridges[5], 510, "0x5b"), f.bridges[3], f.bridges[2], f.bridges[1])
	f.scan(t)
	f.svc.setBridges(1, f.bridges[5], f.bridges[4], f.bridges[3], f.bridges[2], f.bridges[1])
	f.scan(t) // no reorg: nothing counted

	require.InDelta(t, 1, counterValue(t, "activity_reorgs_detected_total")-before["activity_reorgs_detected_total"], 0)
	require.InDelta(t, 1, counterValue(t, "activity_entries_corrected_total")-before["activity_entries_corrected_total"], 0)
	require.InDelta(t, 1, counterValue(t, "activity_entries_removed_total")-before["activity_entries_removed_total"], 0)
}

func counterValue(t *testing.T, name string) float64 {
	t.Helper()
	c, ok := prometheus.Counter(name)
	require.True(t, ok, "counter %s not registered", name)
	var m dto.Metric
	require.NoError(t, c.Write(&m))
	return m.GetCounter().GetValue()
}

func keysOf(m map[int64]*domain.ScannedBridge) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
