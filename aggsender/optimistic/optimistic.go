package optimistic

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/aggchain-ecdsa-multisig/aggchainfep"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
)

// NewOptimistic creates a new instance of OptimisticSignatureCalculatorImpl and OptimisticModeQuerierFromContract.
func NewOptimistic(ctx context.Context,
	logger *log.Logger,
	l1Client aggkittypes.BaseEthereumClienter,
	chainID uint64,
	cfg Config) (*OptimisticSignatureCalculatorImpl, *OptimisticModeQuerierFromContract, error) {
	aggchainFEP, err := aggchainfep.NewAggchainfepCaller(cfg.SovereignRollupAddr, l1Client)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating aggchainFEP contract caller: %w", err)
	}

	optimisticSigner, err := NewOptimisticSignatureCalculatorImpl(
		ctx,
		logger,
		aggchainFEP,
		chainID,
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
