package db

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var activityFilters = []types.ActivityFilter{
	types.ActivityFilterAll, types.ActivityFilterClaimed, types.ActivityFilterPending,
	types.ActivityFilterReadyToClaim, types.ActivityFilterError,
}

// legacyActivityOrder is the order the HTTP command used to sort the whole result in before
// paging moved to the store (CreatedAt descending, then global index descending, parsed as a big.Int),
// plus the bridge key for entries tied on both — which that comparator left unspecified
func legacyActivityOrder(a, b *domain.ActivityEntry) int {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		return 1
	}
	gia, ok := new(big.Int).SetString(string(a.Bridge.GlobalIndex), 10) // base 10
	if !ok {
		gia = new(big.Int)
	}
	gib, ok := new(big.Int).SetString(string(b.Bridge.GlobalIndex), 10) // base 10
	if !ok {
		gib = new(big.Int)
	}
	if c := gib.Cmp(gia); c != 0 {
		return c
	}
	return b.Key().Compare(a.Key())
}

// paginationFixture is 1000 rows of one address: CreatedAt tied in groups of 20, global indexes
// of every encoding (small ones that sort differently as text, rollup ones and mainnet ones above
// 2^64), every tracker claim status, two rows tied on both CreatedAt and global index (the legacy
// collision), one row with a status this build does not know (served as error) and one
// stale-schema row (never served nor counted)
type paginationFixture struct {
	store *sqliteActivityStore
	// served is every entry GetActivity can serve, as decoded
	served []*domain.ActivityEntry
}

func newPaginationFixture(t *testing.T) *paginationFixture {
	t.Helper()
	store := newTestSQLiteActivityStore(t, &fakeActivityScanner{}, &fakeActivityClaims{})
	mustRegisterActivity(t, store, testFromAddress, 0)
	addr := testFromAddress.Hex()

	statuses := []types.TrackerClaimStatus{
		types.TrackerClaimStatusPending, types.TrackerClaimStatusReadyToClaim, types.TrackerClaimStatusClaimed,
		types.TrackerClaimStatusClaimedFinalized, types.TrackerClaimStatusError,
	}
	mainnetFlag := new(big.Int).Lsh(big.NewInt(1), 64) // 2^64
	base := time.Unix(1_700_000_000, 0).UTC()
	const n = 1000
	for i := range n {
		var gi *big.Int
		switch i % 3 {
		case 0:
			gi = big.NewInt(int64(i)) // 9 < 10 numerically, not as text
		case 1:
			gi = new(big.Int).Add(mainnetFlag, big.NewInt(int64(i)))
		default:
			gi = big.NewInt(int64(i)<<32 + int64(i)) // rollup encoding
		}
		createdAt := base.Add(time.Duration(i%50) * time.Second) // ties in groups of 20
		if i == n-1 {
			// the legacy collision: same CreatedAt and global index as the previous row, another key
			gi, _ = new(big.Int).SetString(store.mustGlobalIndex(t, n-2), 10) // base 10
			createdAt = base.Add(time.Duration((n-2)%50) * time.Second)
		}
		bridge := &bridgeservicetypes.BridgeResponse{
			GlobalIndex: bridgeservicetypes.BigIntString(gi.String()), DepositCount: uint32(i),
			TxHash: bridgeservicetypes.Hash(fmt.Sprintf("0x%x", i)), DestinationNetwork: 2,
		}
		item := &domain.ScannedBridge{Bridge: bridge, NetworkID: uint32(i % 3)}
		status := statuses[i%len(statuses)]
		entry := &domain.ActivityEntry{
			Bridge: bridge, BridgeNetworkID: item.NetworkID, TrackerClaimStatus: status,
			ClaimStatus: types.ClaimStatusUnclaimed, CreatedAt: createdAt, UpdatedAt: createdAt,
		}
		require.NoError(t, store.saveBridgeRow(addr, item, entry))
	}
	_, err := store.db.Exec(
		"UPDATE activity_bridge SET tracker_claim_status = 'bogus' WHERE deposit_count = 3")
	require.NoError(t, err)
	_, err = store.db.Exec("UPDATE activity_bridge SET schema_version = 0 WHERE deposit_count = 5")
	require.NoError(t, err)

	f := &paginationFixture{store: store}
	cached, err := store.cachedEntries(addr) // every current-schema row, decoded
	require.NoError(t, err)
	for _, entry := range cached {
		f.served = append(f.served, entry)
	}
	require.Len(t, f.served, n-1, "the stale-schema row is not served")
	_, err = store.db.Exec("CREATE TABLE activity_bridge_backup AS SELECT * FROM activity_bridge")
	require.NoError(t, err)
	return f
}

// mustGlobalIndex is the global index stored for deposit count depositCount
func (s *sqliteActivityStore) mustGlobalIndex(t *testing.T, depositCount int) string {
	t.Helper()
	var gi string
	require.NoError(t, s.db.QueryRow("SELECT global_index FROM activity_bridge WHERE deposit_count = ?",
		depositCount).Scan(&gi))
	return gi
}

// expected is the served entries matching filter, in the legacy order
func (f *paginationFixture) expected(filter types.ActivityFilter) []*domain.ActivityEntry {
	var out []*domain.ActivityEntry
	for _, entry := range f.served {
		if bridgetracker.MatchesActivityFilter(entry, filter) {
			out = append(out, entry)
		}
	}
	slices.SortFunc(out, legacyActivityOrder)
	return out
}

// corruptAllBut makes the data of every row but keep undecodable, so GetActivity fails if it
// decodes any other row; restore puts every row's data back
func (f *paginationFixture) corruptAllBut(t *testing.T, keep []*domain.ActivityEntry) {
	t.Helper()
	_, err := f.store.db.Exec("UPDATE activity_bridge SET data = x'00'")
	require.NoError(t, err)
	for _, entry := range keep {
		_, err := f.store.db.Exec(`UPDATE activity_bridge SET data = (SELECT b.data FROM activity_bridge_backup b
			WHERE b.origin_network_id = activity_bridge.origin_network_id AND b.deposit_count = activity_bridge.deposit_count)
			WHERE origin_network_id = ? AND deposit_count = ?`, entry.BridgeNetworkID, entry.Bridge.DepositCount)
		require.NoError(t, err)
	}
}

func (f *paginationFixture) restore(t *testing.T) {
	t.Helper()
	_, err := f.store.db.Exec(`UPDATE activity_bridge SET data = (SELECT b.data FROM activity_bridge_backup b
		WHERE b.origin_network_id = activity_bridge.origin_network_id AND b.deposit_count = activity_bridge.deposit_count)`)
	require.NoError(t, err)
}

func keysOf(entries []*domain.ActivityEntry) []domain.BridgeKey {
	keys := make([]domain.BridgeKey, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, entry.Key())
	}
	return keys
}

// TestSQLiteActivityStorePaginationDecodesOnlyRequestedPage verifies, for every filter, that
// GetActivity serves each page in the legacy order (ties on CreatedAt broken by the numeric
// global index, above 2^64 included) with the real total, an empty page past the end, and that it
// decodes only the page's rows: every other row's data is corrupted while it runs
func TestSQLiteActivityStorePaginationDecodesOnlyRequestedPage(t *testing.T) {
	f := newPaginationFixture(t)
	// control: the instrumentation catches a decoded row
	f.corruptAllBut(t, nil)
	entries, _, _, err := f.store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll,
		domain.ActivityPage{PageNumber: 1, PageSize: 1})
	f.restore(t)
	require.Error(t, err, "decoding a corrupted row fails")
	require.Nil(t, entries)

	for _, filter := range activityFilters {
		t.Run(filter.String(), func(t *testing.T) {
			want := f.expected(filter)
			require.NotEmpty(t, want)
			const pageSize = 10
			lastPage := uint32((len(want) + pageSize - 1) / pageSize)
			for _, page := range []domain.ActivityPage{
				{PageNumber: 1, PageSize: pageSize}, {PageNumber: 2, PageSize: pageSize},
				{PageNumber: lastPage, PageSize: pageSize}, {PageNumber: lastPage + 1, PageSize: pageSize},
				{PageNumber: 1, PageSize: 200}, {PageNumber: 2, PageSize: 7},
			} {
				start := min(page.Offset(), uint64(len(want)))
				end := min(start+uint64(page.PageSize), uint64(len(want)))
				wantPage := want[start:end]

				f.corruptAllBut(t, wantPage)
				entries, total, _, err := f.store.GetActivity(t.Context(), testFromAddress, false, filter, page)
				f.restore(t)
				require.NoError(t, err, "only the page's rows may be decoded (page %+v)", page)
				require.Equal(t, len(want), total)
				require.NotNil(t, entries)
				require.Equal(t, keysOf(wantPage), keysOf(entries), "page %+v", page)
			}
		})
	}
}

// TestSQLiteActivityStoreRevisionDoesNotDecodeRows verifies the revision is read from the stored
// hashes alone: it is unchanged while every row's data is undecodable
func TestSQLiteActivityStoreRevisionDoesNotDecodeRows(t *testing.T) {
	f := newPaginationFixture(t)
	want, err := f.store.GetActivityRevision(t.Context(), testFromAddress)
	require.NoError(t, err)
	f.corruptAllBut(t, nil)
	got, err := f.store.GetActivityRevision(t.Context(), testFromAddress)
	f.restore(t)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestSQLiteActivityStoreGetActivityHonorsContext verifies a cancelled context stops the read
// with context.Canceled, before any row is decoded (every row's data is undecodable here), for
// the page and the revision alike
func TestSQLiteActivityStoreGetActivityHonorsContext(t *testing.T) {
	f := newPaginationFixture(t)
	f.corruptAllBut(t, nil)
	t.Cleanup(func() { f.restore(t) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	entries, _, _, err := f.store.GetActivity(ctx, testFromAddress, false, types.ActivityFilterAll,
		domain.ActivityPage{PageNumber: 1, PageSize: 10})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, entries)
	_, err = f.store.GetActivityRevision(ctx, testFromAddress)
	require.ErrorIs(t, err, context.Canceled)
}

// TestSQLiteActivityStorePageQueriesUseThePageIndex verifies with EXPLAIN QUERY PLAN that, for
// every filter, the count is served from idx_activity_bridge_page alone and the page is read in
// the index's order — no temporary B-tree sorting every row of the address
func TestSQLiteActivityStorePageQueriesUseThePageIndex(t *testing.T) {
	f := newPaginationFixture(t)
	for _, filter := range activityFilters {
		where, args := activityFilterClause(testFromAddress.Hex(), filter)
		count := queryPlan(t, f.store, "SELECT COUNT(*) FROM activity_bridge WHERE "+where, args...)
		require.Contains(t, count, "USING COVERING INDEX idx_activity_bridge_page", filter.String())
		page := queryPlan(t, f.store,
			"SELECT * FROM activity_bridge WHERE "+where+" ORDER BY "+activityPageOrder+" LIMIT ? OFFSET ?",
			append(args, 10, 10)...)
		require.Contains(t, page, "USING INDEX idx_activity_bridge_page", filter.String())
		require.NotContains(t, page, "TEMP B-TREE", filter.String())
	}
}

func queryPlan(t *testing.T, store *sqliteActivityStore, query string, args ...any) string {
	t.Helper()
	rows, err := store.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	require.NoError(t, err)
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notUsed, &detail))
		details = append(details, detail)
	}
	require.NoError(t, rows.Err())
	return strings.Join(details, "; ")
}

// TestActivityPaginationContract runs the same scanned activity through both stores and checks
// they serve identical pages and totals for every filter — the in-memory store through its bounded
// selection, the SQLite one through SQL — in the legacy order, with an empty page and the real
// total past the end, and the same revision
func TestActivityPaginationContract(t *testing.T) {
	const n = 120
	mainnetFlag := new(big.Int).Lsh(big.NewInt(1), 64) // 2^64
	bridges := make([]*domain.ScannedBridge, 0, n)
	for i := range n {
		gi := big.NewInt(int64(i))
		if i%2 == 0 {
			gi = new(big.Int).Add(mainnetFlag, gi)
		}
		b := &bridgeservicetypes.BridgeResponse{
			GlobalIndex: bridgeservicetypes.BigIntString(gi.String()), DepositCount: uint32(i), DestinationNetwork: 2,
			TxHash: bridgeservicetypes.Hash(fmt.Sprintf("0x%x", i)), BlockNum: uint64(i + 1),
			BlockTimestamp: uint64(1_700_000_000 + i%7), // ties
		}
		bridges = append(bridges, &domain.ScannedBridge{
			Bridge: b, NetworkID: uint32(i % 2), Source: domain.ActivitySourceBridgeService,
		})
	}
	type served struct {
		keys  []domain.BridgeKey
		total int
	}
	results := map[string]map[string]served{}
	revisions := map[string]string{}
	for _, kind := range []string{memoryStore, sqliteStore} {
		t.Run(kind, func(t *testing.T) {
			h := newActivityHarness(t, kind)
			h.scan(bridges)
			// claimed for deposit count % 4 == 0, isClaimed() failing for 1, ready for 2, pending for 3
			h.claims.EXPECT().IsClaimed(mock.Anything, mock.Anything).
				RunAndReturn(func(_ context.Context, b *domain.ScannedBridge) (bool, error) {
					if b.Bridge.DepositCount%4 == 1 {
						return false, fmt.Errorf("boom")
					}
					return b.Bridge.DepositCount%4 == 0, nil
				})
			h.claims.EXPECT().ClaimInfo(mock.Anything, mock.Anything).
				Return(&bridgeservicetypes.ClaimResponse{TxHash: "0xc"}, nil)
			h.claims.EXPECT().IsReadyToClaim(mock.Anything, mock.Anything).
				RunAndReturn(func(_ context.Context, b *domain.ScannedBridge) (bool, error) {
					return b.Bridge.DepositCount%4 == 2, nil
				})
			all := h.refresh()
			require.Len(t, all, n)
			revisions[kind] = h.revision()

			results[kind] = map[string]served{}
			for _, filter := range activityFilters {
				var want []*domain.ActivityEntry
				for _, entry := range all {
					if bridgetracker.MatchesActivityFilter(entry, filter) {
						want = append(want, entry)
					}
				}
				require.NotEmpty(t, want, filter.String())
				slices.SortFunc(want, legacyActivityOrder)
				for _, page := range []domain.ActivityPage{
					{PageNumber: 1, PageSize: 7}, {PageNumber: 3, PageSize: 7}, {PageNumber: 1, PageSize: 200},
					{PageNumber: 100, PageSize: 7},
				} {
					entries, total, _, err := h.store.GetActivity(t.Context(), testFromAddress, false, filter, page)
					require.NoError(t, err)
					require.Equal(t, len(want), total)
					start := min(page.Offset(), uint64(len(want)))
					end := min(start+uint64(page.PageSize), uint64(len(want)))
					require.NotNil(t, entries)
					require.Equal(t, keysOf(want[start:end]), keysOf(entries), "%s page %+v", filter, page)
					results[kind][fmt.Sprintf("%s/%+v", filter, page)] = served{keys: keysOf(entries), total: total}
				}
			}
		})
	}
	require.Equal(t, results[memoryStore], results[sqliteStore], "both stores serve the same pages")
	require.Equal(t, revisions[memoryStore], revisions[sqliteStore])
}

// TestActivityGetActivityOfUnknownAddressIsEmpty verifies both stores answer an address never
// registered with an empty page and a zero total
func TestActivityGetActivityOfUnknownAddressIsEmpty(t *testing.T) {
	for _, kind := range []string{memoryStore, sqliteStore} {
		h := newActivityHarness(t, kind)
		entries, total, warnings, err := h.store.GetActivity(t.Context(), common.HexToAddress("0x9999"), false,
			types.ActivityFilterAll, domain.ActivityPage{PageNumber: 1, PageSize: 10})
		require.NoError(t, err, kind)
		require.Empty(t, entries, kind)
		require.Zero(t, total, kind)
		require.Empty(t, warnings, kind)
	}
}
