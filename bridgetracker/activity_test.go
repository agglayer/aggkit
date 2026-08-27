package bridgetracker

import (
	"context"
	"errors"
	"math/big"
	"testing"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var testFromAddress = common.HexToAddress("0x1111111111111111111111111111111111111111")

func testBridge(globalIndex int64) *bridgeservicetypes.BridgeResponse {
	return &bridgeservicetypes.BridgeResponse{
		OriginNetwork:      1,
		DestinationNetwork: 2,
		DepositCount:       uint32(globalIndex),
		GlobalIndex:        big.NewInt(globalIndex),
		TxHash:             bridgeservicetypes.Hash("0xtx"),
	}
}

// fakeActivityScanner is a hand-rolled ActivityBridgeScanner for tests: bridges is returned on
// every BridgesFrom call, and calls records how many times it was invoked.
type fakeActivityScanner struct {
	bridges []*bridgeservicetypes.BridgeResponse
	err     error
	calls   int
}

func (f *fakeActivityScanner) BridgesFrom(
	context.Context, common.Address,
) ([]*bridgeservicetypes.BridgeResponse, error) {
	f.calls++
	return f.bridges, f.err
}

// fakeActivityClaims is a hand-rolled ActivityClaimChecker for tests: isClaimed/claimInfo are
// consulted in FIFO order per call, one entry per expected IsClaimed/ClaimInfo invocation, so a
// test can assert exactly how many times each was called (and fail loudly if called more).
// isClaimedErrs, if non-nil, is consulted alongside isClaimed: a non-nil entry makes that call
// fail instead of returning the paired isClaimed value.
type fakeActivityClaims struct {
	isClaimed      []bool
	isClaimedErrs  []error
	isClaimedCalls int
	claimInfo      []*bridgeservicetypes.ClaimResponse
	claimInfoCalls int
}

func (f *fakeActivityClaims) IsClaimed(context.Context, *bridgeservicetypes.BridgeResponse) (bool, error) {
	i := f.isClaimedCalls
	f.isClaimedCalls++
	if i < len(f.isClaimedErrs) && f.isClaimedErrs[i] != nil {
		return false, f.isClaimedErrs[i]
	}
	return f.isClaimed[i], nil
}

func (f *fakeActivityClaims) ClaimInfo(
	context.Context, *bridgeservicetypes.BridgeResponse,
) (*bridgeservicetypes.ClaimResponse, error) {
	claim := f.claimInfo[f.claimInfoCalls]
	f.claimInfoCalls++
	return claim, nil
}

func newTestActivityCache(scanner ActivityBridgeScanner, claims ActivityClaimChecker) *ActivityCache {
	supervised := NewMemoryRegistry(10)
	return NewActivityCache(scanner, claims, supervised, log.WithFields("module", "activity_test"))
}

// TestActivityCache_UnclaimedBridgeIsRecheckedEveryCall verifies an unclaimed bridge's claim
// state is re-verified on every GetActivity call, and that includeTracking=false never
// registers it with the tracker.
func TestActivityCache_UnclaimedBridgeIsRecheckedEveryCall(t *testing.T) {
	bridge := testBridge(1)
	scanner := &fakeActivityScanner{bridges: []*bridgeservicetypes.BridgeResponse{bridge}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}

	cache := newTestActivityCache(scanner, claims)

	for range 2 {
		entries, err := cache.GetActivity(t.Context(), testFromAddress, false)
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
// snapshot.
func TestActivityCache_IncludeTrackingRegistersUnclaimedBridge(t *testing.T) {
	bridge := testBridge(1)
	scanner := &fakeActivityScanner{bridges: []*bridgeservicetypes.BridgeResponse{bridge}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}

	cache := newTestActivityCache(scanner, claims)

	entries, err := cache.GetActivity(t.Context(), testFromAddress, true)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.NotNil(t, entries[0].Tracking)

	wantID := domain.TrackingID{NetworkID: bridge.OriginNetwork, TxHash: common.HexToHash(string(bridge.TxHash))}
	require.Equal(t, wantID, entries[0].Tracking.ID())
}

// TestActivityCache_ClaimedAndIndexedBridgeIsNeverRechecked verifies a bridge that is claimed
// with its claim record already fetched is never rechecked on a later call.
func TestActivityCache_ClaimedAndIndexedBridgeIsNeverRechecked(t *testing.T) {
	bridge := testBridge(1)
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*bridgeservicetypes.BridgeResponse{bridge}}
	// only one IsClaimed/ClaimInfo entry: a second consultation would panic on out-of-range
	claims := &fakeActivityClaims{isClaimed: []bool{true}, claimInfo: []*bridgeservicetypes.ClaimResponse{claim}}

	cache := newTestActivityCache(scanner, claims)

	entries, err := cache.GetActivity(t.Context(), testFromAddress, false)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)

	entries, err = cache.GetActivity(t.Context(), testFromAddress, false)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls)
	require.Equal(t, 1, claims.claimInfoCalls)
	require.Equal(t, 2, scanner.calls) // BridgesFrom is still called every time to find new bridges
}

// TestActivityCache_ClaimedButNotYetIndexedBridgeIsRetried verifies a bridge reported as
// claimed on-chain, but whose claim record the destination bridge service has not indexed yet
// (ClaimInfo returns nil), is retried on the next call.
func TestActivityCache_ClaimedButNotYetIndexedBridgeIsRetried(t *testing.T) {
	bridge := testBridge(1)
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*bridgeservicetypes.BridgeResponse{bridge}}
	claims := &fakeActivityClaims{
		isClaimed: []bool{true, true},
		claimInfo: []*bridgeservicetypes.ClaimResponse{nil, claim},
	}

	cache := newTestActivityCache(scanner, claims)

	entries, err := cache.GetActivity(t.Context(), testFromAddress, false)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Nil(t, entries[0].Claim)

	entries, err = cache.GetActivity(t.Context(), testFromAddress, false)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusClaimed, entries[0].ClaimStatus)
	require.Equal(t, claim, entries[0].Claim)
}

// TestActivityCache_ScannerErrorFailsTheCall verifies a scanner failure fails GetActivity
// entirely.
func TestActivityCache_ScannerErrorFailsTheCall(t *testing.T) {
	wantErr := errors.New("bridge service unreachable")
	scanner := &fakeActivityScanner{err: wantErr}
	cache := newTestActivityCache(scanner, &fakeActivityClaims{})

	_, err := cache.GetActivity(t.Context(), testFromAddress, false)
	require.ErrorIs(t, err, wantErr)
}

// TestActivityCache_IsClaimedFailureReportsErrorStatus verifies a failed isClaimed() check
// (e.g. no bridge contract address configured for the destination network) is reported as
// ClaimStatusError — never silently as ClaimStatusUnclaimed — and is retried on the next call.
func TestActivityCache_IsClaimedFailureReportsErrorStatus(t *testing.T) {
	bridge := testBridge(1)
	scanner := &fakeActivityScanner{bridges: []*bridgeservicetypes.BridgeResponse{bridge}}
	claims := &fakeActivityClaims{
		isClaimed:     []bool{false, false},
		isClaimedErrs: []error{errors.New("no bridge contract address configured for network 2"), nil},
	}

	cache := newTestActivityCache(scanner, claims)

	entries, err := cache.GetActivity(t.Context(), testFromAddress, false)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusError, entries[0].ClaimStatus)
	require.Nil(t, entries[0].Claim)

	// the error state is not settled: it is retried on the next call
	entries, err = cache.GetActivity(t.Context(), testFromAddress, false)
	require.NoError(t, err)
	require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
	require.Equal(t, 2, claims.isClaimedCalls)
}
