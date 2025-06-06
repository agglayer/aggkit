package optimistic

import (
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggchainfep"
	optimistichash "github.com/agglayer/aggkit/aggsender/optimistic/optimistichash"
	"github.com/agglayer/aggkit/opnode"
	"github.com/ethereum/go-ethereum/common"
)

// This is just to check in build time that the expected objects fulfill the interfaces
var (
	_ OpNodeClienter                                = (*opnode.OpNodeClient)(nil)
	_ FEPContractQuerier                            = (*aggchainfep.Aggchainfep)(nil)
	_ OptimisticAggregationProofPublicValuesQuerier = (*OptimisticAggregationProofPublicValuesQuery)(nil)
)

// OptimisticAggregationProofPublicValuesQuery implements OptimisticAggregationProofPublicValuesQuerier
type OptimisticAggregationProofPublicValuesQuery struct {
	aggchainFEPContract FEPContractQuerier
	aggchainFEPAddr     common.Address
	opNodeClient        OpNodeClienter
	proverAddress       common.Address
}

// NewOptimisticAggregationProofPublicValuesQuery creates a new instance of OptimisticAggregationProofPublicValuesQuery
func NewOptimisticAggregationProofPublicValuesQuery(
	aggchainFEPContract FEPContractQuerier,
	aggchainFEPAddr common.Address,
	opNodeClient OpNodeClienter,
	proverAddress common.Address,
) *OptimisticAggregationProofPublicValuesQuery {
	return &OptimisticAggregationProofPublicValuesQuery{
		aggchainFEPContract: aggchainFEPContract,
		aggchainFEPAddr:     aggchainFEPAddr,
		opNodeClient:        opNodeClient,
		proverAddress:       proverAddress,
	}
}

// GetAggregationProofPublicValuesData retrieves the AggregationProofPublicValue required for
// the optimistic aggregation proof
func (o *OptimisticAggregationProofPublicValuesQuery) GetAggregationProofPublicValuesData(
	lastProvenBlock, requestedEndBlock uint64,
	l1InfoTreeLeafHash common.Hash) (*optimistichash.AggregationProofPublicValues, error) {
	l2PreRoot, err := o.opNodeClient.OutputAtBlockRoot(lastProvenBlock)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. l2PreRoot opNodeClient.OutputAtBlockRoot(%d). Err: %w",
			lastProvenBlock, err)
	}
	claimRoot, err := o.opNodeClient.OutputAtBlockRoot(requestedEndBlock)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. claimRoot opNodeClient.OutputAtBlockRoot(%d). Err: %w",
			requestedEndBlock, err)
	}
	rollupConfigHash, err := o.aggchainFEPContract.RollupConfigHash(nil)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. rollupConfigHash from contract %s. Err: %w",
			o.aggchainFEPAddr, err)
	}
	multiBlockVKey, err := o.aggchainFEPContract.RangeVkeyCommitment(nil)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. multiBlockVKey(AggregationVkey) from contract %s. Err: %w",
			o.aggchainFEPAddr, err)
	}

	return &optimistichash.AggregationProofPublicValues{
		L1Head:           l1InfoTreeLeafHash,
		L2PreRoot:        l2PreRoot,
		ClaimRoot:        claimRoot,
		L2BlockNumber:    requestedEndBlock,
		RollupConfigHash: rollupConfigHash,
		MultiBlockVKey:   multiBlockVKey,
		ProverAddress:    o.proverAddress,
	}, nil
}
