package flows

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/aggsender/aggchainproofclient"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/optimistic"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
)

var (
	// l2GERReaderFactory is a factory function to create L2 GER reader
	l2GERReaderFactory = l2gersync.NewL2EVMGERReader
)

func NewFlow(
	ctx context.Context,
	cfg config.Config,
	logger *log.Logger,
	storage db.AggSenderStorage,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	rollupDataQuerier types.RollupDataQuerier,
) (types.AggsenderFlow, error) {
	switch types.AggsenderMode(cfg.Mode) {
	case types.PessimisticProofMode:
		commonFlowComponents, err := CreateCommonFlowComponents(
			ctx, logger, storage, l1Client, l1InfoTreeSyncer, l2Syncer, rollupDataQuerier, 0, false,
			cfg.MaxCertSize, cfg.RollupCreationBlockL1, cfg.DelayBetweenRetries.Duration, cfg.AggsenderPrivateKey,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create common flow components: %w", err)
		}

		return NewPPFlow(
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

		optimisticSigner, optimisticModeQuerier, err := optimistic.NewOptimistic(
			ctx, logger, l1Client, cfg.OptimisticModeConfig)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error creating optimistic mode querier: %w", err)
		}

		aggchainFEPQuerier, err := query.NewAggchainFEPQuerier(logger, types.AggchainProofMode,
			cfg.SovereignRollupAddr, l1Client)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error creating aggchain FEP querier: %w", err)
		}

		commonFlowComponents, err := CreateCommonFlowComponents(
			ctx, logger, storage, l1Client, l1InfoTreeSyncer, l2Syncer, rollupDataQuerier,
			aggchainFEPQuerier.StartL2Block(), cfg.RequireNoFEPBlockGap,
			cfg.MaxCertSize, cfg.RollupCreationBlockL1, cfg.DelayBetweenRetries.Duration, cfg.AggsenderPrivateKey,
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
		)

		return NewAggchainProverFlow(
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
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	rollupDataQuerier types.RollupDataQuerier,
	startL2Block uint64,
	requireNoFEPBlockGap bool,
	maxCertSize uint,
	rollupCreationBlockL1 uint64,
	delayBetweenRetries time.Duration,
	signerCfg signertypes.SignerConfig,
) (*CommonFlowComponents, error) {
	signer, err := initializeSigner(ctx, signerCfg, logger)
	if err != nil {
		return nil, err
	}

	l2BridgeQuerier := query.NewBridgeDataQuerier(logger, l2Syncer, delayBetweenRetries)
	l1InfoTreeQuerier := query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer)
	lerQuerier := query.NewLERDataQuerier(rollupCreationBlockL1, rollupDataQuerier)

	baseFlow := NewBaseFlow(
		logger, l2BridgeQuerier, storage, l1InfoTreeQuerier, lerQuerier,
		NewBaseFlowConfig(maxCertSize, startL2Block, requireNoFEPBlockGap),
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
) (signertypes.Signer, error) {
	signer, err := signer.NewSigner(ctx, 0, signerCfg, aggkitcommon.AGGSENDER, logger)
	if err != nil {
		return nil, fmt.Errorf("error NewSigner. Err: %w", err)
	}

	if err := signer.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("error signer.Initialize. Err: %w", err)
	}

	return signer, nil
}
