package optimistic

import (
	optimistichash "github.com/agglayer/aggkit/aggsender/optimistic/optimistichash"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// OpNodeClienter is an interface that defines the methods for interacting with the OpNode client.
type OpNodeClienter interface {
	OutputAtBlockRoot(blockNum uint64) (common.Hash, error)
}

// FEPContractQuerier is an interface that defines the methods for interacting with the FEP contract.
type FEPContractQuerier interface {
	RollupConfigHash(opts *bind.CallOpts) ([32]byte, error)
	RangeVkeyCommitment(opts *bind.CallOpts) ([32]byte, error)
	OptimisticMode(opts *bind.CallOpts) (bool, error)
}

// OptimisticAggregationProofPublicValuesQuerier defines an interface for querying aggregation proof public values in optimistic mode.
type OptimisticAggregationProofPublicValuesQuerier interface {
	GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock uint64,
		l1InfoTreeLeafHash common.Hash) (*optimistichash.AggregationProofPublicValues, error)
}
