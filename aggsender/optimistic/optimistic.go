package optimistic

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	gethcommon "github.com/ethereum/go-ethereum/common"
)

// NewOptimistic creates a new instance of OptimisticSignatureCalculatorImpl and OptimisticModeQuerierFromContract.
func NewOptimistic(ctx context.Context,
	logger *log.Logger,
	l1Client aggkittypes.BaseEthereumClienter,
	rollupAddr gethcommon.Address,
	chainID uint64,
	cfg Config) (*OptimisticSignatureCalculatorImpl, *OptimisticModeQuerierFromContract, error) {
	optimisticSigner, err := NewOptimisticSignatureCalculatorImpl(
		ctx,
		logger,
		l1Client,
		rollupAddr,
		chainID,
		cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating optimistic signer: %w", err)
	}
	optimisticModeQuerier, err := NewOptimisticModeQuerierFromContract(rollupAddr, l1Client)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating optimistic mode querier: %w", err)
	}
	return optimisticSigner, optimisticModeQuerier, nil
}
