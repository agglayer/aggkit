package types

import (
	"context"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"

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
		importedBridges []*agglayertypes.ImportedBridgeExit,
	) (common.Hash, error)
}
