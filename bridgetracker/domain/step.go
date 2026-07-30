package domain

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
)

// BridgeFacts is the driven port of the step derivation: the facts of one bridge, resolved
// on demand. DeriveStep follows the bridge lifecycle and stops at the first unmet milestone,
// so implementations are only queried for the facts the bridge has reached
type BridgeFacts interface {
	// OriginGER returns the GER update on the origin network that covers the bridge, or nil
	// if the bridge is not covered by any GER update yet. Only queried for L1-originated
	// bridges
	OriginGER(ctx context.Context) (*types.GERData, error)

	// OriginLER returns the LER update on the origin L2 network that covers the bridge, or
	// nil if the bridge is not covered yet. Only queried for L2-originated bridges
	OriginLER(ctx context.Context) (*types.LERUpdateResult, error)

	// Certificate returns the agglayer certificate that includes the bridge, or nil if the
	// bridge is not part of any certificate yet. Only queried for L2-originated bridges
	Certificate(ctx context.Context) (*types.CertificateData, error)

	// InjectedGER returns the GER injected on the destination network that covers the
	// bridge, or nil if no covering GER has been injected yet. Only queried when the
	// destination is an L2
	InjectedGER(ctx context.Context) (*types.GERData, error)

	// ClaimFor returns the claim transaction of the bridge on the destination network, or
	// nil if it has not been claimed yet
	ClaimFor(ctx context.Context) (*types.ClaimResult, error)
}

// StepResolution is the outcome of DeriveStep: the current step of the bridge plus the
// result of whichever step just completed, so callers can expose it without re-querying
type StepResolution struct {
	// Step is the current step of the bridge
	Step types.BridgeStep
	// GERUpdate is the result of StepWaitingGERUpdate, set once resolved (L1-origin only)
	GERUpdate *types.GERUpdateResult
	// LERUpdate is the result of StepWaitingLERUpdate, set once resolved (L2-origin only)
	LERUpdate *types.LERUpdateResult
	// Certificate is the result of StepCertificateProcessing, set only once the certificate
	// including the bridge is settled
	Certificate *types.CertificateData
	// InjectedGER is the result of StepWaitingGERInjection, set once the covering GER has
	// been injected on the destination network (L2 destinations only)
	InjectedGER *types.InjectedGERResult
	// Claim is the result of StepWaitingClaim, set once the bridge has been claimed
	Claim *types.ClaimResult
}

// DeriveStep derives the current step of a bridge from its facts. The checks follow the
// bridge lifecycle and stop at the first unmet milestone, so later facts are not queried
// until the bridge reaches them. prevSteps is the bridge's last persisted path (nil on the
// first resolution) and makes the derivation incremental: a milestone already recorded as
// done is never queried again — its result was persisted with the step and BuildSteps
// carries it forward — so an already-advanced bridge only pays for the facts it is still
// waiting on
func DeriveStep(
	ctx context.Context, originNetwork, destinationNetwork uint32, facts BridgeFacts,
	prevSteps []types.BridgeStepPath,
) (StepResolution, error) {
	done := make(map[types.BridgeStep]bool, len(prevSteps))
	for _, sp := range prevSteps {
		done[sp.Step] = sp.Status == types.StepStatusDone
	}

	res := StepResolution{}

	if originNetwork == 0 {
		if !done[types.StepWaitingGERUpdate] {
			ger, err := facts.OriginGER(ctx)
			if err != nil {
				return res, fmt.Errorf("origin GER: %w", err)
			}
			if ger == nil {
				res.Step = types.StepWaitingGERUpdate
				return res, nil
			}
			res.GERUpdate = &types.GERUpdateResult{GER: *ger.GER, BlockNumber: *ger.BlockNumber}
		}
	} else {
		if !done[types.StepWaitingLERUpdate] {
			ler, err := facts.OriginLER(ctx)
			if err != nil {
				return res, fmt.Errorf("origin LER: %w", err)
			}
			if ler == nil {
				res.Step = types.StepWaitingLERUpdate
				return res, nil
			}
			res.LERUpdate = ler
		}

		// bridges originated on an L2 exit through an agglayer certificate; the certificate
		// milestone only closes at settlement, so it is re-queried until
		// StepCertificateProcessing is done
		if !done[types.StepCertificateProcessing] {
			cert, err := facts.Certificate(ctx)
			if err != nil {
				return res, fmt.Errorf("certificate: %w", err)
			}
			switch {
			case cert == nil:
				res.Step = types.StepPendingInclusion
				return res, nil
			case cert.Status.IsSettled():
				res.Certificate = cert
			case cert.Status == agglayertypes.Pending:
				res.Step = types.StepCertificatePending
				return res, nil
			default: // Proven, Candidate or InError
				res.Step = types.StepCertificateProcessing
				return res, nil
			}
		}
	}

	// L2 destinations need the covering GER injected (does not apply to Mainnet)
	if destinationNetwork != 0 && !done[types.StepWaitingGERInjection] {
		injected, err := facts.InjectedGER(ctx)
		if err != nil {
			return res, fmt.Errorf("injected GER: %w", err)
		}
		if injected == nil {
			res.Step = types.StepWaitingGERInjection
			return res, nil
		}
		res.InjectedGER = &types.InjectedGERResult{GER: *injected.GER}
	}

	claim, err := facts.ClaimFor(ctx)
	if err != nil {
		return res, fmt.Errorf("claim status: %w", err)
	}
	if claim != nil {
		res.Step = types.StepClaimed
		res.Claim = claim
	} else {
		res.Step = types.StepWaitingClaim
	}
	return res, nil
}
