package domain

import (
	"context"
	"fmt"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
)

// ClaimChecker is the driven port to whether a bridge has been claimed on its destination
// network: the on-chain isClaimed() call, the same source of truth ActivityClaimChecker uses for
// the activity endpoint (see domain.ActivityClaimChecker)
type ClaimChecker interface {
	// IsClaimed reports whether bridge has already been claimed on its destination network
	IsClaimed(ctx context.Context, bridge *BridgeInfo) (bool, error)
}

// WaitingClaimResolver resolves StepWaitingClaim: whether the bridge has been claimed on its
// destination network, per the destination bridge contract's own isClaimed() — fast and
// authoritative, but it carries no claim transaction/block details of its own (see
// ClaimedResolver, which resolves those once this step completes)
type WaitingClaimResolver struct {
	port ClaimChecker
}

// NewWaitingClaimResolver returns a WaitingClaimResolver checking claims through port
func NewWaitingClaimResolver(port ClaimChecker) *WaitingClaimResolver {
	return &WaitingClaimResolver{port: port}
}

// Resolve implements StepResolver
func (r *WaitingClaimResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, _ int,
) (any, error) {
	claimed, err := r.port.IsClaimed(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("claim status: %w", err)
	}
	if !claimed {
		return nil, ErrStepPending
	}

	return nil, nil
}

// StartDate has no deterministic value of its own: this step's beginning is always "the
// previous step just finished" (chained by UpdateStep)
func (r *WaitingClaimResolver) StartDate(_ *BridgeInfo, _ any) *time.Time {
	return nil
}

// EndDate has no deterministic value of its own: isClaimed() confirms the milestone but carries
// no claim transaction/block details — the real value is produced by the very next step (see
// ClaimedResolver)
func (r *WaitingClaimResolver) EndDate(_ any) *time.Time {
	return nil
}
