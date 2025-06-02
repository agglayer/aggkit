package etherman

import (
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/banana/polygonrollupmanager"
	"github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// L1Config represents the configuration of the network used in L1
type L1Config struct {
	// Chain ID of the L1 network
	L1ChainID uint64 `json:"chainId" mapstructure:"ChainID"`
	// ZkEVMAddr Address of the L1 contract polygonZkEVMAddress
	ZkEVMAddr common.Address `json:"polygonZkEVMAddress" mapstructure:"ZkEVMAddr"`
	// RollupManagerAddr Address of the L1 contract
	RollupManagerAddr common.Address `json:"polygonRollupManagerAddress" mapstructure:"RollupManagerAddr"`
	// PolAddr Address of the L1 Pol token Contract
	PolAddr common.Address `json:"polTokenAddress" mapstructure:"PolAddr"`
	// GlobalExitRootManagerAddr Address of the L1 GlobalExitRootManager contract
	GlobalExitRootManagerAddr common.Address `json:"polygonZkEVMGlobalExitRootAddress" mapstructure:"GlobalExitRootManagerAddr"` //nolint:lll
}

// Client is a simple implementation of EtherMan.
type Client struct {
	EthClient aggkittypes.BaseEthereumClienter

	rollupManagerSC *polygonrollupmanager.Polygonrollupmanager
	RollupID        uint32

	l1Cfg config.L1Config
}

// NewClient creates a new etherman.
func NewClient(cfg config.Config, l1Config config.L1Config) (*Client, error) {
	// Connect to ethereum node
	ethClient, err := ethclient.Dial(cfg.EthermanConfig.URL)
	if err != nil {
		log.Errorf("error connecting to %s: %+v", cfg.EthermanConfig.URL, err)

		return nil, err
	}

	rollupManagerSC, err := polygonrollupmanager.NewPolygonrollupmanager(l1Config.RollupManagerAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create rollup manager contract binding: %w", err)
	}

	// Populate rollup id
	rollupID, err := rollupManagerSC.RollupAddressToID(&bind.CallOpts{Pending: false}, l1Config.ZkEVMAddr)
	if err != nil {
		log.Errorf("failed to retrieve rollup id from rollup manager contract: %+v", err)

		return nil, err
	}
	if rollupID == 0 {
		return nil, fmt.Errorf("invalid rollup id value (%d). Check if the rollup contract  address is correct %s",
			rollupID, l1Config.ZkEVMAddr)
	}
	log.Infof("retrieved rollup id %d from rollup manager", rollupID)

	return &Client{
		EthClient:       ethClient,
		rollupManagerSC: rollupManagerSC,
		RollupID:        rollupID,
		l1Cfg:           l1Config,
	}, nil
}

// GetL2ChainID returns L2 Chain ID
func (c *Client) GetL2ChainID() (uint64, error) {
	// TODO: @Stefan-Ethernal Check if we can invoke the eth_chainId endpoint
	rollupData, err := c.rollupManagerSC.RollupIDToRollupData(
		&bind.CallOpts{Pending: false},
		c.RollupID,
	)
	log.Debug("chainID read from rollupManager: ", rollupData.ChainID)
	if err != nil {
		log.Debug("error from rollupManager: ", err)

		return 0, err
	} else if rollupData.ChainID == 0 {
		return 0, fmt.Errorf("error: chainID received is 0")
	}

	return rollupData.ChainID, nil
}
