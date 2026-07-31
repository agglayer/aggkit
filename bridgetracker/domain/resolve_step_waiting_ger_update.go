package domain

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

// WaitingGERUpdateResolver resolves StepWaitingGERUpdate: whether the Global Exit Root has
// been updated to cover an L1-originated bridge. Only ever the current step of an L1->L2 path,
// since ExpectedPath omits it otherwise
type WaitingGERUpdateResolver struct{}

// Resolve implements StepResolver
func (r WaitingGERUpdateResolver) Resolve(
	ctx context.Context, facts BridgeFacts, tracking *TrackingData, _ int,
) (any, error) {
	ger, err := facts.OriginGER(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("origin GER: %w", err)
	}
	if ger == nil {
		return nil, ErrStepPending
	}

	return &types.GERUpdateResult{GER: *ger.GER, BlockNumber: *ger.BlockNumber}, nil
}
