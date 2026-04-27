package chaingersender

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggoraclecommittee"
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
	insertGERFuncName      = "insertGlobalExitRoot"
	proposeGERFuncName     = "proposeGlobalExitRoot"
	updateExitRootFuncName = "updateExitRoot"
)

type EVMConfig struct {
	GlobalExitRootL2Addr   common.Address      `mapstructure:"GlobalExitRootL2"`
	AggOracleCommitteeAddr common.Address      `mapstructure:"AggOracleCommitteeAddr"`
	GasOffset              uint64              `mapstructure:"GasOffset"`
	WaitPeriodMonitorTx    cfgtypes.Duration   `mapstructure:"WaitPeriodMonitorTx"`
	EthTxManager           ethtxmanager.Config `mapstructure:"EthTxManager"`
	// UseUpdateExitRoot switches GER submission from the combined-hash path
	// (`insertGlobalExitRoot(bytes32)`) to the two-root path
	// (`updateExitRoot(bytes32 rollup, bytes32 mainnet)`). On sovereign chains
	// that can't reverse keccak(mainnet||rollup), the receiving service
	// otherwise has to read live L1 state to decompose — which orphans any
	// GER whose pair has already advanced. Forwarding both roots skips that
	// race. The L2 GER manager contract accepts both calls.
	UseUpdateExitRoot bool `mapstructure:"UseUpdateExitRoot"`
}

// GERMode represents the mode of GER submission
type GERMode string

const (
	DirectInjectionMode      GERMode = "direct_injection"
	DirectUpdateExitRootMode GERMode = "direct_update_exit_root"
	AggOracleCommitteeMode   GERMode = "aggoracle_committee"
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
	// Determine mode based on configuration. Committee mode wins over the
	// two-root flag because the committee uses a separate contract path.
	mode := DirectInjectionMode
	switch {
	case enableAggOracleCommittee && cfg.AggOracleCommitteeAddr != aggkitcommon.ZeroAddress:
		mode = AggOracleCommitteeMode
	case cfg.UseUpdateExitRoot:
		mode = DirectUpdateExitRootMode
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
	case DirectInjectionMode, DirectUpdateExitRootMode:
		// Same permission gate: the caller must be authorised as the GER
		// updater on the L2 contract. The two direct modes differ only in
		// which ABI method they invoke.
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

// updateExitRootTwoRootSelector is the 4-byte function selector for
// `updateExitRoot(bytes32 newRollupExitRoot, bytes32 newMainnetExitRoot)` —
// keccak256("updateExitRoot(bytes32,bytes32)")[:4] = 0x736ca7f4.
//
// Constructed manually rather than via abi.Pack because the
// cdk-contracts-tooling binding for `agglayergerl2` currently only exposes
// the 1-arg `updateExitRoot(bytes32)` overload. The 2-arg form exists on
// Miden's sovereign L2 GER manager (see
// `miden-agglayer/src/ger.rs::updateExitRoot` and
// agglayer-contracts GlobalExitRootManagerL2SovereignChain.sol) and is what
// actually resolves the decomposition race — we pack the calldata by hand
// instead of waiting for the Go binding to catch up.
var updateExitRootTwoRootSelector = [4]byte{0x73, 0x6c, 0xa7, 0xf4}

// hashLen is the length in bytes of an EVM bytes32/keccak256 word.
const hashLen = 32

// InjectExitRoots pushes the uncombined (rollup, mainnet) exit root pair via
// updateExitRoot(bytes32 newRollupExitRoot, bytes32 newMainnetExitRoot) on the
// L2 GER manager. Preferred on sovereign chains that can't reverse
// keccak(mainnet||rollup) — forwarding both roots eliminates the decomposition
// race (RD-862).
func (c *EVMChainGERSender) InjectExitRoots(
	ctx context.Context, ger, mainnetExitRoot, rollupExitRoot common.Hash,
) error {
	isGERInjected, err := c.IsGERInjected(ger)
	if err != nil {
		return fmt.Errorf("error checking if GER (%s) is already injected: %w", ger, err)
	}
	if isGERInjected {
		c.logger.Debugf("GER (%s) is already injected", ger.Hex())
		return nil
	}

	// 4-byte selector + 32-byte rollupExitRoot + 32-byte mainnetExitRoot.
	// Arg order matches the Miden proxy's sol! definition (rollup first).
	txInput := make([]byte, 0, len(updateExitRootTwoRootSelector)+2*hashLen)
	txInput = append(txInput, updateExitRootTwoRootSelector[:]...)
	txInput = append(txInput, rollupExitRoot.Bytes()...)
	txInput = append(txInput, mainnetExitRoot.Bytes()...)
	return c.submitPackedTransaction(ctx, &c.l2GERManagerAddr, txInput, ger, "update-exit-root")
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
	// Pack the function call
	txInput, err := abi.Pack(funcName, ger)
	if err != nil {
		return fmt.Errorf("failed to pack %s call: %w", funcName, err)
	}
	return c.submitPackedTransaction(ctx, targetAddr, txInput, ger, action)
}

// submitPackedTransaction submits already-packed calldata and monitors the tx.
// Split out from submitTransaction so callers that need multi-argument packing
// (e.g. updateExitRoot) can reuse the monitoring logic.
func (c *EVMChainGERSender) submitPackedTransaction(
	ctx context.Context,
	targetAddr *common.Address,
	txInput []byte,
	ger common.Hash,
	action string,
) error {
	ticker := time.NewTicker(c.waitPeriodMonitorTx)
	defer ticker.Stop()

	// Add the transaction to the transaction manager
	id, err := c.ethTxMan.Add(ctx, targetAddr, common.Big0, txInput, c.gasOffset, nil)
	if err == nil {
		c.logger.Infof("%s GER transaction submitted with ID: %s. GER: %s", action, id.Hex(), ger.Hex())
	} else if !errors.Is(err, ethtxmanager.ErrAlreadyExists) {
		return fmt.Errorf("failed to add %s GER transaction: %w", action, err)
	}
	if err != nil {
		c.logger.Infof("%s GER transaction already exists in monitoring DB with ID: %s. GER: %s", action, id.Hex(), ger.Hex())
	}

	c.logger.Debugf("monitoring every %s", c.waitPeriodMonitorTx)

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
	case DirectUpdateExitRootMode:
		// Callers should route via ProcessGERWithRoots; this arm exists only
		// as a defensive guard if an upstream forgets to check UsesTwoRootMode.
		return fmt.Errorf("%s mode requires ProcessGERWithRoots; got ProcessGER", c.mode)
	default:
		return fmt.Errorf("unknown GER mode: %s", c.mode)
	}
}

// UsesTwoRootMode reports whether this sender is configured to forward the
// uncombined (mainnet, rollup) exit root pair rather than the combined GER.
func (c *EVMChainGERSender) UsesTwoRootMode() bool {
	return c.mode == DirectUpdateExitRootMode
}

// ProcessGERWithRoots dispatches a GER alongside its uncombined exit root
// pair. Only meaningful for DirectUpdateExitRootMode — for legacy modes we
// fall through to ProcessGER with just the combined hash.
func (c *EVMChainGERSender) ProcessGERWithRoots(
	ctx context.Context, ger, mainnetExitRoot, rollupExitRoot common.Hash,
) error {
	switch c.mode {
	case DirectUpdateExitRootMode:
		return c.InjectExitRoots(ctx, ger, mainnetExitRoot, rollupExitRoot)
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
