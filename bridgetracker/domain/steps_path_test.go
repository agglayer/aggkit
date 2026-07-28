package domain

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestBuildSteps(t *testing.T) {
	t.Parallel()

	t1 := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	t.Run("fresh bridge mid-path", func(t *testing.T) {
		t.Parallel()

		steps := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{Step: types.StepWaitingGERInjection}, nil, t1)

		require.Equal(t, []types.BridgeStepPath{
			{Step: types.StepWaitingGERUpdate, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepWaitingGERInjection, Status: types.StepStatusInProgress, StartDate: &t1},
			{Step: types.StepWaitingClaim, Status: types.StepStatusPending},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, steps)
	})

	t.Run("unchanged bridge produces an identical result", func(t *testing.T) {
		t.Parallel()

		res := StepResolution{Step: types.StepWaitingGERInjection}
		first := BuildSteps(types.BridgeTypeL1ToL2, res, nil, t1)

		second := BuildSteps(types.BridgeTypeL1ToL2, res, first, t2)

		require.Equal(t, first, second)
	})

	t.Run("advancing a step closes the previous one and keeps its dates", func(t *testing.T) {
		t.Parallel()

		first := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{Step: types.StepWaitingGERInjection}, nil, t1)

		steps := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{Step: types.StepWaitingClaim}, first, t2)

		require.Equal(t, []types.BridgeStepPath{
			{Step: types.StepWaitingGERUpdate, Status: types.StepStatusDone, EndDate: &t1},
			{Step: types.StepWaitingGERInjection, Status: types.StepStatusDone, StartDate: &t1, EndDate: &t2},
			{Step: types.StepWaitingClaim, Status: types.StepStatusInProgress, StartDate: &t2},
			{Step: types.StepClaimed, Status: types.StepStatusPending},
		}, steps)
	})

	t.Run("terminal step completes the moment it is reached", func(t *testing.T) {
		t.Parallel()

		steps := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{Step: types.StepClaimed}, nil, t1)

		last := steps[len(steps)-1]
		require.Equal(t, types.StepClaimed, last.Step)
		require.Equal(t, types.StepStatusDone, last.Status)
		require.Equal(t, &t1, last.StartDate)
		require.Equal(t, &t1, last.EndDate)
	})

	t.Run("certificate processing is a fixed step in the L2-origin path", func(t *testing.T) {
		t.Parallel()

		steps := BuildSteps(types.BridgeTypeL2ToL1, StepResolution{Step: types.StepCertificateProcessing}, nil, t1)

		require.Equal(t, []types.BridgeStep{
			types.StepWaitingLERUpdate,
			types.StepPendingInclusion,
			types.StepCertificatePending,
			types.StepCertificateProcessing,
			types.StepWaitingClaim,
			types.StepClaimed,
		}, stepsOf(steps))
		require.Equal(t, types.StepStatusInProgress, steps[3].Status)
	})

	t.Run("GER update result attaches to its step once resolved", func(t *testing.T) {
		t.Parallel()

		gerUpdate := &types.GERUpdateResult{GER: common.Hash{1}, BlockNumber: 100}
		steps := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{
			Step:      types.StepWaitingGERInjection,
			GERUpdate: gerUpdate,
		}, nil, t1)

		require.Equal(t, gerUpdate, steps[0].Result)
		require.Nil(t, steps[1].Result)
	})

	t.Run("result is carried over once produced, even after the resolution stops reporting it", func(t *testing.T) {
		t.Parallel()

		gerUpdate := &types.GERUpdateResult{GER: common.Hash{1}, BlockNumber: 100}
		first := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{
			Step:      types.StepWaitingGERInjection,
			GERUpdate: gerUpdate,
		}, nil, t1)

		second := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{Step: types.StepWaitingClaim}, first, t2)

		require.Equal(t, gerUpdate, second[0].Result)
	})

	t.Run("injected GER result attaches to WaitingGERInjection once resolved", func(t *testing.T) {
		t.Parallel()

		injectedGER := &types.InjectedGERResult{GER: common.Hash{3}}
		steps := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{
			Step:        types.StepWaitingClaim,
			InjectedGER: injectedGER,
		}, nil, t1)

		require.Equal(t, injectedGER, steps[1].Result)
	})

	t.Run("claim result attaches to WaitingClaim once claimed", func(t *testing.T) {
		t.Parallel()

		claim := &types.ClaimResult{ClaimTx: common.Hash{2}, BlockNumber: 200}
		steps := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{
			Step:  types.StepClaimed,
			Claim: claim,
		}, nil, t1)

		require.Equal(t, claim, steps[2].Result)
	})
}

func TestLifecycle(t *testing.T) {
	t.Parallel()

	t1 := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	t.Run("running: points at the current step", func(t *testing.T) {
		t.Parallel()

		steps := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{Step: types.StepWaitingGERInjection}, nil, t1)
		status, idx := Lifecycle(steps, types.StepWaitingGERInjection)

		require.Equal(t, types.TrackingStatusRunning, status)
		require.Equal(t, 1, idx)
	})

	t.Run("finished: points at the terminal Claimed step", func(t *testing.T) {
		t.Parallel()

		steps := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{Step: types.StepClaimed}, nil, t1)
		status, idx := Lifecycle(steps, types.StepClaimed)

		require.Equal(t, types.TrackingStatusFinished, status)
		require.Equal(t, 3, idx)
	})

	t.Run("error: points at the first step in error, regardless of the current step", func(t *testing.T) {
		t.Parallel()

		steps := BuildSteps(types.BridgeTypeL1ToL2, StepResolution{Step: types.StepWaitingGERInjection}, nil, t1)
		steps[0].Status = types.StepStatusError

		status, idx := Lifecycle(steps, types.StepWaitingGERInjection)

		require.Equal(t, types.TrackingStatusError, status)
		require.Equal(t, 0, idx)
	})
}

func stepsOf(paths []types.BridgeStepPath) []types.BridgeStep {
	steps := make([]types.BridgeStep, len(paths))
	for i, p := range paths {
		steps[i] = p.Step
	}
	return steps
}
