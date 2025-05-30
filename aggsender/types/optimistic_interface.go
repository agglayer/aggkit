package types

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
)

type OptimisticModeQuerier interface {
	// IsOptimisticModeOn returns true if the optimistic mode is on
	IsOptimisticModeOn() (bool, error)
}

type OptimisticSigner interface {
	Sign(ctx context.Context,
		aggchainReq AggchainProofRequest,
		newLocalExitRoot common.Hash,
		certBuildParams *CertificateBuildParams,
	) ([]byte, error)
}
