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

			steps := PendingPath(bridgeType, &BridgeInfo{}, nil, now)

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

// TestPendingPathStartDateFromResolver pins that the first step's StartDate prefers the
// deterministic value its own resolver can derive from info (the origin deposit's own block
// timestamp — see WaitingGERUpdateResolver/WaitingLERUpdateResolver) over now, the moment the
// tracker happened to notice the bridge resolve rather than the moment it was actually created
// (agglayer/aggkit#1840)
func TestPendingPathStartDateFromResolver(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	depositedAt := uint64(1700000400)
	info := &BridgeInfo{BlockTimestamp: depositedAt}
	resolvers := testResolvers(&fakeFacts{})

	for _, bridgeType := range []types.BridgeType{
		types.BridgeTypeL1ToL2, types.BridgeTypeL2ToL1, types.BridgeTypeL2ToL2,
	} {
		t.Run(bridgeType.String(), func(t *testing.T) {
			t.Parallel()

			steps := PendingPath(bridgeType, info, resolvers, now)

			require.Equal(t, blockTime(depositedAt), steps[0].StartDate)
		})
	}
}

// TestPendingPathStartDateFallsBackToNow pins that a nil/incomplete resolvers map, or a
// resolver whose own StartDate returns nil (e.g. info carries no deposit block timestamp),
// leaves the first step's StartDate at now, exactly as before per-step deterministic dates
// existed
func TestPendingPathStartDateFallsBackToNow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	steps := PendingPath(types.BridgeTypeL1ToL2, &BridgeInfo{}, testResolvers(&fakeFacts{}), now)

	require.Equal(t, &now, steps[0].StartDate, "BlockTimestamp is zero, so its resolver returns nil")
}

func stepsOf(paths []BridgeStepPath) []types.BridgeStep {
	steps := make([]types.BridgeStep, len(paths))
	for i, p := range paths {
		steps[i] = p.Step
	}
	return steps
}
