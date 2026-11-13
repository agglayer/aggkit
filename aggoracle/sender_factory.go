package aggoracle

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggoraclecommittee"
	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/aggoracle/gervalidator"
	aggoracletypes "github.com/agglayer/aggkit/aggoracle/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// gerSubmission is how the GERs are submitted to the L2, once the configured mode is resolved
type gerSubmission struct {
	// requested is the configured AggOracle.Mode (auto if it is not set)
	requested Mode
	// mode is the sender to use
	mode chaingersender.GERMode
	// committee is the AggOracleCommittee contract (zero for the direct injection)
	committee common.Address
	// reason explains why the mode was selected, to be logged
	reason string
}

// resolveSubmission decides how the GERs are submitted, from the configured Mode:
//   - direct is used as configured, without reading the L2
//   - committee takes the committee from the configuration or, if it is not set, from the GER updater of the
//     L2 GER manager, and requires them to be the same contract
//   - auto inspects the GER updater of the L2 GER manager and detects the mode (see chaingersender.DetectGERMode)
func resolveSubmission(ctx context.Context, cfg Config, caller bind.ContractCaller) (*gerSubmission, error) {
	requested, err := cfg.ResolveMode()
	if err != nil {
		return nil, err
	}
	gerManager := cfg.EVMSender.GlobalExitRootL2Addr
	configuredCommittee := cfg.EVMSender.AggOracleCommitteeAddr

	switch requested {
	case ModeDirect:
		return &gerSubmission{
			requested: requested, mode: chaingersender.DirectInjectionMode,
			reason: "the L2 is not read: the EthTxManager account must be the GER updater",
		}, nil

	case ModeCommittee:
		committee, _, err := chaingersender.ResolveCommitteeAddress(ctx, caller, gerManager, configuredCommittee)
		if err != nil {
			return nil, err
		}
		if committee == aggkitcommon.ZeroAddress {
			return nil, fmt.Errorf("AggOracle.Mode is committee but there is no committee: "+
				"AggOracleCommitteeAddr is not set and the GER updater of the L2 GER manager %s is the zero address", gerManager)
		}
		source := "AggOracleCommitteeAddr, that is the GER updater of the L2 GER manager"
		if configuredCommittee == aggkitcommon.ZeroAddress {
			source = "the GER updater of the L2 GER manager (AggOracleCommitteeAddr not set)"
		}
		return &gerSubmission{
			requested: requested, mode: chaingersender.AggOracleCommitteeV2Mode, committee: committee,
			reason: fmt.Sprintf("committee %s, from %s", committee, source),
		}, nil

	case ModeAuto:
		detection, err := chaingersender.DetectGERMode(ctx, caller, gerManager, configuredCommittee)
		if err != nil {
			return nil, fmt.Errorf("AggOracle.Mode is auto and the mode can't be detected: %w", err)
		}
		return &gerSubmission{
			requested: requested, mode: detection.Mode, committee: detection.Committee, reason: detection.Reason,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported AggOracle.Mode %q", requested)
	}
}

// describe explains how the GERs are submitted: the configured mode, what it resolved to and why
func (s *gerSubmission) describe(gerManager common.Address) string {
	if s.requested == ModeAuto {
		return fmt.Sprintf("AggOracle.Mode is %q, resolved to %s mode by reading the GER updater of the "+
			"L2 GER manager %s: %s", s.requested, s.mode, gerManager, s.reason)
	}
	return fmt.Sprintf("AggOracle.Mode is %q: %s mode (%s)", s.requested, s.mode, s.reason)
}

// NewEVMChainSender creates the ChainSender that matches the configured Mode, resolving the "auto" mode (and
// the committee address of committee) by reading the contracts of the L2.
func NewEVMChainSender(
	ctx context.Context,
	logger *log.Logger,
	cfg Config,
	l2Client aggkittypes.BaseEthereumClienter,
	l2GERManager aggoracletypes.L2GERManagerContract,
	ethTxMan aggoracletypes.EthTxManager,
) (ChainSender, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	submission, err := resolveSubmission(ctx, cfg, l2Client)
	if err != nil {
		return nil, err
	}
	logger.Info(submission.describe(cfg.EVMSender.GlobalExitRootL2Addr))

	switch submission.mode {
	case chaingersender.DirectInjectionMode:
		sender, err := chaingersender.NewEVMChainGERSender(logger, cfg.EVMSender, l2GERManager, ethTxMan)
		if err != nil {
			return nil, err
		}
		return sender, nil

	case chaingersender.AggOracleCommitteeV2Mode:
		return newCommitteeV2Sender(ctx, logger, cfg, submission.committee, l2Client, l2GERManager, ethTxMan)

	default:
		return nil, fmt.Errorf("unsupported GER submission mode %q", submission.mode)
	}
}

// newCommitteeV2Sender creates the sender of the AggOracleCommittee v2, with the client and the collector of
// the GER validators
func newCommitteeV2Sender(
	ctx context.Context,
	logger *log.Logger,
	cfg Config,
	committeeAddr common.Address,
	l2Client aggkittypes.BaseEthereumClienter,
	l2GERManager aggoracletypes.L2GERManagerContract,
	ethTxMan aggoracletypes.EthTxManager,
) (ChainSender, error) {
	v2Cfg := cfg.CommitteeV2

	validatorClient, err := gervalidator.NewHTTPClient(gervalidator.ClientConfig{
		RequestTimeout: v2Cfg.RequestTimeout.Duration,
		CACertFile:     v2Cfg.TLS.CACertFile,
		ClientCertFile: v2Cfg.TLS.ClientCertFile,
		ClientKeyFile:  v2Cfg.TLS.ClientKeyFile,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create the client of the GER validators: %w", err)
	}
	collector := gervalidator.NewCollector(logger, validatorClient, v2Cfg.RequestTimeout.Duration)

	committee, err := aggoraclecommittee.NewAggoraclecommittee(committeeAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("failed to create the binding of the AggOracleCommittee v2 (SC address: %s): %w",
			committeeAddr, err)
	}

	overrides := make(map[common.Address]string, len(v2Cfg.ValidatorURLOverrides))
	for _, override := range v2Cfg.ValidatorURLOverrides {
		overrides[override.Address] = override.URL
	}

	sender, err := chaingersender.NewCommitteeV2Sender(ctx, logger, cfg.EVMSender, chaingersender.CommitteeV2Config{
		CollectRetryInterval:  v2Cfg.CollectRetryInterval.Duration,
		MaxInjectAttempts:     v2Cfg.MaxInjectAttempts,
		PendingGERTimeout:     v2Cfg.PendingGERTimeout.Duration,
		ValidatorURLOverrides: overrides,
	}, committeeAddr, l2GERManager, committee, collector, ethTxMan)
	if err != nil {
		return nil, err
	}
	return sender, nil
}
