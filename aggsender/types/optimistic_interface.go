package types

import (
	"context"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/ethereum/go-ethereum/common"
)

type OptimisticModeQuerier interface {
	// IsOptimisticModeOn returns true if the optimistic mode is on
	IsOptimisticModeOn() (bool, error)
}

// OptimisticSigner is an interface for signing optimistic proofs.
type OptimisticSigner interface {
	Sign(ctx context.Context,
		aggchainReq AggchainProofRequest,
		newLocalExitRoot common.Hash,
		claims []claimsynctypes.Claim,
	) ([]byte, string, error)
}
