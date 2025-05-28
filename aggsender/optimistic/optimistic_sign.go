package optimistic

import (
	"context"

	"github.com/agglayer/aggkit/aggsender/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

type OptimisticSignatureCalculator interface {
	// CalculateSignature calculates the signature for the given public values.
}

type OptimisticSignatureCalculatorImpl struct {
	queryAggregationProofPublicValues OptimisticAggregationProofPublicValuesQuerier
	signer                            signertypes.HashSigner
}

func (o *OptimisticSignatureCalculatorImpl) Sign(ctx context.Context, types.AggchainProofRequest) (common.Hash, error) {
	aggregationProofPublicValues, err:= o.queryAggregationProofPublicValues.GetAggregationProofPublicValuesData()
}
