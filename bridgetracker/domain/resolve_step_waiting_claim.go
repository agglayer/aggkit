package domain

import (
	"context"
	"fmt"
)

// WaitingClaimResolver resolves StepWaitingClaim: whether the bridge has been claimed on its
// destination network. Completing it also completes StepClaimed in the same call (see
// UpdateStep): Claimed never has a fact check of its own, it is reached the instant the claim
// is found
type WaitingClaimResolver struct{}

// Resolve implements StepResolver
func (r WaitingClaimResolver) Resolve(
	ctx context.Context, facts BridgeFacts, tracking *TrackingData, _ int,
) (any, error) {
	claim, err := facts.ClaimFor(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("claim status: %w", err)
	}
	if claim == nil {
		return nil, ErrStepPending
	}

	return claim, nil
}
