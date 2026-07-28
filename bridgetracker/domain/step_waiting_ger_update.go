package domain

import (
	"context"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

type StepWaitingGERUpdateResolver struct {
}

func (r *StepWaitingGERUpdateResolver) Resolve(ctx context.Context, facts BridgeFacts) (*types.GERUpdateResult, error) {
	ger, err := facts.OriginGER(ctx)
	if err != nil {
		return nil, err
	}
	if ger == nil {
		return nil, nil
	}
	return &types.GERUpdateResult{GER: *ger.GER, BlockNumber: *ger.BlockNumber}, nil
}
