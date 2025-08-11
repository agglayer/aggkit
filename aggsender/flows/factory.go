package flows

import (
	"context"
	"fmt"

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
	signerTypes "github.com/agglayer/go_signer/signer/types"
)

var (
	// l2GERReaderFactory is a factory function to create L2 GER reader
	l2GERReaderFactory = l2gersync.NewL2EVMGERReader
)

// NewFlow creates a new Aggsender flow based on the provided configuration.
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
		commonFlowComponents, err := createCommonComponents(
			ctx, cfg, logger, storage, l1Client, l1InfoTreeSyncer, l2Syncer, rollupDataQuerier, 0, false,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create common flow components: %w", err)
		}

		return NewPPFlow(
			logger,
			commonFlowComponents.baseFlow,
			storage,
			commonFlowComponents.l1InfoTreeDataQuerier,
			commonFlowComponents.l2BridgeQuerier,
			commonFlowComponents.signer,
			cfg.RequireOneBridgeInPPCertificate,
			cfg.MaxL2BlockNumber,
		), nil
	case types.AggchainProofMode:
		if err := cfg.AggkitProverClient.Validate(); err != nil {
			return nil, fmt.Errorf("invalid aggkit prover client config: %w", err)
		}

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

		commonFlowComponents, err := createCommonComponents(
			ctx, cfg, logger, storage, l1Client, l1InfoTreeSyncer, l2Syncer, rollupDataQuerier,
			aggchainFEPQuerier.StartL2Block(), cfg.RequireNoFEPBlockGap,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create common flow components: %w", err)
		}

		l2GERReader, err := l2GERReaderFactory(cfg.GlobalExitRootL2Addr, l2Client, l1InfoTreeSyncer)
		if err != nil {
			return nil, fmt.Errorf("failed to create L2 GER reader: %w", err)
		}

		gerQuerier := query.NewGERDataQuerier(commonFlowComponents.l1InfoTreeDataQuerier, l2GERReader)

		aggchainProofQuerier := query.NewAggchainProofQuery(
			logger,
			aggchainProofClient,
			commonFlowComponents.l1InfoTreeDataQuerier,
			optimisticSigner,
			commonFlowComponents.baseFlow,
			gerQuerier,
		)

		return NewAggchainProverFlow(
			logger,
			NewAggchainProverFlowConfig(cfg.MaxL2BlockNumber),
			commonFlowComponents.baseFlow,
			storage,
			commonFlowComponents.l1InfoTreeDataQuerier,
			commonFlowComponents.l2BridgeQuerier,
			gerQuerier,
			l1Client,
			commonFlowComponents.signer,
			optimisticModeQuerier,
			aggchainProofQuerier,
		), nil

	default:
		return nil, fmt.Errorf("unsupported Aggsender mode: %s", cfg.Mode)
	}
}

type commonFlowComponents struct {
	l2BridgeQuerier       types.BridgeQuerier
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier
	lerQuerier            types.LERQuerier
	baseFlow              types.AggsenderFlowBaser
	signer                signerTypes.Signer
}

func createCommonComponents(
	ctx context.Context,
	cfg config.Config,
	logger *log.Logger,
	storage db.AggSenderStorage,
	l1Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
	rollupDataQuerier types.RollupDataQuerier,
	startL2Block uint64,
	requireNoFEPBlockGap bool,
) (commonFlowComponents, error) {
	signer, err := initializeSigner(ctx, cfg.AggsenderPrivateKey, logger)
	if err != nil {
		return commonFlowComponents{}, err
	}

	l2BridgeQuerier := query.NewBridgeDataQuerier(logger, l2Syncer, cfg.DelayBetweenRetries.Duration)
	l1InfoTreeQuerier := query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer)
	lerQuerier := query.NewLERDataQuerier(cfg.RollupCreationBlockL1, rollupDataQuerier)

	baseFlow := NewBaseFlow(
		logger, l2BridgeQuerier, storage, l1InfoTreeQuerier, lerQuerier,
		NewBaseFlowConfig(cfg.MaxCertSize, startL2Block, requireNoFEPBlockGap),
	)

	return commonFlowComponents{
		l2BridgeQuerier:       l2BridgeQuerier,
		l1InfoTreeDataQuerier: l1InfoTreeQuerier,
		lerQuerier:            lerQuerier,
		baseFlow:              baseFlow,
		signer:                signer,
	}, nil
}

func initializeSigner(
	ctx context.Context,
	signerCfg signerTypes.SignerConfig,
	logger *log.Logger,
) (signerTypes.Signer, error) {
	signer, err := signer.NewSigner(ctx, 0, signerCfg, aggkitcommon.AGGSENDER, logger)
	if err != nil {
		return nil, fmt.Errorf("error NewSigner. Err: %w", err)
	}

	if err := signer.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("error signer.Initialize. Err: %w", err)
	}

	return signer, nil
}
