package domain

import (
	"context"
	"fmt"

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
