package domain

import (
	"context"
	"fmt"
)

// WaitingLERUpdateResolver resolves StepWaitingLERUpdate: whether the Local Exit Root of an
// L2-originated bridge's origin network has been updated to cover it. Only ever the current
// step of an L2->L1 or L2->L2' path, since ExpectedPath omits it otherwise
type WaitingLERUpdateResolver struct{}

// Resolve implements StepResolver
func (r WaitingLERUpdateResolver) Resolve(
	ctx context.Context, facts BridgeFacts, tracking *TrackingData, _ int,
) (any, error) {
	ler, err := facts.OriginLER(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("origin LER: %w", err)
	}
	if ler == nil {
		return nil, ErrStepPending
	}

	return ler, nil
}
