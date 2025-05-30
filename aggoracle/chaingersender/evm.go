package chaingersender

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/globalexitrootmanagerl2sovereignchain"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/aggoracle/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

const insertGERFuncName = "insertGlobalExitRoot"

type EVMConfig struct {
	GlobalExitRootL2Addr common.Address      `mapstructure:"GlobalExitRootL2"`
	GasOffset            uint64              `mapstructure:"GasOffset"`
	WaitPeriodMonitorTx  cfgtypes.Duration   `mapstructure:"WaitPeriodMonitorTx"`
	EthTxManager         ethtxmanager.Config `mapstructure:"EthTxManager"`
}

type EVMChainGERSender struct {
	logger *log.Logger

	l2GERManager     types.L2GERManagerContract
	l2GERManagerAddr common.Address
	l2GERManagerAbi  *abi.ABI

	ethTxMan            types.EthTxManager
	gasOffset           uint64
	waitPeriodMonitorTx time.Duration
}

func NewEVMChainGERSender(
	logger *log.Logger,
	l2GERManagerAddr common.Address,
	l2Client types.EthClienter,
	ethTxMan types.EthTxManager,
	gasOffset uint64,
	waitPeriodMonitorTx time.Duration,
) (*EVMChainGERSender, error) {
	l2GERManager, err := globalexitrootmanagerl2sovereignchain.NewGlobalexitrootmanagerl2sovereignchain(
		l2GERManagerAddr, l2Client)
	if err != nil {
		return nil, err
	}

	if err := validateGERSender(ethTxMan, l2GERManager); err != nil {
		return nil, err
	}

	l2GERAbi, err := globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchainMetaData.GetAbi()
	if err != nil {
		return nil, err
	}

	return &EVMChainGERSender{
		logger:              logger,
		l2GERManager:        l2GERManager,
		l2GERManagerAddr:    l2GERManagerAddr,
		l2GERManagerAbi:     l2GERAbi,
		ethTxMan:            ethTxMan,
		gasOffset:           gasOffset,
		waitPeriodMonitorTx: waitPeriodMonitorTx,
	}, nil
}

// validateGERSender validates whether the provided GER sender is allowed to send and remove GERs
func validateGERSender(txManager types.EthTxManager,
	l2GERManagerSC *globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchain) error {
	gerUpdater, err := l2GERManagerSC.GlobalExitRootUpdater(nil)
	if err != nil {
		return err
	}

	gerRemover, err := l2GERManagerSC.GlobalExitRootRemover(nil)
	if err != nil {
		return err
	}

	// TODO: validate only in case the zero address is provided to SC
	if txManager.From() != gerUpdater {
		return errors.New("invalid GER sender provided (EthTxManager), it is not allowed to update GERs")
	}

	if txManager.From() != gerRemover {
		return errors.New("invalid GER sender provided (EthTxManager), it is not allowed to remove GERs")
	}

	return nil
}

func (c *EVMChainGERSender) IsGERInjected(ger common.Hash) (bool, error) {
	gerIndex, err := c.l2GERManager.GlobalExitRootMap(&bind.CallOpts{Pending: false}, ger)
	if err != nil {
		return false, fmt.Errorf("failed to check if global exit root is injected %s: %w", ger, err)
	}

	return gerIndex.Cmp(common.Big0) == 1, nil
}

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
			c.logger.Infof("context cancelled")
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
