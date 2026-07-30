package domain

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

// WaitingGERInjectionResolver resolves StepWaitingGERInjection: whether the Global Exit Root
// covering the bridge has been injected on its destination network. Only ever the current step
// when the destination is an L2, since ExpectedPath omits it for L2->L1 paths
type WaitingGERInjectionResolver struct {
	Facts BridgeFacts
}

// Resolve implements StepResolver
func (r *WaitingGERInjectionResolver) Resolve(ctx context.Context, tracking *TrackingData, _ int) (any, error) {
	injected, err := r.Facts.InjectedGER(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("injected GER: %w", err)
	}
	if injected == nil {
		return nil, ErrStepPending
	}

	return &types.InjectedGERResult{GER: *injected.GER}, nil
}
