package api

import (
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/stretchr/testify/require"
)

func TestWSTerminalReason(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		resolved       bool
		txError        *types.ErrorStep
		steps          []domain.BridgeStepPath
		expectedReason string
		expectedDone   bool
	}{
		{name: "registered"},
		{
			name:    "transient tx error",
			txError: &types.ErrorStep{ErrorType: types.StepErrorTransient},
		},
		{
			name:           "permanent tx error",
			txError:        &types.ErrorStep{ErrorType: types.StepErrorPermanent},
			expectedReason: "tracker gave up resolving the bridge",
			expectedDone:   true,
		},
		{
			name:           "exhausted tx error",
			txError:        &types.ErrorStep{ErrorType: types.StepErrorExhausted},
			expectedReason: "tracker gave up resolving the bridge",
			expectedDone:   true,
		},
		{
			name:     "step in progress",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepWaitingClaim, Status: types.StepStatusInProgress,
			}},
		},
		{
			name:     "transient step error",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepWaitL1SettledGER, Status: types.StepStatusError,
				Error: &types.ErrorStep{ErrorType: types.StepErrorTransient},
			}},
		},
		{
			name:     "permanent step error",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepWaitL1SettledGER, Status: types.StepStatusError,
				Error: &types.ErrorStep{ErrorType: types.StepErrorPermanent},
			}},
			expectedReason: "tracker gave up resolving the bridge",
			expectedDone:   true,
		},
		{
			name:     "exhausted step error",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepWaitL1SettledGER, Status: types.StepStatusError,
				Error: &types.ErrorStep{ErrorType: types.StepErrorExhausted},
			}},
			expectedReason: "tracker gave up resolving the bridge",
			expectedDone:   true,
		},
		{
			name:     "step error without details",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepWaitL1SettledGER, Status: types.StepStatusError,
			}},
			expectedReason: "tracker gave up resolving the bridge",
			expectedDone:   true,
		},
		{
			name:     "claimed",
			resolved: true,
			steps: []domain.BridgeStepPath{{
				Step: types.StepClaimed, Status: types.StepStatusDone,
			}},
			expectedReason: "bridge claimed",
			expectedDone:   true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			bridgeTx := domain.TrackingBridgeTx{Error: testCase.txError}
			if testCase.resolved {
				bridgeTx.Info = &domain.BridgeInfo{NetworkID: 1}
			}
			tracking := domain.NewTrackingData(domain.TrackingID{}, bridgeTx, testCase.steps)

			reason, done := wsTerminalReason(tracking)
			require.Equal(t, testCase.expectedDone, done)
			require.Equal(t, testCase.expectedReason, reason)
		})
	}
}
