package domain

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// ClaimSource is the driven port to the claim record of a bridge on its destination network
type ClaimSource interface {
	// ClaimFor returns the claim transaction of bridge on the destination network, or nil if
	// the destination network's bridge service has not indexed it yet
	ClaimFor(ctx context.Context, bridge *BridgeInfo) (*types.ClaimResult, error)
}

// ClaimedResolver resolves StepClaimed: once StepWaitingClaim's on-chain check confirms the
// bridge is claimed, this fetches the claim transaction/block from the destination network's
// bridge service. It can stay pending a little after StepWaitingClaim completes — the bridge
// service may not have indexed the claim tx yet even though isClaimed() already returns true —
// so it does get its own fact check, unlike a plain waypoint
type ClaimedResolver struct {
	port ClaimSource
}

// NewClaimedResolver returns a ClaimedResolver reading claims through port
func NewClaimedResolver(port ClaimSource) *ClaimedResolver {
	return &ClaimedResolver{port: port}
}

// Resolve implements StepResolver
func (r *ClaimedResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, _ int,
) (any, error) {
	claim, err := r.port.ClaimFor(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("claim info: %w", err)
	}
	if claim == nil {
		return nil, ErrStepPending
	}

	return claim, nil
}

// StartDate returns the same claim tx block timestamp as EndDate: unlike every other step,
// StepClaimed's own milestone is a single point-in-time on-chain fact, not a span that begins
// whenever the step before it (StepWaitingClaim, whose own on-chain isClaimed() check carries no
// block of its own) happened to be checked — so it is worth overriding the StartDate this step
// was opened with (chained from StepWaitingClaim) with this same, more precise value
func (r *ClaimedResolver) StartDate(_ *BridgeInfo, result any) *time.Time {
	return r.EndDate(result)
}

// EndDate returns the claim tx's own destination-network block timestamp
func (r *ClaimedResolver) EndDate(result any) *time.Time {
	claim, ok := result.(*types.ClaimResult)
	if !ok {
		return nil
	}
	return blockTime(claim.BlockTimestamp)
}

// Warning never has anything to report: EndDate's own value either exists or falls back to now,
// with no partial-failure mode of its own worth explaining
func (r *ClaimedResolver) Warning(_ any) *string {
	return nil
}
