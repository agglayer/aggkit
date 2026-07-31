package domain

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/stretchr/testify/require"
)

func TestPendingPath(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	for _, bridgeType := range []types.BridgeType{
		types.BridgeTypeL1ToL2, types.BridgeTypeL2ToL1, types.BridgeTypeL2ToL2,
	} {
		t.Run(bridgeType.String(), func(t *testing.T) {
			t.Parallel()

			steps := PendingPath(bridgeType, now)

			require.Equal(t, ExpectedPath(bridgeType), stepsOf(steps),
				"the whole route must be visible from the start")
			require.Equal(t, types.StepStatusInProgress, steps[0].Status)
			require.Equal(t, &now, steps[0].StartDate)
			for _, sp := range steps[1:] {
				require.Equal(t, types.StepStatusPending, sp.Status)
				require.Nil(t, sp.StartDate)
			}
		})
	}
}

func stepsOf(paths []types.BridgeStepPath) []types.BridgeStep {
	steps := make([]types.BridgeStep, len(paths))
	for i, p := range paths {
		steps[i] = p.Step
	}
	return steps
}
