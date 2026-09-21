package db

import (
	"context"
	"errors"
	"math/big"
	"path"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var testFromAddress = common.HexToAddress("0x1111111111111111111111111111111111111111")

// testScannedNetworkID is the network these tests' bridges were scanned from, matching
// testBridge's hardcoded OriginNetwork so most tests need not care about the distinction
const testScannedNetworkID = uint32(1)

func testBridge(globalIndex int64) *bridgeservicetypes.BridgeResponse {
	return &bridgeservicetypes.BridgeResponse{
		OriginNetwork:      1,
		DestinationNetwork: 2,
		DepositCount:       uint32(globalIndex),
		GlobalIndex:        bridgeservicetypes.BigIntString(big.NewInt(globalIndex).String()),
		TxHash:             bridgeservicetypes.Hash("0xtx"),
	}
}

func testScannedBridge(globalIndex int64) *domain.ScannedBridge {
	return activityScannedBridge(testBridge(globalIndex), testScannedNetworkID)
}

func activityScannedBridge(bridge *bridgeservicetypes.BridgeResponse, networkID uint32) *domain.ScannedBridge {
	return &domain.ScannedBridge{Bridge: bridge, NetworkID: networkID}
}

// fakeActivityScanner is a hand-rolled ActivityBridgeScanner for tests: it returns whichever of
// bridges is not in known, mirroring ActivitySource.BridgesFrom's real contract, plus whatever
// invalidated is configured (see domain.ActivityBridgeScanner.BridgesFrom's invalidated return)
type fakeActivityScanner struct {
	bridges     []*domain.ScannedBridge
	err         error
	warnings    []domain.ActivityWarning
	invalidated []string
	calls       int
	lastKnown   map[string]domain.KnownBridge
}

func (f *fakeActivityScanner) BridgesFrom(
	_ context.Context, _ common.Address, known map[string]domain.KnownBridge,
) ([]*domain.ScannedBridge, []string, []domain.ActivityWarning, error) {
	f.calls++
	f.lastKnown = known
	if f.err != nil {
		return nil, nil, nil, f.err
	}
	out := make([]*domain.ScannedBridge, 0, len(f.bridges))
	for _, b := range f.bridges {
		if _, ok := known[string(b.Bridge.GlobalIndex)]; ok {
			continue
		}
		out = append(out, b)
	}
	return out, f.invalidated, f.warnings, nil
}

// fakeActivityClaims is a hand-rolled ActivityClaimChecker for tests: isClaimed/claimInfo are
// consulted in FIFO order per call, one entry per expected IsClaimed/ClaimInfo invocation
type fakeActivityClaims struct {
	isClaimed              []bool
	isClaimedErrs          []error
	isClaimedCalls         int
	lastIsClaimedNetworkID uint32
	claimInfo              []*bridgeservicetypes.ClaimResponse
	claimInfoCalls         int
	readyToClaim           bool
	readyToClaimErr        error
	readyToClaims          []bool
	readyToClaimErrs       []error
	readyToClaimCalls      int
}

func (f *fakeActivityClaims) IsClaimed(_ context.Context, bridge *domain.ScannedBridge) (bool, error) {
	f.lastIsClaimedNetworkID = bridge.NetworkID
	i := f.isClaimedCalls
	f.isClaimedCalls++
	if i < len(f.isClaimedErrs) && f.isClaimedErrs[i] != nil {
		return false, f.isClaimedErrs[i]
	}
	return f.isClaimed[i], nil
}

func (f *fakeActivityClaims) ClaimInfo(
	context.Context, *domain.ScannedBridge,
) (*bridgeservicetypes.ClaimResponse, error) {
	claim := f.claimInfo[f.claimInfoCalls]
	f.claimInfoCalls++
	return claim, nil
}

func (f *fakeActivityClaims) IsReadyToClaim(context.Context, *domain.ScannedBridge) (bool, error) {
	i := f.readyToClaimCalls
	f.readyToClaimCalls++
	if i < len(f.readyToClaimErrs) && f.readyToClaimErrs[i] != nil {
		return false, f.readyToClaimErrs[i]
	}
	if i < len(f.readyToClaims) {
		return f.readyToClaims[i], nil
	}
	if f.readyToClaimErr != nil {
		return false, f.readyToClaimErr
	}
	return f.readyToClaim, nil
}

// newTestSQLiteActivityStore builds a sqliteActivityStore with a one-hour idle timeout, long
// enough that no test below evicts anything by accident, backed by a fresh temp-dir DB file
// (shared with a real sqliteRegistry as the supervised store, exactly as they'd share one file
// in production — see NewSQLiteActivityStore's doc)
func newTestSQLiteActivityStore(t *testing.T, scanner domain.ActivityBridgeScanner, claims domain.ActivityClaimChecker,
) *sqliteActivityStore {
	t.Helper()

	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "activity_test")
	supervised, err := NewSQLiteRegistry(dbPath, 10, logger, nil)
	require.NoError(t, err)

	store, err := NewSQLiteActivityStore(dbPath, scanner, claims, supervised, logger, time.Hour)
	require.NoError(t, err)
	s, ok := store.(*sqliteActivityStore)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

// mustRegisterActivity registers addr and fails the test on error, returning whether it is
// ready (see domain.ActivitySupervisedStore.RegisterAndAwait) for the few tests that care;
// callers that don't care simply ignore the return value
func mustRegisterActivity(
	t *testing.T, registry domain.ActivitySupervisedStore, addr common.Address, timeout time.Duration,
) bool {
	t.Helper()
	ready, err := registry.RegisterAndAwait(addr, timeout)
	require.NoError(t, err)
	return ready
}

// refreshAndGet registers addr (a no-op if already registered), optionally primes its sticky
// includeTracking flag (must happen before RefreshAddress, exactly like production:
// activityCommand.Execute's RegisterAndAwait always runs before its own GetActivity call), then
// runs one RefreshAddress + GetActivity round trip — collapsing what RegisterAndAwait/
// ActivityEngine/GetActivity do across separate calls in production into one synchronous step
// for tests that don't exercise the engine or timing directly.
func refreshAndGet(
	t *testing.T, store domain.ActivityRegistry, addr common.Address, includeTracking bool, filter types.ActivityFilter,
) ([]*domain.ActivityEntry, []domain.ActivityWarning, error) {
	t.Helper()
	ctx := t.Context()
	if _, err := store.RegisterAndAwait(addr, 0); err != nil {
		return nil, nil, err
	}
	if includeTracking {
		if _, _, err := store.GetActivity(ctx, addr, true, filter); err != nil {
			return nil, nil, err
		}
	}
	if err := store.RefreshAddress(ctx, addr); err != nil {
		return nil, nil, err
	}
	return store.GetActivity(ctx, addr, includeTracking, filter)
}

// TestSQLiteActivityStoreUnclaimedBridgeIsRecheckedEveryCall verifies an unclaimed bridge's
// claim state is re-verified on every refresh, and that includeTracking=false never registers
// it with the tracker
func TestSQLiteActivityStoreUnclaimedBridgeIsRecheckedEveryCall(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)
	mustRegisterActivity(t, store, testFromAddress, 0)

	for range 2 {
		require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
		entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
		require.Nil(t, entries[0].Claim)
		require.Nil(t, entries[0].Tracking)
	}
	require.Equal(t, 2, claims.isClaimedCalls)
	require.Equal(t, 0, claims.claimInfoCalls)
}

// TestSQLiteActivityStoreGetActivityIsCacheOnly verifies GetActivity never scans or consults
// claims/the tracker itself: only RefreshAddress does.
func TestSQLiteActivityStoreGetActivityIsCacheOnly(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)
	mustRegisterActivity(t, store, testFromAddress, 0)

	entries, _, err := store.GetActivity(t.Context(), testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Empty(t, entries, "nothing has been refreshed yet")
	require.Equal(t, 0, scanner.calls, "GetActivity must never scan")

	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	require.Equal(t, 1, scanner.calls, "RefreshAddress is the one that scans")

	entries, _, err = store.GetActivity(t.Context(), testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, 1, scanner.calls, "a later GetActivity must still never scan")
}

// TestSQLiteActivityStoreIncludeTrackingRegistersUnclaimedBridge verifies includeTracking=true
// registers a still-unclaimed bridge with the supervised store and reports its snapshot, keyed
// by the scanned network — not Bridge.OriginNetwork
func TestSQLiteActivityStoreIncludeTrackingRegistersUnclaimedBridge(t *testing.T) {
	bridge := testBridge(1)
	scanner := &fakeActivityScanner{
		bridges: []*domain.ScannedBridge{activityScannedBridge(bridge, testScannedNetworkID)},
	}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := refreshAndGet(t, store, testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.NotNil(t, entries[0].Tracking)

	wantID := domain.TrackingID{NetworkID: testScannedNetworkID, TxHash: common.HexToHash(string(bridge.TxHash))}
	require.Equal(t, wantID, entries[0].Tracking.ID())
}

// TestSQLiteActivityStoreClaimedAndIndexedBridgeIsNeverRechecked verifies a bridge that is
// claimed with its claim record already fetched is never rechecked on a later refresh —
// settled, and persisted as such across refreshes
func TestSQLiteActivityStoreClaimedAndIndexedBridgeIsNeverRechecked(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// only one IsClaimed/ClaimInfo entry: a second consultation would panic on out-of-range
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}
	store := newTestSQLiteActivityStore(t, scanner, claims)
	mustRegisterActivity(t, store, testFromAddress, 0)

	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)

	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls)
	require.Equal(t, 1, claims.claimInfoCalls)
	require.Equal(t, 2, scanner.calls) // BridgesFrom is still called every refresh to find new bridges
}

// TestSQLiteActivityStoreIsClaimedFailureReportsErrorStatus verifies a failed isClaimed() check
// is reported as ClaimStatusError — never silently as ClaimStatusUnclaimed — and is retried on
// the next refresh
func TestSQLiteActivityStoreIsClaimedFailureReportsErrorStatus(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{false, false},
		isClaimedErrs: []error{errors.New("no bridge contract address configured for network 2"), nil},
	}
	store := newTestSQLiteActivityStore(t, scanner, claims)
	mustRegisterActivity(t, store, testFromAddress, 0)

	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusError, entries[0].ClaimStatus)
	require.Equal(t, "no bridge contract address configured for network 2", entries[0].Errors["claim"])

	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.Equal(t, 2, claims.isClaimedCalls)
	require.Nil(t, entries[0].Errors, "a successful recheck must not carry over the previous failure")
}

// TestSQLiteActivityStoreFilterPendingExcludesClaimedAndErrored verifies ActivityFilterPending
// returns only bridges still unclaimed and not yet ready to claim. The claimed bridge's claim
// record is still fetched during the refresh regardless (a background refresh cannot know a
// future request's filter), it is just excluded from this particular filtered result.
func TestSQLiteActivityStoreFilterPendingExcludesClaimedAndErrored(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	pendingBridge := testScannedBridge(2)
	scanner := &fakeActivityScanner{
		bridges: []*domain.ScannedBridge{
			testScannedBridge(1), pendingBridge, testScannedBridge(3), testScannedBridge(4),
		},
	}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{true, false, false, false},
		isClaimedErrs: []error{nil, nil, errors.New("boom"), nil},
		claimInfo:     []*bridgeservicetypes.ClaimResponse{claim},
		readyToClaims: []bool{false, true},
	}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := refreshAndGet(t, store, testFromAddress, false, types.ActivityFilterPending)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, pendingBridge.Bridge, entries[0].Bridge)
	require.Equal(t, types.TrackerClaimStatusPending, entries[0].TrackerClaimStatus)
	require.Equal(t, 1, claims.claimInfoCalls, "the claimed bridge's record is still fetched during the refresh")
}

// TestSQLiteActivityStoreFilterClaimedExcludesPending verifies ActivityFilterClaimed returns
// only confirmed-claimed bridges
func TestSQLiteActivityStoreFilterClaimedExcludesPending(t *testing.T) {
	claimedBridge := testScannedBridge(1)
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{
		bridges: []*domain.ScannedBridge{claimedBridge, testScannedBridge(2), testScannedBridge(3)},
	}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{true, false, false},
		isClaimedErrs: []error{nil, nil, errors.New("boom")},
		claimInfo:     []*bridgeservicetypes.ClaimResponse{claim},
	}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := refreshAndGet(t, store, testFromAddress, false, types.ActivityFilterClaimed)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, claimedBridge.Bridge, entries[0].Bridge)
	require.Equal(t, claim, entries[0].Claim)
}

// TestSQLiteActivityStoreScannerReceivesGrowingKnownSet verifies the scanner is called with an
// empty known set the first refresh, and with the previously found bridge's key once it has
// been cached — persisted, not just held in memory for the call
func TestSQLiteActivityStoreScannerReceivesGrowingKnownSet(t *testing.T) {
	bridge := testScannedBridge(1)
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{bridge}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)
	mustRegisterActivity(t, store, testFromAddress, 0)

	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	require.Empty(t, scanner.lastKnown, "nothing cached yet on the first refresh")

	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	require.Contains(t, scanner.lastKnown, string(bridge.Bridge.GlobalIndex))
}

// TestSQLiteActivityStoreScannerErrorFailsTheRefresh verifies a scanner failure fails
// RefreshAddress entirely
func TestSQLiteActivityStoreScannerErrorFailsTheRefresh(t *testing.T) {
	wantErr := errors.New("bridge service unreachable")
	scanner := &fakeActivityScanner{err: wantErr}
	store := newTestSQLiteActivityStore(t, scanner, &fakeActivityClaims{})
	mustRegisterActivity(t, store, testFromAddress, 0)

	err := store.RefreshAddress(t.Context(), testFromAddress)
	require.ErrorIs(t, err, wantErr)
}

// TestSQLiteActivityStoreTimestampsTrackCreationAndLastUpdate verifies CreatedAt is stamped
// once and never changes, while UpdatedAt advances on every recheck of a still-unsettled entry
func TestSQLiteActivityStoreTimestampsTrackCreationAndLastUpdate(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)
	mustRegisterActivity(t, store, testFromAddress, 0)

	t1 := time.Now()
	store.now = func() time.Time { return t1 }
	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.True(t, entries[0].CreatedAt.Equal(t1))
	require.True(t, entries[0].UpdatedAt.Equal(t1))

	t2 := t1.Add(time.Minute)
	store.now = func() time.Time { return t2 }
	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.True(t, entries[0].CreatedAt.Equal(t1), "creation time must not change")
	require.True(t, entries[0].UpdatedAt.Equal(t2), "update time must advance on every recheck")
}

// TestSQLiteActivityStorePruneIdleDeletesRowsAndCascades verifies PruneIdle deletes an idle
// address's row and, via ON DELETE CASCADE, every activity_bridge row cached for it — the real
// retention sweep that replaces the old sweepIdle no-op (see issue #1822) — and leaves a
// recently accessed address untouched.
func TestSQLiteActivityStorePruneIdleDeletesRowsAndCascades(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	now := time.Now()
	store.now = func() time.Time { return now }

	entries, _, err := refreshAndGet(t, store, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)

	pruned, err := store.PruneIdle(now.Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, 0, pruned, "just accessed, not idle yet")

	future := now.Add(time.Hour)
	pruned, err = store.PruneIdle(future)
	require.NoError(t, err)
	require.Equal(t, 1, pruned)

	var addrCount, bridgeCount int
	require.NoError(t, store.db.QueryRow("SELECT COUNT(*) FROM activity_address").Scan(&addrCount))
	require.NoError(t, store.db.QueryRow("SELECT COUNT(*) FROM activity_bridge").Scan(&bridgeCount))
	require.Equal(t, 0, addrCount)
	require.Equal(t, 0, bridgeCount, "activity_bridge rows must cascade-delete with their address")
}

// TestSQLiteActivityStoreGetActiveAddresses verifies GetActiveAddresses reports every
// registered address, and nothing once it has been pruned.
func TestSQLiteActivityStoreGetActiveAddresses(t *testing.T) {
	store := newTestSQLiteActivityStore(t, &fakeActivityScanner{}, &fakeActivityClaims{})
	other := common.HexToAddress("0x2222222222222222222222222222222222222222")

	mustRegisterActivity(t, store, testFromAddress, 0)
	mustRegisterActivity(t, store, other, 0)

	addrs, err := store.GetActiveAddresses()
	require.NoError(t, err)
	require.ElementsMatch(t, []common.Address{testFromAddress, other}, addrs)

	pruned, err := store.PruneIdle(time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, 2, pruned)

	addrs, err = store.GetActiveAddresses()
	require.NoError(t, err)
	require.Empty(t, addrs)
}

// TestSQLiteActivityStoreRegisterAddressConcurrentSameAddressCountsOnce pins that racing
// registerAddress calls for the very same brand-new address only ever increment numAddresses
// once: before the fix, both calls could observe aggkitdb.ErrNotFound before either INSERT
// completed and both increment the in-memory count, permanently overcounting relative to the
// actual activity_address row count and triggering ErrActivityRegistryFull well before the
// registry is actually full (see PR #1856 review).
func TestSQLiteActivityStoreRegisterAddressConcurrentSameAddressCountsOnce(t *testing.T) {
	store := newTestSQLiteActivityStore(t, &fakeActivityScanner{}, &fakeActivityClaims{})

	const racers = 20
	now := time.Now()
	var wg sync.WaitGroup
	wg.Add(racers)
	createdCount := int32(0)
	for range racers {
		go func() {
			defer wg.Done()
			created, err := store.registerAddress(testFromAddress.Hex(), now)
			require.NoError(t, err)
			if created {
				atomic.AddInt32(&createdCount, 1)
			}
		}()
	}
	wg.Wait()

	require.EqualValues(t, 1, createdCount, "exactly one racer should have created the row")

	store.countMu.Lock()
	numAddresses := store.numAddresses
	store.countMu.Unlock()
	require.Equal(t, 1, numAddresses, "numAddresses must match the single row actually inserted")
}

// TestSQLiteActivityStoreRegisterAndAwaitWaitsForRefresh verifies RegisterAndAwait blocks a new
// address's caller until RefreshAddress completes, when given a positive timeout.
func TestSQLiteActivityStoreRegisterAndAwaitWaitsForRefresh(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	type registerResult struct {
		ready bool
		err   error
	}
	done := make(chan registerResult, 1)
	go func() {
		ready, err := store.RegisterAndAwait(testFromAddress, time.Second)
		done <- registerResult{ready: ready, err: err}
	}()

	select {
	case addr := <-store.Triggers():
		require.Equal(t, testFromAddress, addr)
		require.NoError(t, store.RefreshAddress(t.Context(), addr))
	case <-time.After(time.Second):
		t.Fatal("RegisterAndAwait never signaled the trigger")
	}

	select {
	case res := <-done:
		require.NoError(t, res.err)
		require.True(t, res.ready, "ready must be true once the refresh completed before timeout")
	case <-time.After(time.Second):
		t.Fatal("RegisterAndAwait never returned after the refresh completed")
	}

	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// TestSQLiteActivityStorePersistsAcrossInstances pins the actual point of this adapter: a
// second store opened over the same DB file sees exactly what the first one wrote
func TestSQLiteActivityStorePersistsAcrossInstances(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}

	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "activity_test")
	supervised, err := NewSQLiteRegistry(dbPath, 10, logger, nil)
	require.NoError(t, err)

	first, err := NewSQLiteActivityStore(dbPath, scanner, claims, supervised, logger, time.Hour)
	require.NoError(t, err)
	entries, _, err := refreshAndGet(t, first, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	firstSQLite, ok := first.(*sqliteActivityStore)
	require.True(t, ok)
	require.NoError(t, firstSQLite.Close())

	// second store, second scanner/claims that would panic if consulted again: proves the
	// second instance served the result straight from the DB, without rescanning/rechecking
	second, err := NewSQLiteActivityStore(
		dbPath, &fakeActivityScanner{}, &fakeActivityClaims{}, supervised, logger, time.Hour)
	require.NoError(t, err)
	secondSQLite, ok := second.(*sqliteActivityStore)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, secondSQLite.Close()) })

	entries, _, err = second.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, claim, entries[0].Claim)
}

// TestSQLiteActivityStorePersistsSource pins that saveBridgeRow persists ScannedBridge.Source
// alongside Bridge/Claim/Errors: a reload must still see which system (bridge-service or RPC)
// supplied the cached entry, since fetchNewBridgesFrom/invalidatedBridges key off it to decide
// whether a later scan can upgrade or must invalidate the cached entry
func TestSQLiteActivityStorePersistsSource(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{
		{Bridge: testBridge(1), NetworkID: testScannedNetworkID, Source: domain.ActivitySourceRPC},
	}}
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{
		{TxHash: "0xclaimtx"},
	}}

	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "activity_test")
	supervised, err := NewSQLiteRegistry(dbPath, 10, logger, nil)
	require.NoError(t, err)

	first, err := NewSQLiteActivityStore(dbPath, scanner, claims, supervised, logger, time.Hour)
	require.NoError(t, err)
	entries, _, err := refreshAndGet(t, first, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, domain.ActivitySourceRPC, entries[0].Source)
	firstSQLite, ok := first.(*sqliteActivityStore)
	require.True(t, ok)
	require.NoError(t, firstSQLite.Close())

	// second store, second scanner that would panic if consulted again: proves the second
	// instance served the entry straight from the DB, Source included, without rescanning
	second, err := NewSQLiteActivityStore(dbPath, &fakeActivityScanner{}, &fakeActivityClaims{}, supervised, logger, time.Hour)
	require.NoError(t, err)
	secondSQLite, ok := second.(*sqliteActivityStore)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, secondSQLite.Close()) })

	entries, _, err = second.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, domain.ActivitySourceRPC, entries[0].Source)
}

// TestSQLiteActivityStoreRecordsScanWarnings verifies a network's scan failure is persisted into
// activity_address.scan_state, and that a fully successful scan leaves it untouched
func TestSQLiteActivityStoreRecordsScanWarnings(t *testing.T) {
	warning := domain.ActivityWarning{NetworkID: 7, Message: "bridge service unreachable"}
	scanner := &fakeActivityScanner{warnings: []domain.ActivityWarning{warning}}
	store := newTestSQLiteActivityStore(t, scanner, &fakeActivityClaims{})

	_, warnings, err := refreshAndGet(t, store, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, []domain.ActivityWarning{warning}, warnings)

	row, err := store.selectAddressRow(testFromAddress.Hex())
	require.NoError(t, err)
	scanState, err := decodeScanState(row.ScanState)
	require.NoError(t, err)
	require.Equal(t, warning.Message, scanState[7].LastError)
	require.NotZero(t, scanState[7].LastErrorAt)
}

// TestSQLiteActivityStoreWarningsReflectLastRefresh verifies GetActivity reports whatever the
// last RefreshAddress call persisted into last_warnings, whether or not GetActivity itself is the
// caller that triggers it — it never scans inline any more (see RefreshAddress's doc)
func TestSQLiteActivityStoreWarningsReflectLastRefresh(t *testing.T) {
	warning := domain.ActivityWarning{NetworkID: 7, Message: "bridge service unreachable"}
	scanner := &fakeActivityScanner{warnings: []domain.ActivityWarning{warning}}
	store := newTestSQLiteActivityStore(t, scanner, &fakeActivityClaims{})

	_, warnings, err := refreshAndGet(t, store, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, []domain.ActivityWarning{warning}, warnings)

	// a later GetActivity call on its own (no refresh in between) still reports the same
	// last-persisted warnings, since GetActivity is a cache-only read
	_, warnings, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, []domain.ActivityWarning{warning}, warnings)

	// a subsequent clean refresh overwrites last_warnings with the new (empty) result
	scanner.warnings = nil
	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	_, warnings, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Empty(t, warnings)
}

// TestSQLiteActivityStoreIncludeTrackingPersistsAcrossRestart verifies the sticky
// include_tracking flag survives reopening the same DB file: a second store, opened after the
// first set the flag, still enriches tracking on its own next refresh.
func TestSQLiteActivityStoreIncludeTrackingPersistsAcrossRestart(t *testing.T) {
	bridge := testBridge(1)
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{activityScannedBridge(bridge, testScannedNetworkID)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}

	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "activity_test")
	supervised, err := NewSQLiteRegistry(dbPath, 10, logger, nil)
	require.NoError(t, err)

	first, err := NewSQLiteActivityStore(dbPath, scanner, claims, supervised, logger, time.Hour)
	require.NoError(t, err)
	mustRegisterActivity(t, first, testFromAddress, 0)
	// prime the sticky flag without refreshing yet, exactly like activityCommand.Execute's
	// RegisterAndAwait-then-GetActivity ordering
	_, _, err = first.GetActivity(t.Context(), testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	firstSQLite, ok := first.(*sqliteActivityStore)
	require.True(t, ok)
	require.NoError(t, firstSQLite.Close())

	second, err := NewSQLiteActivityStore(dbPath, scanner, claims, supervised, logger, time.Hour)
	require.NoError(t, err)
	secondSQLite, ok := second.(*sqliteActivityStore)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, secondSQLite.Close()) })

	require.NoError(t, second.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := second.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].Tracking, "include_tracking must survive the restart")
}

// TestSQLiteActivityStoreBridgeNetworkIDUsesScannedNetworkNotBridgeOriginNetwork verifies
// ActivityEntry.BridgeNetworkID reflects the network the bridge was scanned from, not
// Bridge.OriginNetwork
func TestSQLiteActivityStoreBridgeNetworkIDUsesScannedNetworkNotBridgeOriginNetwork(t *testing.T) {
	const scannedNetworkID = uint32(7)
	bridge := testBridge(1)
	bridge.OriginNetwork = 99
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{activityScannedBridge(bridge, scannedNetworkID)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}
	store := newTestSQLiteActivityStore(t, scanner, claims)

	entries, _, err := refreshAndGet(t, store, testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, scannedNetworkID, entries[0].BridgeNetworkID)
	require.Equal(t, scannedNetworkID, claims.lastIsClaimedNetworkID)
	require.Equal(t, scannedNetworkID, entries[0].Tracking.ID().NetworkID)
}

// TestSQLiteActivityStoreSchemaVersionMismatchIsAMiss pins that a bridge row written under a
// different schema_version is treated exactly like a missing one
func TestSQLiteActivityStoreSchemaVersionMismatchIsAMiss(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{true, false}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}
	store := newTestSQLiteActivityStore(t, scanner, claims)
	mustRegisterActivity(t, store, testFromAddress, 0)

	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)

	_, err = store.db.Exec("UPDATE activity_bridge SET schema_version = ?", activitySchemaVersion+1)
	require.NoError(t, err)

	// a settled (claimed) bridge whose row is now stale must be re-scanned/re-refreshed from
	// scratch, not read back as claimed straight from the stale row
	require.NoError(t, store.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.Equal(t, 2, claims.isClaimedCalls)
}
