package etherman

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrInvalidRollupID = errors.New("invalid rollup id (0)")
	ErrInvalidChainID  = errors.New("invalid chain id (0)")
)

// RollupManagerContract is an abstraction for RollupManager smart contract
type RollupManagerContract interface {
	RollupIDToRollupData(opts *bind.CallOpts, rollupID uint32) (
		agglayermanager.AgglayerManagerRollupDataReturn, error)
	RollupAddressToID(opts *bind.CallOpts, rollupAddress common.Address) (uint32, error)
}

// mockery:ignore
// RollupManagerFactoryFunc is a callback function that creates RollupManager contrat instance
type RollupManagerFactoryFunc func(rollupAddress common.Address,
	client aggkittypes.BaseEthereumClienter) (RollupManagerContract, error)

// RollupDataQuerier is a simple implementation of Etherman.
type RollupDataQuerier struct {
	rollupManagerSC RollupManagerContract
	RollupID        uint32
}

// NewRollupDataQuerier creates a new rollup data querier instance
func NewRollupDataQuerier(
	l1Config config.L1NetworkConfig,
	ethClient aggkittypes.BaseEthereumClienter,
	rollupManagerFactory RollupManagerFactoryFunc,
) (*RollupDataQuerier, error) {
	rmContract, err := bindRollupManagerContract(l1Config.RollupManagerAddr, ethClient, rollupManagerFactory)
	if err != nil {
		return nil, err
	}

	rollupID, err := fetchRollupID(rmContract, l1Config.RollupAddr)
	if err != nil {
		return nil, err
	}

	log.Infof("retrieved rollup id %d from rollup manager", rollupID)

	return &RollupDataQuerier{
		rollupManagerSC: rmContract,
		RollupID:        rollupID,
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
