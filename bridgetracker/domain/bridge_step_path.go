package domain

import (
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

// BridgeStepPath is the domain-internal representation of one step of the expected path of a
// bridge — ResolveSteps/UpdateStep read and mutate it directly. See api.BridgeStepPath for the
// wire shape published to clients (StepString/StatusString and JSON tags/MarshalJSON belong
// there, not here)
type BridgeStepPath struct {
	Step   types.BridgeStep
	Status types.StepStatus

	StartDate        *time.Time
	EndDate          *time.Time
	ExpectedDuration *types.Duration

	// Result is the data the step has produced so far; its shape depends on Step:
	// *types.GERUpdateResult (StepWaitingGERUpdate), *types.InjectedGERResult
	// (StepWaitingGERInjection), *types.LERUpdateResult (StepWaitingLERUpdate),
	// *types.PendingInclusionResult (StepPendingInclusion), *types.CertificateData
	// (StepCertificatePending), *types.L1SettledGERResult (StepWaitL1SettledGER) or
	// *types.ClaimResult (StepClaimed). nil until the step produces one, and for steps
	// that never do. Most steps only set this once Done, but StepCertificatePending (Status
	// still InProgress) may already carry the certificate's current, not yet settled, status —
	// see ErrCertificateNotSettled
	ResultGerUpdate        *types.GERUpdateResult
	ResultInjectedGer      *types.InjectedGERResult
	ResultLerUpdate        *types.LERUpdateResult
	ResultPendingInclusion *types.PendingInclusionResult
	ResultCertificateData  *types.CertificateData
	ResultL1SettledGer     *types.L1SettledGERResult
	ResultClaim            *types.ClaimResult
	// Error carries the error details when Status is types.StepStatusError, nil otherwise
	Error *types.ErrorStep
}

// SetResult replaces whichever typed Result field currently holds a value with result: at most
// one of them is ever non-nil at a time, mirroring the single Result field this type used to
// have. nil clears all of them (a step whose resolver produced no result this call)
func (b *BridgeStepPath) SetResult(result any) {
	b.ResultGerUpdate = nil
	b.ResultInjectedGer = nil
	b.ResultLerUpdate = nil
	b.ResultPendingInclusion = nil
	b.ResultCertificateData = nil
	b.ResultL1SettledGer = nil
	b.ResultClaim = nil

	switch r := result.(type) {
	case nil:
	case *types.GERUpdateResult:
		b.ResultGerUpdate = r
	case *types.InjectedGERResult:
		b.ResultInjectedGer = r
	case *types.LERUpdateResult:
		b.ResultLerUpdate = r
	case *types.PendingInclusionResult:
		b.ResultPendingInclusion = r
	case *types.CertificateData:
		b.ResultCertificateData = r
	case *types.L1SettledGERResult:
		b.ResultL1SettledGer = r
	case *types.ClaimResult:
		b.ResultClaim = r
	default:
		panic("unexpected result type")
	}
}

// Result returns whichever typed Result field currently holds a value, or nil if none does
func (b *BridgeStepPath) Result() any {
	switch {
	case b.ResultGerUpdate != nil:
		return b.ResultGerUpdate
	case b.ResultInjectedGer != nil:
		return b.ResultInjectedGer
	case b.ResultLerUpdate != nil:
		return b.ResultLerUpdate
	case b.ResultPendingInclusion != nil:
		return b.ResultPendingInclusion
	case b.ResultCertificateData != nil:
		return b.ResultCertificateData
	case b.ResultL1SettledGer != nil:
		return b.ResultL1SettledGer
	case b.ResultClaim != nil:
		return b.ResultClaim
	default:
		return nil
	}
}
