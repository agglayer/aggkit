package api

import (
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
)

// BridgeStepPath describes one step of the expected path of a bridge, as returned by the API
type BridgeStepPath struct {
	// StepIndex is this step's position within the parent TrackingData.AllSteps list
	StepIndex int `json:"step_index"`
	// StepName is the string representation of the bridge step (e.g. "PendingInclusion")
	StepName string `json:"step_name"`
	// Status is the string representation of the step's status (e.g. "done")
	Status           string          `json:"status"`
	StartDate        *time.Time      `json:"start_date,omitempty"`
	EndDate          *time.Time      `json:"end_date,omitempty"`
	ExpectedDuration *types.Duration `json:"expected_duration,omitempty"`
	// Result is the data the step has produced so far; its shape depends on Step:
	// *types.GERUpdateResult (StepWaitingGERUpdate), *types.InjectedGERResult
	// (StepWaitingGERInjection), *types.LERUpdateResult (StepWaitingLERUpdate),
	// *types.PendingInclusionResult (StepPendingInclusion), *types.CertificateData
	// (StepCertificatePending), *types.L1SettledGERResult (StepWaitL1SettledGER) or
	// *types.ClaimResult (StepWaitingClaim). nil until
	// the step produces one, and for steps that never do. Most steps only set this once Done,
	// but StepCertificatePending (Status still InProgress) may already carry the certificate's
	// current, not yet settled, status — see domain.ErrCertificateNotSettled
	Result any `json:"result,omitempty"`
	// Error carries the error details when Status is types.StepStatusError, nil otherwise
	Error *types.ErrorStep `json:"error,omitempty"`
}

// newBridgeStepPaths converts the domain-internal step path into the wire shape published to
// clients; nil in, nil out (TrackingData.AllSteps stays nil while the bridge is unresolved)
func newBridgeStepPaths(steps []domain.BridgeStepPath) []BridgeStepPath {
	if steps == nil {
		return nil
	}
	wire := make([]BridgeStepPath, len(steps))
	for i, s := range steps {
		wire[i] = BridgeStepPath{
			StepIndex:        i,
			StepName:         s.Step.String(),
			Status:           s.Status.String(),
			StartDate:        s.StartDate,
			EndDate:          s.EndDate,
			ExpectedDuration: s.ExpectedDuration,
			Result:           s.Result(),
			Error:            s.Error,
		}
	}
	return wire
}
