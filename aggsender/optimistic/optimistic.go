package optimistic

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
)

// NewOptimistic creates a new instance of OptimisticSignatureCalculatorImpl and OptimisticModeQuerierFromContract.
func NewOptimistic(ctx context.Context,
	logger *log.Logger,
	l1Client types.EthClient,
	cfg Config) (*OptimisticSignatureCalculatorImpl, *OptimisticModeQuerierFromContract, error) {
	optimisticSigner, err := NewOptimisticSignatureCalculatorImpl(
		ctx,
		logger,
		l1Client,
		cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating optimistic signer: %w", err)
	}
	optimisticModeQuerier, err := NewOptimisticModeQuerierFromContract(cfg.SovereignRollupAddr, l1Client)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating optimistic mode querier: %w", err)
	}
	return optimisticSigner, optimisticModeQuerier, nil
}
