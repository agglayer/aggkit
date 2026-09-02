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
		GlobalIndex:        big.NewInt(globalIndex),
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
// bridges is not in known, mirroring ActivitySource.BridgesFrom's real contract. calls records
// how many times it was invoked, lastKnown the known argument it was last called with.
type fakeActivityScanner struct {
	bridges   []*domain.ScannedBridge
	err       error
	warnings  []domain.ActivityWarning
	calls     int
	lastKnown map[string]struct{}
}

func (f *fakeActivityScanner) BridgesFrom(
	_ context.Context, _ common.Address, known map[string]struct{},
) ([]*domain.ScannedBridge, []domain.ActivityWarning, error) {
	f.calls++
	f.lastKnown = known
	if f.err != nil {
		return nil, nil, f.err
	}
	out := make([]*domain.ScannedBridge, 0, len(f.bridges))
	for _, b := range f.bridges {
		if _, ok := known[b.Bridge.GlobalIndex.String()]; ok {
			continue
		}
		out = append(out, b)
	}
	return out, f.warnings, nil
}

// fakeActivityClaims is a hand-rolled ActivityClaimChecker for tests: isClaimed/claimInfo are
// consulted in FIFO order per call, one entry per expected IsClaimed/ClaimInfo invocation, so a
// test can assert exactly how many times each was called (and fail loudly if called more).
// isClaimedErrs, if non-nil, is consulted alongside isClaimed: a non-nil entry makes that call
// fail instead of returning the paired isClaimed value. lastIsClaimedNetworkID records the
// NetworkID of the last ScannedBridge IsClaimed was called with.
type fakeActivityClaims struct {
	isClaimed              []bool
	isClaimedErrs          []error
	isClaimedCalls         int
	lastIsClaimedNetworkID uint32
	claimInfo              []*bridgeservicetypes.ClaimResponse
	claimInfoCalls         int
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

// newTestActivityCache builds an ActivityCache with a one-hour idle timeout, long enough that
// no test below evicts anything by accident; tests exercising eviction build their own directly.
func newTestActivityCache(scanner ActivityBridgeScanner, claims ActivityClaimChecker) *ActivityCache {
	supervised := NewMemoryRegistry(10)
	return NewActivityCache(scanner, claims, supervised, log.WithFields("module", "activity_test"), time.Hour)
}

// TestActivityCache_UnclaimedBridgeIsRecheckedEveryCall verifies an unclaimed bridge's claim
// state is re-verified on every GetActivity call, and that includeTracking=false never
// registers it with the tracker.
func TestActivityCache_UnclaimedBridgeIsRecheckedEveryCall(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}

	cache := newTestActivityCache(scanner, claims)

	for range 2 {
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

// TestActivityCache_IncludeTrackingRegistersUnclaimedBridge verifies includeTracking=true
// registers a still-unclaimed bridge with the supervised store (register-only) and reports its
// snapshot, keyed by the scanned network — not Bridge.OriginNetwork.
func TestActivityCache_IncludeTrackingRegistersUnclaimedBridge(t *testing.T) {
	bridge := testBridge(1)
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{scannedBridge(bridge, testScannedNetworkID)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.NotNil(t, entries[0].Tracking)

	wantID := domain.TrackingID{NetworkID: testScannedNetworkID, TxHash: common.HexToHash(string(bridge.TxHash))}
	require.Equal(t, wantID, entries[0].Tracking.ID())
}

// TestActivityCache_ClaimedAndIndexedBridgeIsNeverRechecked verifies a bridge that is claimed
// with its claim record already fetched is never rechecked on a later call.
func TestActivityCache_ClaimedAndIndexedBridgeIsNeverRechecked(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// only one IsClaimed/ClaimInfo entry: a second consultation would panic on out-of-range
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)

	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls)
	require.Equal(t, 1, claims.claimInfoCalls)
	require.Equal(t, 2, scanner.calls) // BridgesFrom is still called every time to find new bridges
}

// TestActivityCache_ClaimedButNotYetIndexedBridgeIsRetried verifies a bridge reported as claimed
// on-chain, but whose claim record the destination bridge service has not indexed yet (ClaimInfo
// returns nil), has its claim record retried on the next call — without asking isClaimed() again,
// since a confirmed claim never reverts (see ActivityCache.refresh).
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

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Nil(t, entries[0].Claim)

	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls, "isClaimed must not be asked again once confirmed claimed")
	require.Equal(t, 2, claims.claimInfoCalls)
}

// TestActivityCache_ScannerErrorFailsTheCall verifies a scanner failure fails GetActivity
// entirely.
func TestActivityCache_ScannerErrorFailsTheCall(t *testing.T) {
	wantErr := errors.New("bridge service unreachable")
	scanner := &fakeActivityScanner{err: wantErr}
	cache := newTestActivityCache(scanner, &fakeActivityClaims{})

	_, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.ErrorIs(t, err, wantErr)
}

// TestActivityCache_IsClaimedFailureReportsErrorStatus verifies a failed isClaimed() check
// (e.g. no bridge contract address configured for the destination network) is reported as
// ClaimStatusError — never silently as ClaimStatusUnclaimed — and is retried on the next call
// (unlike a confirmed claim, an error is not permanent).
func TestActivityCache_IsClaimedFailureReportsErrorStatus(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{false, false},
		isClaimedErrs: []error{errors.New("no bridge contract address configured for network 2"), nil},
	}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusError, entries[0].ClaimStatus)
	require.Nil(t, entries[0].Claim)
	require.Equal(t, "no bridge contract address configured for network 2", entries[0].Errors["claim"])

	// the error state is not settled: it is retried on the next call
	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.Equal(t, 2, claims.isClaimedCalls)
	require.Nil(t, entries[0].Errors, "a successful recheck must not carry over the previous failure")
}

// TestActivityCache_FilterPendingExcludesClaimedAndErroredAndSkipsClaimInfo verifies
// ActivityFilterPending returns only confirmed-unclaimed bridges — excluding both claimed ones
// and ones whose isClaimed() check errored — and never fetches a claimed bridge's claim record
// (only IsClaimed is consulted, never ClaimInfo).
func TestActivityCache_FilterPendingExcludesClaimedAndErroredAndSkipsClaimInfo(t *testing.T) {
	pendingBridge := testScannedBridge(2)
	scanner := &fakeActivityScanner{
		bridges: []*domain.ScannedBridge{testScannedBridge(1), pendingBridge, testScannedBridge(3)},
	}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{true, false, false}, // no claimInfo entries: ClaimInfo must not be called
		isClaimedErrs: []error{nil, nil, errors.New("boom")},
	}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterPending)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, pendingBridge.Bridge, entries[0].Bridge)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.Equal(t, 0, claims.claimInfoCalls)
}

// TestActivityCache_FilterErrorReturnsOnlyErroredAndSkipsClaimInfo verifies ActivityFilterError
// returns only bridges whose isClaimed() check failed, excludes claimed and pending ones, and
// never fetches a claimed bridge's claim record for this filter either.
func TestActivityCache_FilterErrorReturnsOnlyErroredAndSkipsClaimInfo(t *testing.T) {
	erroredBridge := testScannedBridge(3)
	wantErr := errors.New("boom")
	scanner := &fakeActivityScanner{
		bridges: []*domain.ScannedBridge{testScannedBridge(1), testScannedBridge(2), erroredBridge},
	}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{true, false, false}, // no claimInfo entries: ClaimInfo must not be called
		isClaimedErrs: []error{nil, nil, wantErr},
	}

	cache := newTestActivityCache(scanner, claims)

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterError)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, erroredBridge.Bridge, entries[0].Bridge)
	require.Equal(t, types.ClaimStatusError, entries[0].ClaimStatus)
	require.Equal(t, wantErr.Error(), entries[0].Errors["claim"])
	require.Equal(t, 0, claims.claimInfoCalls)
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

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterClaimed)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, claimedBridge.Bridge, entries[0].Bridge)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)
}

// TestActivityCache_PendingBridgeSkippedThenFetchedOnceFilterAllIsUsed verifies a bridge left
// unsettled by ActivityFilterPending (claimed, but its claim record deliberately not fetched)
// gets its claim record fetched normally the next time ActivityFilterAll is used — without
// isClaimed() being asked again, since it was already confirmed claimed.
func TestActivityCache_PendingBridgeSkippedThenFetchedOnceFilterAllIsUsed(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// a single isClaimed entry: a second consultation would panic on out-of-range
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}

	cache := newTestActivityCache(scanner, claims)

	// filterBridges=pending: claimed, but its claim record is deliberately not fetched, and the
	// bridge itself is excluded from this result
	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterPending)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.Equal(t, 0, claims.claimInfoCalls)

	// filterBridges=all: the still-unsettled entry is rechecked — its claim record is fetched,
	// but isClaimed() is not asked again
	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls)
	require.Equal(t, 1, claims.claimInfoCalls)
}

// TestActivityCache_ScannerReceivesGrowingKnownSet verifies the scanner is called with an empty
// known set the first time (nothing cached yet), and with the previously found bridge's key once
// it has been cached.
func TestActivityCache_ScannerReceivesGrowingKnownSet(t *testing.T) {
	bridge := testScannedBridge(1)
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{bridge}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}

	cache := newTestActivityCache(scanner, claims)

	_, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Empty(t, scanner.lastKnown, "nothing cached yet on the first call")

	_, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Contains(t, scanner.lastKnown, bridge.Bridge.GlobalIndex.String())
}

// TestActivityCache_IdleAddressIsForgotten verifies an address untouched for longer than
// idleTimeout is forgotten entirely: proven indirectly by observing isClaimed() being asked
// again for a bridge that had already settled — which would not happen if its cached state had
// survived.
func TestActivityCache_IdleAddressIsForgotten(t *testing.T) {
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

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls, "settled after the first call")

	now = now.Add(2 * time.Minute) // past idleTimeout

	entries, _, err = cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 2, claims.isClaimedCalls, "the address was forgotten, so isClaimed is asked again from scratch")
}

// TestActivityCache_TimestampsTrackCreationAndLastUpdate verifies CreatedAt is stamped once and
// never changes, while UpdatedAt advances on every recheck of a still-unsettled entry.
func TestActivityCache_TimestampsTrackCreationAndLastUpdate(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}

	cache := newTestActivityCache(scanner, claims)
	t1 := time.Now()
	cache.now = func() time.Time { return t1 }

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.True(t, entries[0].CreatedAt.Equal(t1))
	require.True(t, entries[0].UpdatedAt.Equal(t1))

	t2 := t1.Add(time.Minute)
	cache.now = func() time.Time { return t2 }

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
	t1 := time.Now()
	cache.now = func() time.Time { return t1 }

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.True(t, entries[0].UpdatedAt.Equal(t1))

	cache.now = func() time.Time { return t1.Add(time.Minute) }

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

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, true, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, scannedNetworkID, entries[0].BridgeNetworkID)
	require.Equal(t, scannedNetworkID, claims.lastIsClaimedNetworkID)
	require.Equal(t, scannedNetworkID, entries[0].Tracking.ID().NetworkID)
}
