package chaingersender

import (
	"context"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggoraclemanager"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/globalexitrootmanagerl2sovereignchain"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/aggoracle/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

const insertGERFuncName = "insertGlobalExitRoot"

type EVMConfig struct {
	GlobalExitRootL2Addr common.Address      `mapstructure:"GlobalExitRootL2"`
	AggOracleManagerAddr common.Address      `mapstructure:"AggOracleManagerAddr"`
	GasOffset            uint64              `mapstructure:"GasOffset"`
	WaitPeriodMonitorTx  cfgtypes.Duration   `mapstructure:"WaitPeriodMonitorTx"`
	EthTxManager         ethtxmanager.Config `mapstructure:"EthTxManager"`
}

type EVMChainGERSender struct {
	logger *log.Logger

	l2GERManager     types.L2GERManagerContract
	l2GERManagerAddr common.Address
	l2GERManagerAbi  *abi.ABI

	aggOracleManager     types.AggOracleManagerContract
	aggOracleManagerAddr common.Address
	aggOracleManagerAbi  *abi.ABI

	ethTxMan            types.EthTxManager
	gasOffset           uint64
	waitPeriodMonitorTx time.Duration
}

func NewEVMChainGERSender(
	logger *log.Logger,
	l2GERManagerAddr common.Address,
	aggOracleManagerAddr common.Address,
	l2Client aggkittypes.BaseEthereumClienter,
	ethTxMan types.EthTxManager,
	gasOffset uint64,
	waitPeriodMonitorTx time.Duration,
	enableAggOracleQuorum bool,
) (*EVMChainGERSender, error) {
	l2GERManager, err := globalexitrootmanagerl2sovereignchain.NewGlobalexitrootmanagerl2sovereignchain(
		l2GERManagerAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("failed to create binding for GER L2 manager (SC address: %s): %w", l2GERManagerAddr, err)
	}

	l2GERAbi, err := globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchainMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve GER L2 manager ABI: %w", err)
	}

	// Initialize AggOracleManager fields only when quorum is enabled
	var aggOracleManager types.AggOracleManagerContract
	var aggOracleManagerAbi *abi.ABI

	if enableAggOracleQuorum {
		// Create AggOracleManager contract binding
		aggOracleManager, err = aggoraclemanager.NewAggoraclemanager(aggOracleManagerAddr, l2Client)
		if err != nil {
			return nil, fmt.Errorf("failed to create binding for AggOracleManager (SC address: %s): %w", aggOracleManagerAddr, err)
		}

		// Get the ABI for AggOracleManager
		aggOracleManagerAbi, err = aggoraclemanager.AggoraclemanagerMetaData.GetAbi()
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve AggOracleManager ABI: %w", err)
		}

		// Validate GER proposer when quorum is enabled
		if err := validateGERProposer(ethTxMan.From(), aggOracleManager); err != nil {
			return nil, err
		}
	} else {
		// Validate GER sender when quorum is disabled
		if err := validateGERSender(ethTxMan.From(), l2GERManager); err != nil {
			return nil, err
		}
	}

	return &EVMChainGERSender{
		logger:               logger,
		l2GERManager:         l2GERManager,
		l2GERManagerAddr:     l2GERManagerAddr,
		l2GERManagerAbi:      l2GERAbi,
		aggOracleManager:     aggOracleManager,
		aggOracleManagerAddr: aggOracleManagerAddr,
		aggOracleManagerAbi:  aggOracleManagerAbi,
		ethTxMan:             ethTxMan,
		gasOffset:            gasOffset,
		waitPeriodMonitorTx:  waitPeriodMonitorTx,
	}, nil
}

// validateGERSender validates whether the provided GER sender is allowed to send and remove GERs
func validateGERSender(gerSender common.Address, l2GERManagerSC types.L2GERManagerContract) error {
	zeroAddr := common.Address{}
	gerUpdater, err := l2GERManagerSC.GlobalExitRootUpdater(nil)
	if err != nil {
		return fmt.Errorf("failed to retrieve GER updater address from GER L2 manager: %w", err)
	}

	if gerUpdater != zeroAddr && gerSender != gerUpdater {
		return fmt.Errorf("invalid GER sender provided (in the EthTxManager configuration), "+
			"and it is not allowed to update GERs. Expected GER updater by the L2 GER manager contract: %s", gerUpdater)
	}

	return nil
}

// validateGERProposer validates whether the provided GER proposer is allowed to propose GERs
func validateGERProposer(gerProposer common.Address, aggOracleManagerSC types.AggOracleManagerContract) error {
	// Check if the address is an oracle member by trying to get their index
	// If the address is not an oracle member, getAggOracleMemberIndex will revert with OracleMemberNotFound
	_, err := aggOracleManagerSC.GetAggOracleMemberIndex(&bind.CallOpts{Pending: false}, gerProposer)
	if err != nil {
		return fmt.Errorf("invalid GER proposer provided (address: %s), not an oracle member: %w", gerProposer.Hex(), err)
	}

	return nil
}

// IsGERInjected checks if the provided global exit root is already injected into the
// L2 GER manager contract by querying the map
func (c *EVMChainGERSender) IsGERInjected(ger common.Hash) (bool, error) {
	gerIndex, err := c.l2GERManager.GlobalExitRootMap(&bind.CallOpts{Pending: false}, ger)
	if err != nil {
		return false, fmt.Errorf("failed to check if global exit root is injected %s: %w", ger, err)
	}

	return gerIndex.Cmp(common.Big0) == 1, nil
}

// InjectGER injects the provided global exit root into the L2 GER manager contract
func (c *EVMChainGERSender) InjectGER(ctx context.Context, ger common.Hash) error {
	ticker := time.NewTicker(c.waitPeriodMonitorTx)
	defer ticker.Stop()

	updateGERTxInput, err := c.l2GERManagerAbi.Pack(insertGERFuncName, ger)
	if err != nil {
		return err
	}

	id, err := c.ethTxMan.Add(ctx, &c.l2GERManagerAddr, common.Big0, updateGERTxInput, c.gasOffset, nil)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			c.logger.Infof("context cancelled handled in InjectGER for tx %s", id.Hex())
			return nil

		case <-ticker.C:
			c.logger.Debugf("waiting for tx %s to be mined", id.Hex())
			res, err := c.ethTxMan.Result(ctx, id)
			if err != nil {
				c.logger.Errorf("failed to check the transaction %s status: %s", id.Hex(), err)
				return err
			}

			switch res.Status {
			case ethtxtypes.MonitoredTxStatusCreated,
				ethtxtypes.MonitoredTxStatusSent:
				continue
			case ethtxtypes.MonitoredTxStatusFailed:
				return fmt.Errorf("inject GER tx %s failed", id.Hex())
			case ethtxtypes.MonitoredTxStatusMined,
				ethtxtypes.MonitoredTxStatusSafe,
				ethtxtypes.MonitoredTxStatusFinalized:
				c.logger.Debugf("inject GER tx %s was successfully mined at block %d", id.Hex(), res.MinedAtBlockNumber)

				return nil
			default:
				c.logger.Error("unexpected tx status:", res.Status)
			}

		}

	}
}

// ProposeGER proposes the provided global exit root to the AggOracleManager contract
func (c *EVMChainGERSender) ProposeGER(ctx context.Context, ger common.Hash) error {
	// Check if AggOracleManager is initialized (only when quorum is enabled)
	if c.aggOracleManager == nil || c.aggOracleManagerAbi == nil {
		return fmt.Errorf("AggOracleManager not initialized - enableAggOracleQuorum must be true to use ProposeGER")
	}

	ticker := time.NewTicker(c.waitPeriodMonitorTx)
	defer ticker.Stop()

	// Pack the proposeGlobalExitRoot function call
	proposeGERTxInput, err := c.aggOracleManagerAbi.Pack("proposeGlobalExitRoot", ger)
	if err != nil {
		return fmt.Errorf("failed to pack proposeGlobalExitRoot call: %w", err)
	}

	// Add the transaction to the transaction manager
	id, err := c.ethTxMan.Add(ctx, &c.aggOracleManagerAddr, common.Big0, proposeGERTxInput, c.gasOffset, nil)
	if err != nil {
		return fmt.Errorf("failed to add propose GER transaction: %w", err)
	}

	c.logger.Infof("propose GER transaction submitted with ID: %s", id.Hex())

	// Monitor the transaction status
	for {
		select {
		case <-ctx.Done():
			c.logger.Infof("context cancelled handled in ProposeGER for tx %s", id.Hex())
			return nil

		case <-ticker.C:
			c.logger.Debugf("waiting for propose GER tx %s to be mined", id.Hex())
			res, err := c.ethTxMan.Result(ctx, id)
			if err != nil {
				c.logger.Errorf("failed to check the propose GER transaction %s status: %s", id.Hex(), err)
				return err
			}

			switch res.Status {
			case ethtxtypes.MonitoredTxStatusCreated,
				ethtxtypes.MonitoredTxStatusSent:
				continue
			case ethtxtypes.MonitoredTxStatusFailed:
				return fmt.Errorf("propose GER tx %s failed", id.Hex())
			case ethtxtypes.MonitoredTxStatusMined,
				ethtxtypes.MonitoredTxStatusSafe,
				ethtxtypes.MonitoredTxStatusFinalized:
				c.logger.Debugf("propose GER tx %s was successfully mined at block %d", id.Hex(), res.MinedAtBlockNumber)
				return nil
			default:
				c.logger.Error("unexpected tx status:", res.Status)
			}
		}
	}
}
