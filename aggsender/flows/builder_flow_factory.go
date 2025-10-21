package flows

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/agglayer/aggkit/aggsender/aggchainproofclient"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/optimistic"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	// l2GERReaderFactory is a factory function to create L2 GER reader
	l2GERReaderFactory = l2gersync.NewL2EVMGERReader
)

// NewBuilderFlow creates a new AggsenderBuilderFlow based on the provided configuration.
func NewBuilderFlow(
	ctx context.Context,
	cfg config.Config,
	logger *log.Logger,
	storage db.AggSenderStorage,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	rollupDataQuerier types.RollupDataQuerier,
	committeeQuerier types.MultisigQuerier,
) (types.AggsenderBuilderFlow, error) {
	switch cfg.Mode {
	case types.PessimisticProofMode:
		commonFlowComponents, err := CreateCommonFlowComponents(
			ctx, logger, storage, l1Client, l2Client, l1InfoTreeSyncer, l2Syncer,
			rollupDataQuerier, committeeQuerier,
			0, false,
			cfg.MaxCertSize, cfg.RollupCreationBlockL1,
			cfg.DelayBetweenRetries.Duration, cfg.AggsenderPrivateKey,
			true,
			cfg.RequireCommitteeMembershipCheck,
			cfg.AgglayerBridgeL2Addr,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create common flow components: %w", err)
		}

		return NewPPBuilderFlow(
			logger,
			commonFlowComponents.BaseFlow,
			storage,
			commonFlowComponents.L1InfoTreeDataQuerier,
			commonFlowComponents.L2BridgeQuerier,
			commonFlowComponents.Signer,
			cfg.RequireOneBridgeInPPCertificate,
			cfg.MaxL2BlockNumber,
		), nil
	case types.AggchainProofMode:
		aggchainProofClient, err := aggchainproofclient.NewAggchainProofClient(cfg.AggkitProverClient)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error creating aggkit prover client: %w", err)
		}

		l2ChainID, err := rollupDataQuerier.GetRollupChainID()
		if err != nil {
			return nil, fmt.Errorf("error getting rollup chain id: %w", err)
		}

		optimisticSigner, optimisticModeQuerier, err := optimistic.NewOptimistic(
			ctx, logger, l1Client, l2ChainID, cfg.OptimisticModeConfig)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error creating optimistic mode querier: %w", err)
		}

		aggchainFEPQuerier, err := query.NewAggchainFEPQuerier(logger, types.AggchainProofMode,
			cfg.SovereignRollupAddr, l1Client)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error creating aggchain FEP querier: %w", err)
		}

		commonFlowComponents, err := CreateCommonFlowComponents(
			ctx, logger, storage, l1Client, l2Client, l1InfoTreeSyncer, l2Syncer,
			rollupDataQuerier, committeeQuerier,
			aggchainFEPQuerier.StartL2Block(), cfg.RequireNoFEPBlockGap,
			cfg.MaxCertSize, cfg.RollupCreationBlockL1,
			cfg.DelayBetweenRetries.Duration, cfg.AggsenderPrivateKey,
			true,
			cfg.RequireCommitteeMembershipCheck,
			cfg.AgglayerBridgeL2Addr,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create common flow components: %w", err)
		}

		l2GERReader, err := l2GERReaderFactory(cfg.GlobalExitRootL2Addr, l2Client, l1InfoTreeSyncer)
		if err != nil {
			return nil, fmt.Errorf("failed to create L2 GER reader: %w", err)
		}

		aggchainProofQuerier := query.NewAggchainProofQuery(
			logger,
			aggchainProofClient,
			commonFlowComponents.L1InfoTreeDataQuerier,
			optimisticSigner,
			commonFlowComponents.BaseFlow,
			query.NewGERDataQuerier(commonFlowComponents.L1InfoTreeDataQuerier, l2GERReader),
			commonFlowComponents.L2BridgeQuerier,
		)

		return NewAggchainProverBuilderFlow(
			logger,
			NewAggchainProverFlowConfig(cfg.MaxL2BlockNumber),
			commonFlowComponents.BaseFlow,
			storage,
			commonFlowComponents.L1InfoTreeDataQuerier,
			commonFlowComponents.L2BridgeQuerier,
			l1Client,
			commonFlowComponents.Signer,
			optimisticModeQuerier,
			aggchainProofQuerier,
		), nil

	default:
		return nil, fmt.Errorf("unsupported Aggsender mode: %s", cfg.Mode)
	}
}

type CommonFlowComponents struct {
	L2BridgeQuerier       types.BridgeQuerier
	L1InfoTreeDataQuerier types.L1InfoTreeDataQuerier
	LERQuerier            types.LERQuerier
	BaseFlow              types.AggsenderFlowBaser
	Signer                signertypes.Signer
}

func CreateCommonFlowComponents(
	ctx context.Context,
	logger *log.Logger,
	storage db.AggSenderStorage,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	rollupDataQuerier types.RollupDataQuerier,
	committeeQuerier types.MultisigQuerier,
	startL2Block uint64,
	requireNoFEPBlockGap bool,
	maxCertSize uint,
	rollupCreationBlockL1 uint64,
	delayBetweenRetries time.Duration,
	signerCfg signertypes.SignerConfig,
	fullClaimsRequired bool,
	requireCommitteeMembershipCheck bool,
	agglayerBridgeL2Addr common.Address,
) (*CommonFlowComponents, error) {
	l2ChainID, err := rollupDataQuerier.GetRollupChainID()
	if err != nil {
		return nil, fmt.Errorf("error getting rollup chain id: %w", err)
	}

	signer, err := initializeSigner(ctx, signerCfg, logger, l2ChainID,
		committeeQuerier, requireCommitteeMembershipCheck)
	if err != nil {
		return nil, err
	}

	agglayerBridgeL2Reader, err := bridgesync.NewAgglayerBridgeL2Reader(agglayerBridgeL2Addr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("failed to create bridge L2 sovereign reader: %w", err)
	}

	l2BridgeQuerier := query.NewBridgeDataQuerier(logger, l2Syncer, delayBetweenRetries, agglayerBridgeL2Reader)
	l1InfoTreeQuerier := query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer)
	lerQuerier := query.NewLERDataQuerier(rollupCreationBlockL1, rollupDataQuerier)

	baseFlow := NewBaseFlow(
		logger, l2BridgeQuerier, storage, l1InfoTreeQuerier, lerQuerier,
		NewBaseFlowConfig(maxCertSize, startL2Block, requireNoFEPBlockGap, fullClaimsRequired),
	)

	return &CommonFlowComponents{
		L2BridgeQuerier:       l2BridgeQuerier,
		L1InfoTreeDataQuerier: l1InfoTreeQuerier,
		LERQuerier:            lerQuerier,
		BaseFlow:              baseFlow,
		Signer:                signer,
	}, nil
}

func initializeSigner(
	ctx context.Context,
	signerCfg signertypes.SignerConfig,
	logger *log.Logger,
	l2ChainID uint64,
	committeeQuerier types.MultisigQuerier,
	requireCommitteeMembershipCheck bool,
) (signertypes.Signer, error) {
	signer, err := signer.NewSigner(ctx, l2ChainID, signerCfg, aggkitcommon.AGGSENDER, logger)
	if err != nil {
		return nil, fmt.Errorf("error NewSigner. Err: %w", err)
	}

	if err := signer.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("error signer.Initialize. Err: %w", err)
	}

	multisigCommittee, err := committeeQuerier.GetMultisigCommittee(ctx, big.NewInt(int64(aggkittypes.Latest)))
	if err != nil {
		if requireCommitteeMembershipCheck {
			return nil, fmt.Errorf("error getting multisig committee: %w", err)
		}

		logger.Warnf("error getting multisig committee: %v", err)
		return signer, nil
	}

	if !multisigCommittee.IsMember(signer.PublicAddress()) {
		if requireCommitteeMembershipCheck {
			return nil, fmt.Errorf("signer address %s is not part of the multisig committee: %s",
				signer.PublicAddress(), multisigCommittee.String())
		}

		logger.Warnf("signer address %s is not part of the multisig committee: %s",
			signer.PublicAddress(), multisigCommittee.String())
	}

	return signer, nil
}
