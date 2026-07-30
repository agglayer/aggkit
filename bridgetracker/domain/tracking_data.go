package domain

import (
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// TrackingID identifies a supervised bridge: the network the creating tx was sent to plus
// its hash
type TrackingID struct {
	NetworkID uint32
	TxHash    common.Hash
}

// String implements fmt.Stringer, for use in logs/traces
func (id TrackingID) String() string {
	return fmt.Sprintf("network=%d/tx=%s", id.NetworkID, id.TxHash)
}

// TrackingData is the snapshot of a supervised bridge at a given moment
type TrackingData struct {
	id               TrackingID
	trackingBridgeTx TrackingBridgeTx
	allSteps         []types.BridgeStepPath
}

// NewTrackingData builds the snapshot of a supervised bridge from its identity, tracked tx
// facts and its expected path (nil while the tracker has not resolved the bridge yet).
// TrackingStatus is not stored: it is fully derived from those two (see TrackingStatus)
func NewTrackingData(
	id TrackingID, bridgeTx TrackingBridgeTx, allSteps []types.BridgeStepPath,
) *TrackingData {
	return &TrackingData{id: id, trackingBridgeTx: bridgeTx, allSteps: allSteps}
}

// Error returns whatever error currently explains the bridge's state, if any: the current
// step's error once AllSteps is resolved (nil if that step is not in error), or the tx-level
// Error while it is not — set for a terminal give-up (handleUnresolved / handlePermanentFailure) as
// well as a transient FindBridge failure still being retried (persistResolveError); nil if
// nothing has failed
func (t *TrackingData) Error() *types.ErrorStep {
	if t == nil {
		return &types.ErrorStep{
			ErrorType:   types.StepErrorPermanent,
			RetryCount:  0,
			Description: []string{"tracking data is nil"},
		}
	}
	stepIndex := t.StepIndex()
	if stepIndex != nil {
		if *stepIndex < 0 || *stepIndex >= len(t.allSteps) {
			return &types.ErrorStep{
				ErrorType: types.StepErrorPermanent,
				Description: []string{fmt.Sprintf(
					"step index %d out of range: allSteps has %d entries", *stepIndex, len(t.allSteps),
				)},
			}
		}
		return t.allSteps[*stepIndex].Error
	}
	return t.trackingBridgeTx.Error
}

// TrackingBridgeTx is intended for debugging purposes only.
func (t *TrackingData) TrackingBridgeTx() TrackingBridgeTx {
	if t == nil {
		return TrackingBridgeTx{}
	}
	return t.trackingBridgeTx
}

func (t *TrackingData) AllSteps() []types.BridgeStepPath {
	if t == nil {
		return nil
	}
	return t.allSteps
}

// StepIndex returns the index into AllSteps of the step that explains TrackingStatus: the
// first step in error if any step failed, the step currently in progress if the bridge is
// still running, or the last step (Claimed, always the tail of every path) once it is
// finished. nil while AllSteps is nil (mirrors domain.Lifecycle, without needing the
// resolver's current-step hint: BuildSteps guarantees exactly one of these three shapes)
func (t *TrackingData) StepIndex() *int {
	if t == nil || t.allSteps == nil {
		return nil
	}
	for i, step := range t.allSteps {
		if step.Status == types.StepStatusError {
			return &i
		}
	}
	for i, step := range t.allSteps {
		if step.Status == types.StepStatusInProgress {
			return &i
		}
	}
	lastIdx := len(t.allSteps) - 1
	return &lastIdx
}

// TrackingStatus derives the bridge's lifecycle status from the snapshot, nothing is stored:
// once AllSteps is resolved it reflects the step that explains it (see StepIndex); until
// then, the tx-level facts say it all — a terminal Error (the tracker gave up resolving the
// tx: exhausted or permanent) reads as Error, a resolved tx (IsDone) as Running, and
// anything else (including a transient failure still being retried) as Registered
func (t *TrackingData) TrackingStatus() types.TrackingStatus {
	if t == nil {
		return types.TrackingStatusError
	}
	stepIndex := t.StepIndex()
	if stepIndex != nil {
		return convertStepStatusToTrackingStatus(t.allSteps[*stepIndex].Status)
	}
	if t.trackingBridgeTx.IsInTerminalError() {
		return types.TrackingStatusError
	}
	if t.trackingBridgeTx.IsDone() {
		return types.TrackingStatusRunning
	}
	return types.TrackingStatusRegistered
}

func (t *TrackingData) ID() TrackingID {
	if t == nil {
		// TODO:think about this? must be a fatal?
		return TrackingID{}
	}
	return t.id
}

// Info returns the bridge facts resolved once by BridgeEventSource.FindBridge, nil until then
func (t *TrackingData) Info() *BridgeInfo {
	if t == nil {
		return nil
	}
	return t.trackingBridgeTx.Info
}

// BridgeTx returns the tx-level facts of the snapshot (everything but AllSteps), for store
// implementations that need to update one part of a snapshot while preserving the rest
func (t *TrackingData) BridgeTx() TrackingBridgeTx {
	if t == nil {
		return TrackingBridgeTx{}
	}
	return t.trackingBridgeTx
}

// Failed reports whether the tracker gave up trying to resolve the bridge at all: the tx
// does not exist on the network, or is not a bridge transaction. Distinct from a step-level
// error on an otherwise-resolved bridge (TrackingStatus is also Error there, but Info stays
// populated and the engine keeps polling it)
func (t *TrackingData) Failed() bool {
	return t.TrackingStatus() == types.TrackingStatusError && t.Info() == nil
}

func convertStepStatusToTrackingStatus(stepStatus types.StepStatus) types.TrackingStatus {
	switch stepStatus {
	case types.StepStatusPending:
		return types.TrackingStatusRunning
	case types.StepStatusInProgress:
		return types.TrackingStatusRunning
	case types.StepStatusDone:
		return types.TrackingStatusFinished
	case types.StepStatusError:
		return types.TrackingStatusError
	default:
		return types.TrackingStatusError
	}
}
