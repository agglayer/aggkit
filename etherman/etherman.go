package etherman

import (
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonrollupmanager"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// RollupManagerContract is an abstraction for RollupManager smart contract
type RollupManagerContract interface {
	RollupIDToRollupData(opts *bind.CallOpts, rollupID uint32) (polygonrollupmanager.PolygonRollupManagerRollupDataReturn, error)
	RollupAddressToID(opts *bind.CallOpts, rollupAddress common.Address) (uint32, error)
}

// DialFunc is callback function that creates BaseEthereumClienter, used to interact with Ethereum nodes
type DialFunc func(url string) (aggkittypes.BaseEthereumClienter, error)

// RollupManagerFactoryFunc is a callback function that creates RollupManager contrat instance
type RollupManagerFactoryFunc func(rollupAddress common.Address,
	client aggkittypes.BaseEthereumClienter) (RollupManagerContract, error)

// Client is a simple implementation of Etherman.
type Client struct {
	rollupManagerSC RollupManagerContract
	RollupID        uint32
}

// NewClient creates a new etherman client instance
func NewClient(l1Config config.L1NetworkConfig,
	ethClientFactory DialFunc,
	rollupManagerFactory RollupManagerFactoryFunc,
) (*Client, error) {
	ethClient, err := ethClientFactory(l1Config.URL)
	if err != nil {
		log.Errorf("error connecting to %s: %v", l1Config.URL, err)
		return nil, err
	}

	rollupManagerSC, err := rollupManagerFactory(l1Config.RollupManagerAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create rollup manager contract binding: %w", err)
	}

	// Populate rollup id
	rollupID, err := getRollupID(rollupManagerSC, l1Config.RollupAddr)
	if err != nil {
		return nil, err
	}
	log.Infof("retrieved rollup id %d from rollup manager", rollupID)

	return &Client{
		rollupManagerSC: rollupManagerSC,
		RollupID:        rollupID,
	}, nil
}

// getRollupID reads the rollup id from rollup manager contract based on provided rollup address
func getRollupID(rollupManagerSC RollupManagerContract, rollupAddr common.Address) (uint32, error) {
	rollupID, err := rollupManagerSC.RollupAddressToID(&bind.CallOpts{Pending: false}, rollupAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to retrieve rollup id from rollup manager contract: %+w", err)
	}

	if rollupID == 0 {
		return 0, fmt.Errorf("invalid rollup id value (%d). Check if the rollup contract address is correct %s",
			rollupID, rollupAddr)
	}

	return rollupID, nil
}

// GetL2ChainID returns L2 Chain ID
func (c *Client) GetL2ChainID() (uint64, error) {
	rollupData, err := c.rollupManagerSC.RollupIDToRollupData(&bind.CallOpts{Pending: false}, c.RollupID)
	log.Infof("rollup chain id (read from rollup manager): %d", rollupData.ChainID)
	if err != nil {
		log.Debug("error from rollupManager: ", err)

		return 0, err
	} else if rollupData.ChainID == 0 {
		return 0, fmt.Errorf("error: chainID received is 0")
	}

	return rollupData.ChainID, nil
}
