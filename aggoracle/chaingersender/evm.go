package chaingersender

import (
	"context"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/tmp-detailed-claim-event/agglayergerl2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/tmp-detailed-claim-event/aggoraclecommittee"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/aggoracle/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

const (
	insertGERFuncName  = "insertGlobalExitRoot"
	proposeGERFuncName = "proposeGlobalExitRoot"
)

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
	DirectInjectionMode    GERMode = "direct_injection"
	AggOracleCommitteeMode GERMode = "aggoracle_committee"
)

type EVMChainGERSender struct {
	logger *log.Logger
	mode   GERMode

	// L2 GER Manager (always needed for checking if GER is injected)
	l2GERManager     types.L2GERManagerContract
	l2GERManagerAddr common.Address
	l2GERManagerAbi  *abi.ABI

	// AggOracle Committee (only needed for AggOracle committee mode)
	aggOracleCommittee     types.AggOracleCommitteeContract
	aggOracleCommitteeAddr common.Address
	aggOracleCommitteeAbi  *abi.ABI

	// Client for contract bindings
	l2Client aggkittypes.BaseEthereumClienter

	ethTxMan            types.EthTxManager
	gasOffset           uint64
	waitPeriodMonitorTx time.Duration
}

func NewEVMChainGERSender(
	logger *log.Logger,
	cfg EVMConfig,
	l2Client aggkittypes.BaseEthereumClienter,
	l2GERManager types.L2GERManagerContract,
	ethTxMan types.EthTxManager,
	enableAggOracleCommittee bool,
) (*EVMChainGERSender, error) {
	// Determine mode based on configuration
	mode := DirectInjectionMode
	if enableAggOracleCommittee && cfg.AggOracleCommitteeAddr != aggkitcommon.ZeroAddress {
		mode = AggOracleCommitteeMode
	}
	logger.Infof("EVMChainGERSender initialized in %s mode", mode)

	l2GERAbi, err := agglayergerl2.Agglayergerl2MetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve GER L2 manager ABI: %w", err)
	}

	sender := &EVMChainGERSender{
		logger:                 logger,
		mode:                   mode,
		l2GERManager:           l2GERManager,
		l2GERManagerAddr:       cfg.GlobalExitRootL2Addr,
		l2GERManagerAbi:        l2GERAbi,
		aggOracleCommitteeAddr: cfg.AggOracleCommitteeAddr,
		l2Client:               l2Client,
		ethTxMan:               ethTxMan,
		gasOffset:              cfg.GasOffset,
		waitPeriodMonitorTx:    cfg.WaitPeriodMonitorTx.Duration,
	}

	// Initialize and validate mode-specific components
	if err := sender.initializeAndValidateMode(); err != nil {
		return nil, err
	}

	return sender, nil
}

// initializeAndValidateMode initializes mode-specific components and validations
func (c *EVMChainGERSender) initializeAndValidateMode() error {
	switch c.mode {
	case DirectInjectionMode:
		return validateGERSender(c.ethTxMan.From(), c.l2GERManager, c.l2GERManagerAddr)
	case AggOracleCommitteeMode:
		return c.initializeAndValidateAggOracleCommitteeMode()
	default:
		return fmt.Errorf("unknown GER mode: %s", c.mode)
	}
}

func (c *EVMChainGERSender) initializeAndValidateAggOracleCommitteeMode() error {
	// Create AggOracleCommittee contract binding
	aggOracleCommittee, err := aggoraclecommittee.NewAggoraclecommittee(c.aggOracleCommitteeAddr, c.l2Client)
	if err != nil {
		return fmt.Errorf("failed to create binding for AggOracleCommittee (SC address: %s): %w",
			c.aggOracleCommitteeAddr, err)
	}

	// Validate GER proposer
	if err := validateGERProposer(c.ethTxMan.From(), aggOracleCommittee); err != nil {
		return err
	}

	// Get the ABI for AggOracleCommittee
	aggOracleCommitteeAbi, err := aggoraclecommittee.AggoraclecommitteeMetaData.GetAbi()
	if err != nil {
		return fmt.Errorf("failed to retrieve AggOracleCommittee ABI: %w", err)
	}

	_, err = aggOracleCommittee.AggOracleMembers(&bind.CallOpts{Pending: false}, common.Big0)
	if err != nil {
		return fmt.Errorf("failed to retrieve AggOracleCommittee members: %w", err)
	}

	c.aggOracleCommittee = aggOracleCommittee
	c.aggOracleCommitteeAbi = aggOracleCommitteeAbi

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

// IsGERProposed checks if the provided global exit root has already been proposed by the oracle committee member
func (c *EVMChainGERSender) IsGERProposed(ger common.Hash) (bool, error) {
	lastProposedGER, err := c.aggOracleCommittee.AddressToLastProposedGER(
		&bind.CallOpts{Pending: false}, c.ethTxMan.From())
	if err != nil {
		return false, fmt.Errorf("failed to check last proposed GER for oracle committee member %s: %w",
			c.ethTxMan.From(), err)
	}

	lastProposedGERHash := common.Hash(lastProposedGER)
	return lastProposedGERHash == ger, nil
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

// ProposeGER proposes the provided global exit root to the AggOracleCommittee contract
func (c *EVMChainGERSender) ProposeGER(ctx context.Context, ger common.Hash) error {
	isGERInjected, err := c.IsGERInjected(ger)
	if err != nil {
		return fmt.Errorf("error checking if GER (%s) is already injected: %w", ger, err)
	}

	if isGERInjected {
		c.logger.Debugf("GER (%s) is already injected", ger.Hex())
		return nil
	}

	// Check if the GER has already been proposed by the oracle committee member
	isProposed, err := c.IsGERProposed(ger)
	if err != nil {
		return err
	}
	if isProposed {
		c.logger.Infof("GER %s has already been proposed by the aggoracle committee member", ger.Hex())
		return nil
	}

	return c.submitTransaction(ctx, &c.aggOracleCommitteeAddr, c.aggOracleCommitteeAbi, proposeGERFuncName, ger, "propose")
}

// submitTransaction is a generic method to submit and monitor transactions
func (c *EVMChainGERSender) submitTransaction(
	ctx context.Context,
	targetAddr *common.Address,
	abi *abi.ABI,
	funcName string,
	ger common.Hash,
	action string,
) error {
	ticker := time.NewTicker(c.waitPeriodMonitorTx)
	defer ticker.Stop()

	// Pack the function call
	txInput, err := abi.Pack(funcName, ger)
	if err != nil {
		return fmt.Errorf("failed to pack %s call: %w", funcName, err)
	}

	// Add the transaction to the transaction manager
	id, err := c.ethTxMan.Add(ctx, targetAddr, common.Big0, txInput, c.gasOffset, nil)
	if err != nil {
		return fmt.Errorf("failed to add %s GER transaction: %w", action, err)
	}

	c.logger.Infof("%s GER transaction submitted with ID: %s", action, id.Hex())

	// Monitor the transaction status
	for {
		select {
		case <-ctx.Done():
			c.logger.Infof("context cancelled handled in %s for tx %s", action, id.Hex())
			return nil

		case <-ticker.C:
			c.logger.Debugf("waiting for %s GER tx %s to be mined", action, id.Hex())
			res, err := c.ethTxMan.Result(ctx, id)
			if err != nil {
				c.logger.Errorf("failed to check the %s GER transaction %s status: %s", action, id.Hex(), err)
				return err
			}

			switch res.Status {
			case ethtxtypes.MonitoredTxStatusCreated,
				ethtxtypes.MonitoredTxStatusSent:
				continue
			case ethtxtypes.MonitoredTxStatusFailed:
				return fmt.Errorf("%s GER tx %s failed", action, id.Hex())
			case ethtxtypes.MonitoredTxStatusEvicted:
				c.logger.Debugf("%s GER tx %s was evicted", action, id.Hex())
				return nil
			case ethtxtypes.MonitoredTxStatusMined,
				ethtxtypes.MonitoredTxStatusSafe,
				ethtxtypes.MonitoredTxStatusFinalized:
				c.logger.Debugf("%s GER tx %s was successfully mined at block %d", action, id.Hex(), res.MinedAtBlockNumber)
				return nil
			default:
				c.logger.Error("unexpected tx status:", res.Status)
			}
		}
	}
}

func (c *EVMChainGERSender) ProcessGER(ctx context.Context, ger common.Hash) error {
	switch c.mode {
	case DirectInjectionMode:
		return c.InjectGER(ctx, ger)
	case AggOracleCommitteeMode:
		return c.ProposeGER(ctx, ger)
	default:
		return fmt.Errorf("unknown GER mode: %s", c.mode)
	}
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

// validateGERProposer validates whether the provided GER proposer is allowed to propose GERs
func validateGERProposer(gerProposer common.Address, aggOracleCommitteeSC types.AggOracleCommitteeContract) error {
	// Check if the address is an oracle member by trying to get their index
	// If the address is not an oracle member, getAggOracleMemberIndex will revert with OracleMemberNotFound
	_, err := aggOracleCommitteeSC.GetAggOracleMemberIndex(&bind.CallOpts{Pending: false}, gerProposer)
	if err != nil {
		return fmt.Errorf("invalid GER proposer provided (address: %s), not an oracle member: %w", gerProposer.Hex(), err)
	}

	return nil
}
