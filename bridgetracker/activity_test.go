package bridgetracker

import (
	"context"
	"errors"
	"math/big"
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
// testBridge's hardcoded OriginNetwork so most tests need not care about the distinction (see
// TestActivityCache_BridgeNetworkIDUsesScannedNetworkNotBridgeOriginNetwork for a test that does)
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

// testScannedBridge wraps testBridge with testScannedNetworkID, the network these tests treat
// as "the bridge service that returned it"
func testScannedBridge(globalIndex int64) *domain.ScannedBridge {
	return scannedBridge(testBridge(globalIndex), testScannedNetworkID)
}

func scannedBridge(bridge *bridgeservicetypes.BridgeResponse, networkID uint32) *domain.ScannedBridge {
	return &domain.ScannedBridge{Bridge: bridge, NetworkID: networkID}
}

// fakeActivityScanner is a hand-rolled ActivityBridgeScanner for tests: it returns whichever of
// bridges is not in known, mirroring ActivitySource.BridgesFrom's real contract, plus whatever
// invalidated is configured (see domain.ActivityBridgeScanner.BridgesFrom's invalidated return).
// calls records how many times it was invoked, lastKnown the known argument it was last called
// with.
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
// consulted in FIFO order per call, one entry per expected IsClaimed/ClaimInfo invocation, so a
// test can assert exactly how many times each was called (and fail loudly if called more).
// isClaimedErrs, if non-nil, is consulted alongside isClaimed: a non-nil entry makes that call
// fail instead of returning the paired isClaimed value. lastIsClaimedNetworkID records the
// NetworkID of the last ScannedBridge IsClaimed was called with. readyToClaim/readyToClaimErr
// configure IsReadyToClaim's own (single, reused) result, defaulting to "not ready, no error"
// for tests that don't care about it; readyToClaims/readyToClaimErrs, if non-nil, are consulted
// in FIFO order instead — one entry per expected IsReadyToClaim invocation — for tests that need
// a mix of pending/ready-to-claim bridges in the same call.
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

// newTestActivityCache builds an ActivityCache with a one-hour idle timeout, long enough that
// no test below evicts anything by accident; tests exercising eviction build their own directly.
func newTestActivityCache(scanner ActivityBridgeScanner, claims ActivityClaimChecker) *ActivityCache {
	supervised := NewMemoryRegistry(10)
	return NewActivityCache(scanner, claims, supervised, log.WithFields("module", "activity_test"), time.Hour)
}

// refreshAndGet registers addr (a no-op if already registered), optionally primes its sticky
// includeTracking flag (see domain.ActivityQuerier.GetActivity's doc — the flag must already be
// set before RefreshAddress runs for that refresh to enrich tracking, exactly like production:
// activityCommand.Execute's RegisterAndAwait always runs before its own GetActivity call, so by
// the time a request that asked for includeTracking can flip the flag, the entry it flips it on
// already exists), then runs one RefreshAddress + GetActivity round trip — collapsing what
// RegisterAndAwait/ActivityEngine/GetActivity do across separate calls in production into one
// synchronous step for tests that don't exercise the engine or timing directly.
func refreshAndGet(
	t *testing.T, registry ActivityRegistry, addr common.Address, includeTracking bool, filter types.ActivityFilter,
) ([]*domain.ActivityEntry, []domain.ActivityWarning, error) {
	t.Helper()
	ctx := t.Context()
	if err := registry.RegisterAndAwait(addr, 0); err != nil {
		return nil, nil, err
	}
	if includeTracking {
		if _, _, err := registry.GetActivity(ctx, addr, true, filter); err != nil {
			return nil, nil, err
		}
	}
	if err := registry.RefreshAddress(ctx, addr); err != nil {
		return nil, nil, err
	}
	return registry.GetActivity(ctx, addr, includeTracking, filter)
}

// TestActivityCache_UnclaimedBridgeIsRecheckedEveryCall verifies an unclaimed bridge's claim
// state is re-verified on every refresh, and that includeTracking=false never registers it with
// the tracker.
func TestActivityCache_UnclaimedBridgeIsRecheckedEveryCall(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}

	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))

	for range 2 {
		require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
		entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
		require.Nil(t, entries[0].Claim)
		require.Nil(t, entries[0].Tracking)
	}
	require.Equal(t, 2, claims.isClaimedCalls)
	require.Equal(t, 0, claims.claimInfoCalls)
}

// TestActivityCache_GetActivityIsCacheOnly verifies GetActivity never scans or consults
// claims/the tracker itself: only RefreshAddress does. This is the key regression test for the
// split between the two.
func TestActivityCache_GetActivityIsCacheOnly(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}

	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Empty(t, entries, "nothing has been refreshed yet")
	require.Equal(t, 0, scanner.calls, "GetActivity must never scan")
	require.Equal(t, 0, claims.isClaimedCalls, "GetActivity must never consult claims")

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	require.Equal(t, 1, scanner.calls, "RefreshAddress is the one that scans")

	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, 1, scanner.calls, "a later GetActivity must still never scan")
}

// TestActivityCache_IncludeTrackingRegistersUnclaimedBridge verifies includeTracking=true
// registers a still-unclaimed bridge with the supervised store (register-only) and reports its
// snapshot, keyed by the scanned network — not Bridge.OriginNetwork.
func TestActivityCache_IncludeTrackingRegistersUnclaimedBridge(t *testing.T) {
	bridge := testBridge(1)
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{scannedBridge(bridge, testScannedNetworkID)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := refreshAndGet(t, cache, testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.NotNil(t, entries[0].Tracking)

	wantID := domain.TrackingID{NetworkID: testScannedNetworkID, TxHash: common.HexToHash(string(bridge.TxHash))}
	require.Equal(t, wantID, entries[0].Tracking.ID())
}

// TestActivityCache_TrackerClaimStatusMirrorsClaimState pins how ActivityEntry.TrackerClaimStatus
// is derived for every combination the wire "Claimed" field can report: TrackerClaimStatusClaimed/
// Error mirror ClaimStatus directly; while unclaimed, includeTracking=true copies the tracker's
// own snapshot (a fresh, unresolved snapshot reads TrackerClaimStatusPending, per
// domain.TrackingData.ClaimStatus), and never falls back to IsReadyToClaim, while
// includeTracking=false resolves it directly through IsReadyToClaim instead.
func TestActivityCache_TrackerClaimStatusMirrorsClaimState(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}

	testCases := []struct {
		name            string
		isClaimed       bool
		isClaimedErr    error
		includeTracking bool
		readyToClaim    bool
		want            types.TrackerClaimStatus
		wantReadyCalls  int
	}{
		{
			name:      "claimed -> TrackerClaimStatusClaimed",
			isClaimed: true,
			want:      types.TrackerClaimStatusClaimed,
		},
		{
			name:         "isClaimed() failure -> TrackerClaimStatusError",
			isClaimedErr: errors.New("boom"),
			want:         types.TrackerClaimStatusError,
		},
		{
			name:            "unclaimed, includeTracking=true -> copied from the tracker's own snapshot",
			isClaimed:       false,
			includeTracking: true,
			readyToClaim:    true, // must be ignored: tracking short-circuits IsReadyToClaim
			want:            types.TrackerClaimStatusPending,
			wantReadyCalls:  0,
		},
		{
			name:            "unclaimed, includeTracking=false, not yet ready -> TrackerClaimStatusPending",
			isClaimed:       false,
			includeTracking: false,
			readyToClaim:    false,
			want:            types.TrackerClaimStatusPending,
			wantReadyCalls:  1,
		},
		{
			name:            "unclaimed, includeTracking=false, ready -> TrackerClaimStatusReadyToClaim",
			isClaimed:       false,
			includeTracking: false,
			readyToClaim:    true,
			want:            types.TrackerClaimStatusReadyToClaim,
			wantReadyCalls:  1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
			claims := &fakeActivityClaims{
				isClaimed:     []bool{tc.isClaimed},
				isClaimedErrs: []error{tc.isClaimedErr},
				claimInfo:     []*bridgeservicetypes.ClaimResponse{claim},
				readyToClaim:  tc.readyToClaim,
			}
			cache := newTestActivityCache(scanner, claims)

			entries, _, err := refreshAndGet(t, cache, testFromAddress, tc.includeTracking, types.ActivityFilterAll)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, tc.want, entries[0].TrackerClaimStatus)
			require.Equal(t, tc.wantReadyCalls, claims.readyToClaimCalls)
		})
	}
}

// TestActivityCache_ReadyToClaimFailureLeavesPendingAndReportsError verifies a failed
// IsReadyToClaim check does not fail the whole entry: TrackerClaimStatus conservatively stays
// TrackerClaimStatusPending, and the failure is reported under Errors["readiness"].
func TestActivityCache_ReadyToClaimFailureLeavesPendingAndReportsError(t *testing.T) {
	wantErr := errors.New("l1 info tree index unavailable")
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}, readyToClaimErr: wantErr}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, types.TrackerClaimStatusPending, entries[0].TrackerClaimStatus)
	require.Equal(t, wantErr.Error(), entries[0].Errors["readiness"])
}

// TestActivityCache_ClaimedAndIndexedBridgeIsNeverRechecked verifies a bridge that is claimed
// with its claim record already fetched is never rechecked on a later refresh.
func TestActivityCache_ClaimedAndIndexedBridgeIsNeverRechecked(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// only one IsClaimed/ClaimInfo entry: a second consultation would panic on out-of-range
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}

	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls)
	require.Equal(t, 1, claims.claimInfoCalls)
	require.Equal(t, 2, scanner.calls) // BridgesFrom is still called every refresh to find new bridges
}

// TestActivityCache_FlushActivityForcesRecheck verifies that, unlike a plain refresh,
// FlushActivity discards a settled (claimed + indexed) entry entirely, so the next refresh
// re-verifies it from scratch instead of reusing it untouched (see settled)
func TestActivityCache_FlushActivityForcesRecheck(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// two isClaimed/claimInfo entries: a third consultation would panic on out-of-range, proving
	// FlushActivity causes exactly one extra recheck, not unbounded rechecks
	claims := &fakeActivityClaims{
		isClaimed: []bool{true, true},
		claimInfo: []*bridgeservicetypes.ClaimResponse{claim, claim},
	}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)

	// a plain refresh would leave the settled entry untouched (see
	// TestActivityCache_ClaimedAndIndexedBridgeIsNeverRechecked) -- flushing forces a recheck
	cache.FlushActivity(testFromAddress)
	entries, _, err = refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 2, claims.isClaimedCalls)
	require.Equal(t, 2, claims.claimInfoCalls)

	// flushing an address with nothing cached is a harmless no-op
	require.NotPanics(t, func() { cache.FlushActivity(common.HexToAddress("0x02")) })
}

// TestActivityCache_ClaimedButNotYetIndexedBridgeIsRetried verifies a bridge reported as claimed
// on-chain, but whose claim record the destination bridge service has not indexed yet (ClaimInfo
// returns nil), has its claim record retried on the next refresh — without asking isClaimed()
// again, since a confirmed claim never reverts (see ActivityCache.refresh).
func TestActivityCache_ClaimedButNotYetIndexedBridgeIsRetried(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// a single isClaimed entry: a second consultation would panic on out-of-range, proving it is
	// never asked again once confirmed claimed
	claims := &fakeActivityClaims{
		isClaimed: []bool{true},
		claimInfo: []*bridgeservicetypes.ClaimResponse{nil, claim},
	}

	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Nil(t, entries[0].Claim)

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls, "isClaimed must not be asked again once confirmed claimed")
	require.Equal(t, 2, claims.claimInfoCalls)
}

// TestActivityCache_ScannerErrorFailsTheRefresh verifies a scanner failure fails RefreshAddress
// entirely.
func TestActivityCache_ScannerErrorFailsTheRefresh(t *testing.T) {
	wantErr := errors.New("bridge service unreachable")
	scanner := &fakeActivityScanner{err: wantErr}
	cache := newTestActivityCache(scanner, &fakeActivityClaims{})
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))

	err := cache.RefreshAddress(t.Context(), testFromAddress)
	require.ErrorIs(t, err, wantErr)
}

// TestActivityCache_IsClaimedFailureReportsErrorStatus verifies a failed isClaimed() check
// (e.g. no bridge contract address configured for the destination network) is reported as
// ClaimStatusError — never silently as ClaimStatusUnclaimed — and is retried on the next refresh
// (unlike a confirmed claim, an error is not permanent).
func TestActivityCache_IsClaimedFailureReportsErrorStatus(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{false, false},
		isClaimedErrs: []error{errors.New("no bridge contract address configured for network 2"), nil},
	}

	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusError, entries[0].ClaimStatus)
	require.Nil(t, entries[0].Claim)
	require.Equal(t, "no bridge contract address configured for network 2", entries[0].Errors["claim"])

	// the error state is not settled: it is retried on the next refresh
	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.Equal(t, 2, claims.isClaimedCalls)
	require.Nil(t, entries[0].Errors, "a successful recheck must not carry over the previous failure")
}

// TestActivityCache_FilterPendingExcludesClaimedAndErrored verifies ActivityFilterPending
// returns only bridges still unclaimed and not yet ready to claim — excluding claimed,
// ready-to-claim, and errored ones. The claimed bridge's claim record is still fetched during
// the refresh regardless (a background refresh cannot know a future request's filter), it is
// just excluded from this particular filtered result.
func TestActivityCache_FilterPendingExcludesClaimedAndErrored(t *testing.T) {
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
		readyToClaims: []bool{false, true}, // pendingBridge (not ready), then testScannedBridge(4) (ready)
	}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterPending)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, pendingBridge.Bridge, entries[0].Bridge)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.Equal(t, types.TrackerClaimStatusPending, entries[0].TrackerClaimStatus)
	require.Equal(t, 1, claims.claimInfoCalls, "the claimed bridge's record is still fetched during the refresh")
}

// TestActivityCache_FilterReadyToClaimReturnsOnlyReady verifies ActivityFilterReadyToClaim
// returns only bridges still unclaimed and ready to claim — excluding claimed, still-pending,
// and errored ones.
func TestActivityCache_FilterReadyToClaimReturnsOnlyReady(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	readyBridge := testScannedBridge(3)
	scanner := &fakeActivityScanner{
		bridges: []*domain.ScannedBridge{
			testScannedBridge(1), testScannedBridge(2), readyBridge, testScannedBridge(4),
		},
	}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{true, false, false, false},
		isClaimedErrs: []error{nil, errors.New("boom"), nil, nil},
		claimInfo:     []*bridgeservicetypes.ClaimResponse{claim},
		readyToClaims: []bool{true, false}, // readyBridge (ready), then testScannedBridge(4) (not ready)
	}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterReadyToClaim)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, readyBridge.Bridge, entries[0].Bridge)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.Equal(t, types.TrackerClaimStatusReadyToClaim, entries[0].TrackerClaimStatus)
}

// TestActivityCache_FilterErrorReturnsOnlyErrored verifies ActivityFilterError returns only
// bridges whose isClaimed() check failed, excluding claimed and pending ones.
func TestActivityCache_FilterErrorReturnsOnlyErrored(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	erroredBridge := testScannedBridge(3)
	wantErr := errors.New("boom")
	scanner := &fakeActivityScanner{
		bridges: []*domain.ScannedBridge{testScannedBridge(1), testScannedBridge(2), erroredBridge},
	}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{true, false, false},
		isClaimedErrs: []error{nil, nil, wantErr},
		claimInfo:     []*bridgeservicetypes.ClaimResponse{claim},
	}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterError)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, erroredBridge.Bridge, entries[0].Bridge)
	require.Equal(t, types.ClaimStatusError, entries[0].ClaimStatus)
	require.Equal(t, wantErr.Error(), entries[0].Errors["claim"])
}

// TestActivityCache_FilterClaimedExcludesPending verifies ActivityFilterClaimed returns only
// confirmed-claimed bridges, excluding both unclaimed ones and ones whose check errored.
func TestActivityCache_FilterClaimedExcludesPending(t *testing.T) {
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

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterClaimed)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, claimedBridge.Bridge, entries[0].Bridge)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)
}

// TestActivityCache_ClaimedBridgeExcludedFromPendingButVisibleUnderAll verifies a claimed bridge
// (its claim record already fetched during the refresh, regardless of filter) is excluded from
// a filterBridges=pending read but shows up once filterBridges=all is used instead — the same
// cached entry, read with two different filters, with no extra refresh needed in between.
func TestActivityCache_ClaimedBridgeExcludedFromPendingButVisibleUnderAll(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// a single isClaimed/claimInfo entry: a second consultation would panic on out-of-range
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}

	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))
	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterPending)
	require.NoError(t, err)
	require.Empty(t, entries, "a claimed bridge must not appear under filterBridges=pending")

	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls)
	require.Equal(t, 1, claims.claimInfoCalls, "no second refresh happened, so ClaimInfo was fetched exactly once")
}

// TestActivityCache_ScannerReceivesGrowingKnownSet verifies the scanner is called with an empty
// known set the first refresh (nothing cached yet), and with the previously found bridge's key
// once it has been cached.
func TestActivityCache_ScannerReceivesGrowingKnownSet(t *testing.T) {
	bridge := testScannedBridge(1)
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{bridge}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}

	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	require.Empty(t, scanner.lastKnown, "nothing cached yet on the first refresh")

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	require.Contains(t, scanner.lastKnown, string(bridge.Bridge.GlobalIndex))
}

// TestActivityCache_SourceIsCarriedForwardAcrossRechecks verifies a bridge's Source (which
// system supplied it — the bridge service or the RPC fallback, see domain.ActivitySourceKind) is
// recorded on first scan and survives every later recheck, even once the scanner itself stops
// reporting it (because it is now cached/"known" — see fakeActivityScanner): the synthetic
// re-check pass in RefreshAddress must carry Source forward from the cached entry, not reset it
// to its zero value.
func TestActivityCache_SourceIsCarriedForwardAcrossRechecks(t *testing.T) {
	bridge := testScannedBridge(1)
	bridge.Source = domain.ActivitySourceRPC
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{bridge}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}

	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, domain.ActivitySourceRPC, entries[0].Source)

	// second refresh: the scanner no longer reports it (already known), so this exercises the
	// synthetic recheck path, not a fresh scan result
	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, domain.ActivitySourceRPC, entries[0].Source, "Source must survive the recheck, not reset to empty")
}

// TestActivityCache_ForgetsInvalidatedBridges verifies a bridge the scanner reports as
// invalidated (see domain.ActivityBridgeScanner.BridgesFrom's invalidated return — a bridge an
// RPC-based source once reported that a reorg has since removed, with nothing yet re-including
// it) is forgotten: removed from the cache and absent from the result, even though it was cached
// and unclaimed just before this refresh.
func TestActivityCache_ForgetsInvalidatedBridges(t *testing.T) {
	bridge := testScannedBridge(1)
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{bridge}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}
	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// second refresh: the scanner no longer finds it at all, and reports it invalidated instead
	scanner.bridges = nil
	scanner.invalidated = []string{string(bridge.Bridge.GlobalIndex)}

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Empty(t, entries, "an invalidated bridge must be forgotten, not served with stale data")
}

// TestActivityCache_PruneIdleForgetsUnaccessedAddresses verifies PruneIdle forgets an address
// last accessed before the cutoff, proven indirectly by observing isClaimed() being asked again
// for a bridge that had already settled — which would not happen if its cached state had
// survived — and leaves a recently accessed address untouched.
func TestActivityCache_PruneIdleForgetsUnaccessedAddresses(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{
		isClaimed: []bool{true, true},
		claimInfo: []*bridgeservicetypes.ClaimResponse{claim, claim},
	}

	supervised := NewMemoryRegistry(10)
	cache := NewActivityCache(scanner, claims, supervised, log.WithFields("module", "activity_test"), time.Minute)
	now := time.Now()
	cache.now = func() time.Time { return now }

	entries, _, err := refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls, "settled after the first refresh")

	pruned, err := cache.PruneIdle(now.Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, 0, pruned, "just accessed, not idle yet")

	now = now.Add(2 * time.Minute) // past idleTimeout

	pruned, err = cache.PruneIdle(now.Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, pruned)

	entries, _, err = refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 2, claims.isClaimedCalls, "the address was forgotten, so isClaimed is asked again from scratch")
}

// TestActivityCache_RegisterAndAwaitWaitsForRefresh verifies RegisterAndAwait blocks a new
// address's caller until RefreshAddress completes, when given a positive timeout.
func TestActivityCache_RegisterAndAwaitWaitsForRefresh(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}
	cache := newTestActivityCache(scanner, claims)

	done := make(chan error, 1)
	go func() { done <- cache.RegisterAndAwait(testFromAddress, time.Second) }()

	select {
	case addr := <-cache.Triggers():
		require.Equal(t, testFromAddress, addr)
		require.NoError(t, cache.RefreshAddress(t.Context(), addr))
	case <-time.After(time.Second):
		t.Fatal("RegisterAndAwait never signaled the trigger")
	}

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("RegisterAndAwait never returned after the refresh completed")
	}

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// TestActivityCache_RegisterAndAwaitExistingAddressReturnsImmediately verifies an
// already-registered address never triggers or waits, regardless of timeout.
func TestActivityCache_RegisterAndAwaitExistingAddressReturnsImmediately(t *testing.T) {
	cache := newTestActivityCache(&fakeActivityScanner{}, &fakeActivityClaims{})
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))

	// drain the trigger the first registration signaled, so a second signal would prove a
	// (wrong) re-trigger, not a leftover from before
	<-cache.Triggers()

	require.NoError(t, cache.RegisterAndAwait(testFromAddress, time.Hour))
	select {
	case addr := <-cache.Triggers():
		t.Fatalf("unexpected trigger for already-registered address %s", addr)
	default:
	}
}

// TestActivityCache_RegisterAndAwaitTimeoutFallsBackToWhateverIsCached verifies that if timeout
// elapses before RefreshAddress runs, RegisterAndAwait still returns (no error), and GetActivity
// simply reports nothing cached yet.
func TestActivityCache_RegisterAndAwaitTimeoutFallsBackToWhateverIsCached(t *testing.T) {
	cache := newTestActivityCache(&fakeActivityScanner{}, &fakeActivityClaims{})

	err := cache.RegisterAndAwait(testFromAddress, 10*time.Millisecond)
	require.NoError(t, err)

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// TestActivityCache_GetActiveAddresses verifies GetActiveAddresses reports every registered
// address, and nothing once it has been pruned.
func TestActivityCache_GetActiveAddresses(t *testing.T) {
	cache := newTestActivityCache(&fakeActivityScanner{}, &fakeActivityClaims{})
	other := common.HexToAddress("0x2222222222222222222222222222222222222222")

	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))
	require.NoError(t, cache.RegisterAndAwait(other, 0))

	addrs, err := cache.GetActiveAddresses()
	require.NoError(t, err)
	require.ElementsMatch(t, []common.Address{testFromAddress, other}, addrs)

	pruned, err := cache.PruneIdle(time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, 2, pruned)

	addrs, err = cache.GetActiveAddresses()
	require.NoError(t, err)
	require.Empty(t, addrs)
}

// TestActivityCache_TimestampsTrackCreationAndLastUpdate verifies CreatedAt is stamped once and
// never changes, while UpdatedAt advances on every recheck of a still-unsettled entry.
func TestActivityCache_TimestampsTrackCreationAndLastUpdate(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}

	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))
	t1 := time.Now()
	cache.now = func() time.Time { return t1 }

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.True(t, entries[0].CreatedAt.Equal(t1))
	require.True(t, entries[0].UpdatedAt.Equal(t1))

	t2 := t1.Add(time.Minute)
	cache.now = func() time.Time { return t2 }

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.True(t, entries[0].CreatedAt.Equal(t1), "creation time must not change")
	require.True(t, entries[0].UpdatedAt.Equal(t2), "update time must advance on every recheck")
}

// TestActivityCache_TimestampsFreezeOnceSettled verifies UpdatedAt stops advancing once a bridge
// settles (claimed with its claim record fetched), since a settled entry is never refreshed again.
func TestActivityCache_TimestampsFreezeOnceSettled(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// a single isClaimed/claimInfo entry: a second consultation would panic on out-of-range
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}

	cache := newTestActivityCache(scanner, claims)
	require.NoError(t, cache.RegisterAndAwait(testFromAddress, 0))
	t1 := time.Now()
	cache.now = func() time.Time { return t1 }

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.True(t, entries[0].UpdatedAt.Equal(t1))

	cache.now = func() time.Time { return t1.Add(time.Minute) }

	require.NoError(t, cache.RefreshAddress(t.Context(), testFromAddress))
	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.True(t, entries[0].UpdatedAt.Equal(t1), "a settled entry is never refreshed again")
}

// TestActivityCache_BridgeNetworkIDUsesScannedNetworkNotBridgeOriginNetwork verifies
// ActivityEntry.BridgeNetworkID reflects the network the bridge was scanned from (what the
// caller actually asked bridge_service for), not Bridge.OriginNetwork — which is the origin of
// the bridged ASSET and can differ when re-bridging an asset from a third network (see
// domain.ScannedBridge). It also verifies isClaimed()'s on-chain sourceBridgeNetwork argument and
// the tracker's TrackingID both use the scanned network, never Bridge.OriginNetwork.
func TestActivityCache_BridgeNetworkIDUsesScannedNetworkNotBridgeOriginNetwork(t *testing.T) {
	// OriginNetwork (99) is the asset-origin decoy, deliberately different from the network this
	// bridge was actually scanned from (5)
	const scannedNetworkID = uint32(7)
	bridge := testBridge(1)
	bridge.OriginNetwork = 99
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{scannedBridge(bridge, scannedNetworkID)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := refreshAndGet(t, cache, testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, scannedNetworkID, entries[0].BridgeNetworkID)
	require.Equal(t, scannedNetworkID, claims.lastIsClaimedNetworkID)
	require.Equal(t, scannedNetworkID, entries[0].Tracking.ID().NetworkID)
}
