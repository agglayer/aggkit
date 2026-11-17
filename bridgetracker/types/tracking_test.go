package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrackingStatusString(t *testing.T) {
	require.Equal(t, "registered", TrackingStatusRegistered.String())
	require.Equal(t, "running", TrackingStatusRunning.String())
	require.Equal(t, "error", TrackingStatusError.String())
	require.Equal(t, "finished", TrackingStatusFinished.String())
	require.Equal(t, "Unknown(99)", TrackingStatus(99).String())
}

func TestTrackerClaimStatusString(t *testing.T) {
	require.Equal(t, "pending", TrackerClaimStatusPending.String())
	require.Equal(t, "readyToClaim", TrackerClaimStatusReadyToClaim.String())
	require.Equal(t, "claimed", TrackerClaimStatusClaimed.String())
	require.Equal(t, "error", TrackerClaimStatusError.String())
	require.Equal(t, "claimed_finalized", TrackerClaimStatusClaimedFinalized.String())
	require.Equal(t, "Unknown(99)", TrackerClaimStatus(99).String())
}

// TestTrackerClaimStatusNumericValuesAreStable pins the numeric values of the statuses that
// existed before "claimed_finalized": the new one is appended, so none of them moved
func TestTrackerClaimStatusNumericValuesAreStable(t *testing.T) {
	require.Equal(t, 0, int(TrackerClaimStatusPending))
	require.Equal(t, 1, int(TrackerClaimStatusReadyToClaim))
	require.Equal(t, 2, int(TrackerClaimStatusClaimed))
	require.Equal(t, 3, int(TrackerClaimStatusError))
	require.Equal(t, 4, int(TrackerClaimStatusClaimedFinalized))
}

// TestParseTrackerClaimStatusRoundTrip verifies every status parses back from its own String(),
// including the new "claimed_finalized", and that anything else is rejected
func TestParseTrackerClaimStatusRoundTrip(t *testing.T) {
	for _, status := range []TrackerClaimStatus{
		TrackerClaimStatusPending, TrackerClaimStatusReadyToClaim, TrackerClaimStatusClaimed,
		TrackerClaimStatusError, TrackerClaimStatusClaimedFinalized,
	} {
		parsed, err := ParseTrackerClaimStatus(status.String())
		require.NoError(t, err, status.String())
		require.Equal(t, status, parsed)
	}

	for _, invalid := range []string{"", "claimedFinalized", "CLAIMED", TrackerClaimStatus(99).String()} {
		_, err := ParseTrackerClaimStatus(invalid)
		require.Error(t, err, invalid)
	}
}

// TestTrackerClaimStatusIsClaimed verifies both claimed values — final or not — count as claimed
// and nothing else does
func TestTrackerClaimStatusIsClaimed(t *testing.T) {
	require.True(t, TrackerClaimStatusClaimed.IsClaimed())
	require.True(t, TrackerClaimStatusClaimedFinalized.IsClaimed())
	require.False(t, TrackerClaimStatusPending.IsClaimed())
	require.False(t, TrackerClaimStatusReadyToClaim.IsClaimed())
	require.False(t, TrackerClaimStatusError.IsClaimed())
}
