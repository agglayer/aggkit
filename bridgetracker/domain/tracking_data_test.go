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
		{
			name:     "steps rule once present: step in transient error -> Running (still retrying)",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{
					Step:   types.StepWaitingClaim,
					Status: types.StepStatusError,
					Error:  &types.ErrorStep{ErrorType: types.StepErrorTransient},
				},
			},
			expected: types.TrackingStatusRunning,
		},
		{
			name:     "steps rule once present: step in exhausted error -> Error (gave up retrying)",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{
					Step:   types.StepWaitingClaim,
					Status: types.StepStatusError,
					Error:  &types.ErrorStep{ErrorType: types.StepErrorExhausted},
				},
			},
			expected: types.TrackingStatusError,
		},
		{
			name:     "steps rule once present: step in permanent error -> Error",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{
					Step:   types.StepWaitingClaim,
					Status: types.StepStatusError,
					Error:  &types.ErrorStep{ErrorType: types.StepErrorPermanent},
				},
			},
			expected: types.TrackingStatusError,
		},
		{
			name:     "an earlier step's transient error does not hide a later terminal one -> Error",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{
					Step:   types.StepWaitingGERUpdate,
					Status: types.StepStatusError,
					Error:  &types.ErrorStep{ErrorType: types.StepErrorTransient},
				},
				{
					Step:   types.StepWaitingClaim,
					Status: types.StepStatusError,
					Error:  &types.ErrorStep{ErrorType: types.StepErrorPermanent},
				},
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

// TestClaimStatusDerivation pins the full derivation table of ClaimStatus: TrackingStatusError
// always wins (whatever produced it), otherwise it follows the current step — Pending unless
// that step is StepWaitingClaim (ReadyToClaim) or StepClaimed (Claimed)
func TestClaimStatusDerivation(t *testing.T) {
	t.Parallel()

	id := TrackingID{NetworkID: 1}
	info := &BridgeInfo{NetworkID: 1}

	testCases := []struct {
		name     string
		bridgeTx TrackingBridgeTx
		allSteps []BridgeStepPath
		expected types.TrackerClaimStatus
	}{
		{
			name:     "fresh entry -> Pending",
			expected: types.TrackerClaimStatusPending,
		},
		{
			name: "transient tx error still being retried -> Pending",
			bridgeTx: TrackingBridgeTx{
				Error: &types.ErrorStep{ErrorType: types.StepErrorTransient},
			},
			expected: types.TrackerClaimStatusPending,
		},
		{
			name: "exhausted tx error -> Error (the tracker gave up)",
			bridgeTx: TrackingBridgeTx{
				Error: &types.ErrorStep{ErrorType: types.StepErrorExhausted},
			},
			expected: types.TrackerClaimStatusError,
		},
		{
			name: "permanent tx error -> Error (not a bridge tx)",
			bridgeTx: TrackingBridgeTx{
				Error: &types.ErrorStep{ErrorType: types.StepErrorPermanent},
			},
			expected: types.TrackerClaimStatusError,
		},
		{
			name:     "resolved bridge without steps yet -> Pending",
			bridgeTx: TrackingBridgeTx{Info: info},
			expected: types.TrackerClaimStatusPending,
		},
		{
			name:     "in progress on an earlier step -> Pending",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepWaitingLERUpdate, Status: types.StepStatusInProgress},
			},
			expected: types.TrackerClaimStatusPending,
		},
		{
			name:     "in progress on WaitingClaim -> ReadyToClaim",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepWaitingClaim, Status: types.StepStatusInProgress},
			},
			expected: types.TrackerClaimStatusReadyToClaim,
		},
		{
			name:     "claimed -> Claimed",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepClaimed, Status: types.StepStatusDone},
			},
			expected: types.TrackerClaimStatusClaimed,
		},
		{
			name:     "step in error -> Error, even if that step is WaitingClaim",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepWaitingClaim, Status: types.StepStatusError},
			},
			expected: types.TrackerClaimStatusError,
		},
		{
			name:     "transient error on WaitingClaim -> ReadyToClaim (still retrying)",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{
					Step:   types.StepWaitingClaim,
					Status: types.StepStatusError,
					Error:  &types.ErrorStep{ErrorType: types.StepErrorTransient},
				},
			},
			expected: types.TrackerClaimStatusReadyToClaim,
		},
		{
			name:     "transient error on an earlier step -> Pending (still retrying)",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{
					Step:   types.StepWaitingLERUpdate,
					Status: types.StepStatusError,
					Error:  &types.ErrorStep{ErrorType: types.StepErrorTransient},
				},
			},
			expected: types.TrackerClaimStatusPending,
		},
		{
			name:     "claimed step pending, nothing in progress -> Pending (not finished yet)",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepWaitingClaim, Status: types.StepStatusDone},
				{Step: types.StepClaimed, Status: types.StepStatusPending},
			},
			expected: types.TrackerClaimStatusPending,
		},
		{
			name:     "done steps followed by pending ones, nothing in progress -> Pending (torn read)",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepWaitingLERUpdate, Status: types.StepStatusDone},
				{Step: types.StepPendingInclusion, Status: types.StepStatusDone},
				{Step: types.StepCertificatePending, Status: types.StepStatusPending},
				{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
				{Step: types.StepClaimed, Status: types.StepStatusPending},
			},
			expected: types.TrackerClaimStatusPending,
		},
		{
			name:     "only WaitingClaim left pending, nothing in progress -> ReadyToClaim",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepWaitingL1InfoLeafAvailable, Status: types.StepStatusDone},
				{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
				{Step: types.StepClaimed, Status: types.StepStatusPending},
			},
			expected: types.TrackerClaimStatusReadyToClaim,
		},
		{
			name:     "intermediate steps skipped, claimed done -> Claimed",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{Step: types.StepWaitingLERUpdate, Status: types.StepStatusDone},
				{Step: types.StepPendingInclusion, Status: types.StepStatusSkipped},
				{Step: types.StepClaimed, Status: types.StepStatusDone},
			},
			expected: types.TrackerClaimStatusClaimed,
		},
		{
			name:     "exhausted error on WaitingClaim -> Error (gave up retrying)",
			bridgeTx: TrackingBridgeTx{Info: info},
			allSteps: []BridgeStepPath{
				{
					Step:   types.StepWaitingClaim,
					Status: types.StepStatusError,
					Error:  &types.ErrorStep{ErrorType: types.StepErrorExhausted},
				},
			},
			expected: types.TrackerClaimStatusError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tracking := NewTrackingData(id, tc.bridgeTx, tc.allSteps)
			require.Equal(t, tc.expected, tracking.ClaimStatus())
		})
	}

	t.Run("nil snapshot -> Error", func(t *testing.T) {
		t.Parallel()

		var tracking *TrackingData
		require.Equal(t, types.TrackerClaimStatusError, tracking.ClaimStatus())
	})
}

// TestStepIndexPicksFirstPendingWhenNothingInProgress pins that a snapshot with done steps
// followed by pending ones (and nothing in progress, e.g. read mid-way through a multi-step
// store write) points at the first pending step and reads as Running, never at the last
// (Claimed) step as if the bridge were finished
func TestStepIndexPicksFirstPendingWhenNothingInProgress(t *testing.T) {
	t.Parallel()

	path := ExpectedPath(types.BridgeTypeL2ToL2)
	allSteps := make([]BridgeStepPath, len(path))
	for i, s := range path {
		allSteps[i] = BridgeStepPath{Step: s, Status: types.StepStatusPending}
	}
	allSteps[0].Status = types.StepStatusDone
	allSteps[1].Status = types.StepStatusDone

	tracking := NewTrackingData(TrackingID{NetworkID: 1}, TrackingBridgeTx{Info: &BridgeInfo{NetworkID: 1}}, allSteps)
	require.NotNil(t, tracking.StepIndex())
	require.Equal(t, 2, *tracking.StepIndex())
	require.Equal(t, types.TrackingStatusRunning, tracking.TrackingStatus())
	require.NotEqual(t, types.TrackerClaimStatusClaimed, tracking.ClaimStatus())

	for i := range allSteps {
		allSteps[i].Status = types.StepStatusDone
	}
	finished := NewTrackingData(TrackingID{NetworkID: 1}, TrackingBridgeTx{Info: &BridgeInfo{NetworkID: 1}}, allSteps)
	require.Equal(t, len(allSteps)-1, *finished.StepIndex(), "every step done -> the last one (Claimed)")
	require.Equal(t, types.TrackingStatusFinished, finished.TrackingStatus())
	require.Equal(t, types.TrackerClaimStatusClaimed, finished.ClaimStatus())
}
