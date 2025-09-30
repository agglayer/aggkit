package optimistic

import (
	"math/big"

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
	StartingBlockNumber(opts *bind.CallOpts) (*big.Int, error)
	LatestBlockNumber(opts *bind.CallOpts) (*big.Int, error)
	GetAggchainSigners(opts *bind.CallOpts) ([]common.Address, error)
	OptimisticMode(opts *bind.CallOpts) (bool, error)
	SelectedOpSuccinctConfigName(opts *bind.CallOpts) ([32]byte, error)
	OpSuccinctConfigs(opts *bind.CallOpts, arg0 [32]byte) (struct {
		AggregationVkey     [32]byte
		RangeVkeyCommitment [32]byte
		RollupConfigHash    [32]byte
	}, error)
}

// OptimisticAggregationProofPublicValuesQuerier defines an interface for
// querying aggregation proof public values in optimistic mode.
type OptimisticAggregationProofPublicValuesQuerier interface {
	GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock uint64,
		l1InfoTreeLeafHash common.Hash) (*optimistichash.AggregationProofPublicValues, error)
}
