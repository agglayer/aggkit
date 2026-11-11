package chaingersender

import (
	"context"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	"github.com/agglayer/aggkit/aggoracle/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

const insertGERFuncName = "insertGlobalExitRoot"

type EVMConfig struct {
	GlobalExitRootL2Addr   common.Address      `mapstructure:"GlobalExitRootL2"`
	AggOracleCommitteeAddr common.Address      `mapstructure:"AggOracleCommitteeAddr"`
	GasOffset              uint64              `mapstructure:"GasOffset"`
	WaitPeriodMonitorTx    cfgtypes.Duration   `mapstructure:"WaitPeriodMonitorTx"`
	EthTxManager           ethtxmanager.Config `mapstructure:"EthTxManager"`
}

// GERMode represents the mode of GER submission
type GERMode string

const (
	// DirectInjectionMode injects the GER with a single tx sent by the GER updater (an EOA)
	DirectInjectionMode GERMode = "direct_injection"
	// AggOracleCommitteeV2Mode injects the GER, with the signatures of the GER validators,
	// on the AggOracleCommittee v2
	AggOracleCommitteeV2Mode GERMode = "aggoracle_committee_v2"
)

// EVMChainGERSender injects the GERs directly into the L2 GER manager, with a transaction sent by the GER updater
// (an EOA)
type EVMChainGERSender struct {
	logger *log.Logger

	// L2 GER Manager
	l2GERManager     types.L2GERManagerContract
	l2GERManagerAddr common.Address
	l2GERManagerAbi  *abi.ABI

	ethTxMan            types.EthTxManager
	gasOffset           uint64
	waitPeriodMonitorTx time.Duration
}

// NewEVMChainGERSender creates a sender that injects the GERs directly into the L2 GER manager. The sender of the
// transactions (the account of the EthTxManager) must be the GER updater of the L2 GER manager.
// For the AggOracleCommittee v2 use NewCommitteeV2Sender.
func NewEVMChainGERSender(
	logger *log.Logger,
	cfg EVMConfig,
	l2GERManager types.L2GERManagerContract,
	ethTxMan types.EthTxManager,
) (*EVMChainGERSender, error) {
	l2GERAbi, err := agglayergerl2.Agglayergerl2MetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve GER L2 manager ABI: %w", err)
	}

	if err := validateGERSender(ethTxMan.From(), l2GERManager, cfg.GlobalExitRootL2Addr); err != nil {
		return nil, err
	}
	logger.Infof("EVMChainGERSender initialized in %s mode", DirectInjectionMode)

	return &EVMChainGERSender{
		logger:              logger,
		l2GERManager:        l2GERManager,
		l2GERManagerAddr:    cfg.GlobalExitRootL2Addr,
		l2GERManagerAbi:     l2GERAbi,
		ethTxMan:            ethTxMan,
		gasOffset:           cfg.GasOffset,
		waitPeriodMonitorTx: cfg.WaitPeriodMonitorTx.Duration,
	}, nil
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
	isGERInjected, err := c.IsGERInjected(ger)
	if err != nil {
		return fmt.Errorf("error checking if GER (%s) is already injected: %w", ger, err)
	}

	if isGERInjected {
		c.logger.Debugf("GER (%s) is already injected", ger.Hex())
		return nil
	}
	return c.submitTransaction(ctx, &c.l2GERManagerAddr, c.l2GERManagerAbi, insertGERFuncName, ger, "inject")
}

// submitTransaction packs the call to funcName(ger) and submits it, waiting until it is mined
func (c *EVMChainGERSender) submitTransaction(
	ctx context.Context,
	targetAddr *common.Address,
	abi *abi.ABI,
	funcName string,
	ger common.Hash,
	action string,
) error {
	// Pack the function call
	txInput, err := abi.Pack(funcName, ger)
	if err != nil {
		return fmt.Errorf("failed to pack %s call: %w", funcName, err)
	}

	submitter := &txSubmitter{
		logger:              c.logger,
		ethTxMan:            c.ethTxMan,
		gasOffset:           c.gasOffset,
		waitPeriodMonitorTx: c.waitPeriodMonitorTx,
	}
	return submitter.submitAndWait(ctx, targetAddr, txInput, action, ger)
}

// ProcessGER injects the GER into the L2 GER manager, if it is not injected yet
func (c *EVMChainGERSender) ProcessGER(ctx context.Context, ger common.Hash) error {
	return c.InjectGER(ctx, ger)
}

// validateGERSender validates whether the provided GER sender is allowed to send and remove GERs
func validateGERSender(gerSender common.Address,
	l2GERManagerSC types.L2GERManagerContract, l2GERManagerAddr common.Address) error {
	zeroAddr := common.Address{}
	gerUpdater, err := l2GERManagerSC.GlobalExitRootUpdater(nil)
	if err != nil {
		return fmt.Errorf("failed to retrieve GER updater address from GER L2 manager (SC address %s): %w",
			l2GERManagerAddr, err)
	}

	if gerUpdater != zeroAddr && gerSender != gerUpdater {
		return fmt.Errorf("invalid GER sender provided (in the EthTxManager configuration), "+
			"and it is not allowed to update GERs. Expected GER updater by the L2 GER manager contract (SC address: %s): %s",
			l2GERManagerAddr, gerUpdater)
	}

	return nil
}
