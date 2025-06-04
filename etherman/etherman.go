package etherman

import (
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/banana/polygonrollupmanager"
	"github.com/agglayer/aggkit/config"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Client is a simple implementation of Etherman.
type Client struct {
	rollupManagerSC *polygonrollupmanager.Polygonrollupmanager
	RollupID        uint32
}

// NewClient creates a new etherman.
func NewClient(l1Config config.L1NetworkConfig) (*Client, error) {
	// Connect to ethereum node
	ethClient, err := ethclient.Dial(l1Config.URL)
	if err != nil {
		log.Errorf("error connecting to %s: %+v", l1Config.URL, err)

		return nil, err
	}

	rollupManagerSC, err := polygonrollupmanager.NewPolygonrollupmanager(l1Config.RollupManagerAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create rollup manager contract binding: %w", err)
	}

	// Populate rollup id
	rollupID, err := rollupManagerSC.RollupAddressToID(&bind.CallOpts{Pending: false}, l1Config.RollupAddr)
	if err != nil {
		log.Errorf("failed to retrieve rollup id from rollup manager contract: %+v", err)

		return nil, err
	}
	if rollupID == 0 {
		return nil, fmt.Errorf("invalid rollup id value (%d). Check if the rollup contract  address is correct %s",
			rollupID, l1Config.RollupAddr)
	}
	log.Infof("retrieved rollup id %d from rollup manager", rollupID)

	return &Client{
		rollupManagerSC: rollupManagerSC,
		RollupID:        rollupID,
	}, nil
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
