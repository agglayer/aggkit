package domain

import (
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

// BuildSteps materializes the expected path of a bridge as BridgeStepPath entries: steps
// before the current one are done, the current one is in progress (or done if terminal)
// and later ones are pending. Dates and results already observed in prevSteps are carried
// over so an unchanged bridge produces an identical result (and is therefore not
// re-published); now stamps the dates of newly reached milestones and res carries the
// result of whichever step just completed
func BuildSteps(
	bridgeType types.BridgeType, res StepResolution, prevSteps []types.BridgeStepPath, now time.Time,
) []types.BridgeStepPath {
	path := ExpectedPath(bridgeType)

	currentIdx := 0
	for i, step := range path {
		if step == res.Step {
			currentIdx = i
			break
		}
	}

	prevByStep := make(map[types.BridgeStep]types.BridgeStepPath, len(prevSteps))
	for _, sp := range prevSteps {
		prevByStep[sp.Step] = sp
	}

	steps := make([]types.BridgeStepPath, len(path))
	for i, stepID := range path {
		sp := types.BridgeStepPath{Step: stepID}
		old, hadOld := prevByStep[stepID]
		if hadOld {
			sp.Result = old.Result
		}
		switch {
		case i < currentIdx:
			sp.Status = types.StepStatusDone
			if hadOld {
				sp.StartDate = old.StartDate
			}
			if hadOld && old.EndDate != nil {
				sp.EndDate = old.EndDate
			} else {
				endDate := now
				sp.EndDate = &endDate
			}
		case i == currentIdx:
			sp.Status = types.StepStatusInProgress
			if hadOld && old.Status == types.StepStatusInProgress && old.StartDate != nil {
				sp.StartDate = old.StartDate
			} else {
				startDate := now
				sp.StartDate = &startDate
			}
			if stepID == types.StepClaimed {
				// terminal step: it completes the moment it is reached
				sp.Status = types.StepStatusDone
				endDate := now
				sp.EndDate = &endDate
			}
		default:
			sp.Status = types.StepStatusPending
		}

		switch stepID {
		case types.StepWaitingGERUpdate:
			if res.GERUpdate != nil {
				sp.Result = res.GERUpdate
			}
		case types.StepWaitingLERUpdate:
			if res.LERUpdate != nil {
				sp.Result = res.LERUpdate
			}
		case types.StepCertificateProcessing:
			if res.Certificate != nil {
				sp.Result = res.Certificate
			}
		case types.StepWaitingGERInjection:
			if res.InjectedGER != nil {
				sp.Result = res.InjectedGER
			}
		case types.StepWaitingClaim:
			if res.Claim != nil {
				sp.Result = res.Claim
			}
		}

		steps[i] = sp
	}
	return steps
}

// Lifecycle derives the TrackingStatus of a resolved bridge from its built steps, along with
// the index (into steps) of the step that explains that status: the first step in error if
// any step failed, the current step if the bridge is still running, or the last step
// (Claimed) once it is finished. It never returns TrackingStatusRegistered: that value only
// applies before a bridge is resolved, which callers handle separately
func Lifecycle(steps []types.BridgeStepPath, currentStep types.BridgeStep) (types.TrackingStatus, int) {
	for i, sp := range steps {
		if sp.Status == types.StepStatusError {
			return types.TrackingStatusError, i
		}
	}

	currentIdx := 0
	for i, sp := range steps {
		if sp.Step == currentStep {
			currentIdx = i
			break
		}
	}

	if currentStep == types.StepClaimed {
		return types.TrackingStatusFinished, currentIdx
	}
	return types.TrackingStatusRunning, currentIdx
}
