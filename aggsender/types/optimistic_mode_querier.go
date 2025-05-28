package types

import "github.com/ethereum/go-ethereum/common"

type OptimisticModeQuerier interface {
	// IsOptimisticModeOn returns true if the optimistic mode is on
	IsOptimisticModeOn() (bool, error)
}

type OptimisticAggregationProofPublicValuesQuerier interface {
	GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock uint64,
		l1InfoTreeLeafHash common.Hash) (*AggregationProofPublicValues, error)
}
