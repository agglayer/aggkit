package domain

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// LERSource is the driven port to the Local Exit Root state on an L2-originated bridge's
// origin network
type LERSource interface {
	// OriginLER returns the LER update on the origin L2 network that covers bridge, or nil if
	// it is not covered yet
	OriginLER(ctx context.Context, bridge *BridgeInfo) (*types.LERUpdateResult, error)
}

// WaitingLERUpdateResolver resolves StepWaitingLERUpdate: whether the Local Exit Root of an
// L2-originated bridge's origin network has been updated to cover it. Only ever the current
// step of an L2->L1 or L2->L2' path, since ExpectedPath omits it otherwise
type WaitingLERUpdateResolver struct {
	port LERSource
}

// NewWaitingLERUpdateResolver returns a WaitingLERUpdateResolver reading the origin LER through port
func NewWaitingLERUpdateResolver(port LERSource) *WaitingLERUpdateResolver {
	return &WaitingLERUpdateResolver{port: port}
}

// Resolve implements StepResolver
func (r *WaitingLERUpdateResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, _ int,
) (any, error) {
	ler, err := r.port.OriginLER(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("origin LER: %w", err)
	}
	if ler == nil {
		return nil, ErrStepPending
	}

	return ler, nil
}

// StartDate returns the origin deposit's own block timestamp: this resolver is only ever the
// first step of an L2->L1 or L2->L2' path (see ExpectedPath), whose beginning is the bridge's
// own creation, not "the step before it finished" — there is none
func (r *WaitingLERUpdateResolver) StartDate(info *BridgeInfo, _ any) *time.Time {
	return blockTime(info.BlockTimestamp)
}

// EndDate has no deterministic value today: LERUpdateResult only carries BlockNumber, not its
// own timestamp — resolving it would need an extra RPC call this resolver does not make yet
// (agglayer/aggkit#1840)
func (r *WaitingLERUpdateResolver) EndDate(_ any) *time.Time {
	return nil
}
