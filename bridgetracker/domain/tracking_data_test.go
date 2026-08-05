package domain

import (
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/stretchr/testify/require"
)

// TestTrackingStatusDerivation pins the full derivation table of TrackingStatus: nothing is
// stored, the status follows from AllSteps once present, and from the tx-level facts until
// then
func TestTrackingStatusDerivation(t *testing.T) {
	t.Parallel()

	id := TrackingID{NetworkID: 1}
	info := &BridgeInfo{NetworkID: 1}

	testCases := []struct {
		name     string
		bridgeTx TrackingBridgeTx
		allSteps []BridgeStepPath
		expected types.TrackingStatus
	}{
		{
			name:     "fresh entry -> Registered",
			expected: types.TrackingStatusRegistered,
		},
		{
			name: "transient tx error still being retried -> Registered",
			bridgeTx: TrackingBridgeTx{
				Error: &types.ErrorStep{ErrorType: types.StepErrorTransient},
			},
			expected: types.TrackingStatusRegistered,
		},
		{
			name: "exhausted tx error -> Error (the tracker gave up)",
			bridgeTx: TrackingBridgeTx{
				Error: &types.ErrorStep{ErrorType: types.StepErrorExhausted},
			},
			expected: types.TrackingStatusError,
		},
		{
			name: "permanent tx error -> Error (not a bridge tx)",
			bridgeTx: TrackingBridgeTx{
				Error: &types.ErrorStep{ErrorType: types.StepErrorPermanent},
			},
			expected: types.TrackingStatusError,
		},
		{
			name:     "resolved bridge without steps yet -> Running",
			bridgeTx: TrackingBridgeTx{Info: info},
			expected: types.TrackingStatusRunning,
		},
		{
			name:     "steps rule once present: in progress -> Running",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepWaitingClaim, Status: types.StepStatusInProgress},
			},
			expected: types.TrackingStatusRunning,
		},
		{
			name:     "steps rule once present: claimed -> Finished",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepClaimed, Status: types.StepStatusDone},
			},
			expected: types.TrackingStatusFinished,
		},
		{
			name:     "steps rule once present: step in error -> Error",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepWaitingClaim, Status: types.StepStatusError},
			},
			expected: types.TrackingStatusError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tracking := NewTrackingData(id, tc.bridgeTx, tc.allSteps)
			require.Equal(t, tc.expected, tracking.TrackingStatus())
		})
	}

	t.Run("nil snapshot -> Error", func(t *testing.T) {
		t.Parallel()

		var tracking *TrackingData
		require.Equal(t, types.TrackingStatusError, tracking.TrackingStatus())
	})
}
