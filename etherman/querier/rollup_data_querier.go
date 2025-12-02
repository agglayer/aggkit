package etherman

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrInvalidRollupID = errors.New("invalid rollup id (0)")
	ErrInvalidChainID  = errors.New("invalid chain id (0)")

	populateAgglayerManagerInitializedMapFn = populateAgglayerManagerInitializedMap

	// preEtrogDeployedRollups contains set of rollup ids that were created prior to Etrog hard fork
	preEtrogDeployedRollups = map[uint32]struct{}{1: {}}
)

// RollupManagerContract is an abstraction for RollupManager smart contract
type RollupManagerContract interface {
	RollupIDToRollupData(opts *bind.CallOpts, rollupID uint32) (
		agglayermanager.AgglayerManagerRollupDataReturn, error)
	RollupAddressToID(opts *bind.CallOpts, rollupAddress common.Address) (uint32, error)
	FilterInitialized(opts *bind.FilterOpts) (*agglayermanager.AgglayermanagerInitializedIterator, error)
}

// mockery:ignore
// RollupManagerFactoryFunc is a callback function that creates RollupManager contrat instance
type RollupManagerFactoryFunc func(rollupAddress common.Address,
	client aggkittypes.BaseEthereumClienter) (RollupManagerContract, error)

// RollupDataQuerier is a simple implementation of Etherman.
type RollupDataQuerier struct {
	rollupManagerSC          RollupManagerContract
	rollupManagerUpgradedMap map[uint8]uint64
	RollupID                 uint32
}

// NewRollupDataQuerier creates a new rollup data querier instance
func NewRollupDataQuerier(
	ctx context.Context,
	l1Config ethermanconfig.L1NetworkConfig,
	ethClient aggkittypes.BaseEthereumClienter,
	rollupManagerFactory RollupManagerFactoryFunc,
) (*RollupDataQuerier, error) {
	rollupManagerSC, err := bindRollupManagerContract(l1Config.RollupManagerAddr, ethClient, rollupManagerFactory)
	if err != nil {
		return nil, err
	}

	rollupID, err := fetchRollupID(rollupManagerSC, l1Config.RollupAddr)
	if err != nil {
		return nil, err
	}

	log.Infof("retrieved rollup id %d from rollup manager", rollupID)

	var rollupManagerUpgradedMap map[uint8]uint64
	if _, exists := preEtrogDeployedRollups[rollupID]; exists {
		rollupManagerUpgradedMap, err = populateAgglayerManagerInitializedMapFn(
			ctx, rollupManagerSC, ethClient, l1Config.RollupManagerCreationBlock, l1Config.BlocksChunkSize)
		if err != nil {
			return nil, fmt.Errorf("failed to populate agglayer manager initialized map: %w", err)
		}
	}

	return &RollupDataQuerier{
		rollupManagerSC:          rollupManagerSC,
		rollupManagerUpgradedMap: rollupManagerUpgradedMap,
		RollupID:                 rollupID,
	}, nil
}

// bindRollupManagerContract creates a RollupManager smart contract binding using the provided factory function.
// It takes a contract address and an Ethereum client, and returns an initialized RollupManagerContract instance.
// Returns an error if the contract binding cannot be created.
func bindRollupManagerContract(
	addr common.Address,
	client aggkittypes.BaseEthereumClienter,
	factory RollupManagerFactoryFunc,
) (RollupManagerContract, error) {
	contract, err := factory(addr, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create rollup manager contract binding: %w", err)
	}
	return contract, nil
}

// fetchRollupID reads the rollup id from rollup manager contract based on provided rollup address
func fetchRollupID(rm RollupManagerContract, rollupAddr common.Address) (uint32, error) {
	rollupID, err := rm.RollupAddressToID(&bind.CallOpts{Pending: false}, rollupAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to retrieve rollup id from rollup manager contract: %w", err)
	}

	if rollupID == 0 {
		return 0, fmt.Errorf("%w: (check the rollup address %s)", ErrInvalidRollupID, rollupAddr)
	}

	return rollupID, nil
}

// populateAgglayerManagerInitializedMap populates a map of agglayer manager initialized events
// with version as key and block number as value
func populateAgglayerManagerInitializedMap(
	ctx context.Context,
	rollupManager RollupManagerContract,
	client aggkittypes.BaseEthereumClienter,
	startBlock, blocksChunkSize uint64,
) (map[uint8]uint64, error) {
	// Get the latest block number to define chunk boundaries
	latestBlock, err := client.BlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest block header: %w", err)
	}

	res := make(map[uint8]uint64)

	for startBlock <= latestBlock {
		end := min(startBlock+blocksChunkSize-1, latestBlock)

		filterOpts := &bind.FilterOpts{
			Start:   startBlock,
			End:     &end,
			Context: ctx,
		}

		it, err := rollupManager.FilterInitialized(filterOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to filter Initialized events (chunk %d-%d): %w", startBlock, end, err)
		}

		for it.Next() {
			res[it.Event.Version] = it.Event.Raw.BlockNumber
		}

		if err := it.Close(); err != nil {
			return nil, fmt.Errorf("failed to close iterator (chunk %d-%d): %w", startBlock, end, err)
		}

		startBlock = end + 1
	}

	if len(res) == 0 {
		return nil, errors.New("no Initialized events found")
	}

	return res, nil
}

// GetRollupChainID returns rollup chain id (L2 network)
func (r *RollupDataQuerier) GetRollupChainID() (uint64, error) {
	rollupData, err := r.GetRollupData(nil)
	if err != nil {
		return 0, err
	}

	if rollupData.ChainID == 0 {
		return 0, ErrInvalidChainID
	}

	log.Infof("rollup chain id (read from rollup manager): %d", rollupData.ChainID)
	return rollupData.ChainID, nil
}

// GetRollupData returns rollup data based on the provided rollup id
func (r *RollupDataQuerier) GetRollupData(blockNumber *big.Int) (
	agglayermanager.AgglayerManagerRollupDataReturn, error) {
	rollupData, err := r.rollupManagerSC.RollupIDToRollupData(
		&bind.CallOpts{
			Pending:     false,
			BlockNumber: blockNumber,
		}, r.RollupID)
	if err != nil {
		log.Debug("error from rollupManager: ", err)
		return agglayermanager.AgglayerManagerRollupDataReturn{},
			fmt.Errorf("failed to retrieve rollup data for rollup id %d: %w", r.RollupID, err)
	}

	return rollupData, nil
}

// GetAgglayerManagerUpgradeBlock returns the rollup manager upgrade block for the given version ID.
// If the version ID is not found, it returns false.
func (r *RollupDataQuerier) GetUpgradeBlock(ctx context.Context, versionID uint8) uint64 {
	return r.rollupManagerUpgradedMap[versionID]
}
