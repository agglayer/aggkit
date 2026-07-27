package domain

import (
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/stretchr/testify/require"
)

func TestExpectedPath(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		bridgeType types.BridgeType
		expected   []types.BridgeStep
	}{
		{
			name:       "L1 to L2",
			bridgeType: types.BridgeTypeL1ToL2,
			expected: []types.BridgeStep{
				types.StepWaitingGERUpdate,
				types.StepWaitingGERInjection,
				types.StepWaitingClaim,
				types.StepClaimed,
			},
		},
		{
			name:       "L2 to L1",
			bridgeType: types.BridgeTypeL2ToL1,
			expected: []types.BridgeStep{
				types.StepWaitingLERUpdate,
				types.StepPendingInclusion,
				types.StepCertificatePending,
				types.StepCertificateProcessing,
				types.StepWaitingClaim,
				types.StepClaimed,
			},
		},
		{
			name:       "L2 to L2",
			bridgeType: types.BridgeTypeL2ToL2,
			expected: []types.BridgeStep{
				types.StepWaitingLERUpdate,
				types.StepPendingInclusion,
				types.StepCertificatePending,
				types.StepCertificateProcessing,
				types.StepWaitingGERInjection,
				types.StepWaitingClaim,
				types.StepClaimed,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, ExpectedPath(tc.bridgeType))
		})
	}
}
