package domain

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// ClaimSource is the driven port to the claim state of a bridge on its destination network
type ClaimSource interface {
	// ClaimFor returns the claim transaction of bridge on the destination network, or nil if
	// it has not been claimed yet
	ClaimFor(ctx context.Context, bridge *BridgeInfo) (*types.ClaimResult, error)
}

// WaitingClaimResolver resolves StepWaitingClaim: whether the bridge has been claimed on its
// destination network. Completing it also completes StepClaimed in the same call (see
// UpdateStep): Claimed never has a fact check of its own, it is reached the instant the claim
// is found
type WaitingClaimResolver struct {
	port ClaimSource
}

// NewWaitingClaimResolver returns a WaitingClaimResolver reading claims through port
func NewWaitingClaimResolver(port ClaimSource) *WaitingClaimResolver {
	return &WaitingClaimResolver{port: port}
}

// Resolve implements StepResolver
func (r *WaitingClaimResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, _ int,
) (any, error) {
	claim, err := r.port.ClaimFor(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("claim status: %w", err)
	}
	if claim == nil {
		return nil, ErrStepPending
	}

	return claim, nil
}
