package query

import (
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainfep"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.AggchainFEPRollupQuerier = (*noOpAggchainFEPRollupQuerier)(nil)

// noOpAggchainFEPRollupQuerier is a no-operation implementation of the AggchainFEPQuerier interface.
// It provides empty implementations for all query methods, typically used for testing
// or when aggregation chain FEP (Front End Processor) querying functionality is disabled when
// network is in PessimisticProof mode.
type noOpAggchainFEPRollupQuerier struct{}

func (n *noOpAggchainFEPRollupQuerier) StartL2Block() uint64 {
	return 0
}

func (n *noOpAggchainFEPRollupQuerier) GetLastSettledL2Block() (uint64, error) {
	return 0, nil
}

func (n *noOpAggchainFEPRollupQuerier) IsFEP() bool {
	return false
}

func (n *noOpAggchainFEPRollupQuerier) GetAggregationProofPublicValuesData(
	lastProvenBlock, requestedEndBlock uint64,
	l1InfoTreeLeafHash common.Hash) (*types.AggregationProofPublicValues, error) {
	return &types.AggregationProofPublicValues{}, nil
}

var _ types.AggchainFEPRollupQuerier = (*aggchainFEPRollupQuerier)(nil)

// aggchainFEPRollupQuerier encapsulates the necessary information and interfaces required to query
// the Aggchain FEP rollup contract
type aggchainFEPRollupQuerier struct {
	startL2BlockNum   uint64
	aggchainFEPAddr   common.Address
	aggchainFEPCaller types.FEPContractQuerier
}

// NewAggchainFEPQuerier creates a new AggchainFEP querier instance for interacting with the AggchainFEP contract.
//
// The function handles two scenarios:
// 1. If aggchainFEPAddr is the zero address, it returns a no-op querier for PP networks without AggchainFEP contract
// 2. If a valid address is provided, it creates a real querier that connects to the AggchainFEP contract
//
// Parameters:
//   - logger: Logger instance for recording operations and errors
//   - aggchainFEPAddr: The Ethereum address of the AggchainFEP contract
//   - l1Client: Ethereum client interface for L1 blockchain interactions
//
// Returns:
//   - types.AggchainFEPQuerier: Either a no-op or real querier implementation
//   - error: Any error encountered during contract initialization or starting block number retrieval
//
// The function will fail if:
//   - The AggchainFEP contract caller cannot be created
//   - The starting block number cannot be retrieved from the contract
func NewAggchainFEPQuerier(
	logger *log.Logger,
	aggsenderMode types.AggsenderMode,
	aggchainFEPAddr common.Address,
	l1Client aggkittypes.BaseEthereumClienter) (types.AggchainFEPRollupQuerier, error) {
	if aggchainFEPAddr == aggkitcommon.ZeroAddress || aggsenderMode == types.PessimisticProofMode {
		// its a PP network without AggchainFEP contract
		logger.Infof("aggchainProverFlow - AggchainFEP contract address is zero, or mode (%s) is "+
			"PessimisticProofMode, using no-op querier", aggsenderMode)
		return &noOpAggchainFEPRollupQuerier{}, nil
	}

	aggChainFEPContract, err := aggchainfep.NewAggchainfepCaller(aggchainFEPAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error creating AggchainFEP rollup caller (%s): %w",
			aggchainFEPAddr.String(), err)
	}

	return newAggchainFEPQuerier(logger, aggchainFEPAddr, aggChainFEPContract)
}

func newAggchainFEPQuerier(
	logger *log.Logger,
	aggchainFEPAddr common.Address,
	aggchainFEPCaller types.FEPContractQuerier,
) (types.AggchainFEPRollupQuerier, error) {
	startL2Block, err := aggchainFEPCaller.StartingBlockNumber(nil)
	if err != nil {
		return nil, fmt.Errorf("aggchainProverFlow - error AggChainFEPContract.StartingBlockNumber (%s): %w",
			aggchainFEPAddr.String(), err)
	}

	logger.Infof("aggchainProverFlow - AggchainFEP contract address is not zero, using real querier (%s)",
		aggchainFEPAddr.String())

	return &aggchainFEPRollupQuerier{
		startL2BlockNum:   startL2Block.Uint64(),
		aggchainFEPCaller: aggchainFEPCaller,
		aggchainFEPAddr:   aggchainFEPAddr,
	}, nil
}

// IsFEP returns true if the AggchainFEP querier is for FEP networks, false otherwise.
func (a *aggchainFEPRollupQuerier) IsFEP() bool {
	return true
}

// StartL2Block returns the starting L2 block number for the FEP network.
func (a *aggchainFEPRollupQuerier) StartL2Block() uint64 {
	return a.startL2BlockNum
}

// GetLastSettledL2Block retrieves the latest settled L2 block number from the AggchainFEP contract.
// It calls the LatestBlockNumber method on the aggchainFEPCaller and returns the block number as uint64.
// Returns an error if the contract call fails or if there's an issue retrieving the block number.
func (a *aggchainFEPRollupQuerier) GetLastSettledL2Block() (uint64, error) {
	latestSettledL2Block, err := a.aggchainFEPCaller.LatestBlockNumber(nil)
	if err != nil {
		return 0, fmt.Errorf("aggchainProverFlow - error getting latest settled block number from AggchainFEP (%s): %w",
			a.aggchainFEPAddr.String(), err)
	}

	return latestSettledL2Block.Uint64(), nil
}
