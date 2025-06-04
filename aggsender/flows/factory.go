package flows

import (
	"context"
	"fmt"

	agggchainproofclient "github.com/agglayer/aggkit/aggsender/aggchainproofclient"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/optimistic"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/go_signer/signer"
	signerTypes "github.com/agglayer/go_signer/signer/types"
)

// NewFlow creates a new Aggsender flow based on the provided configuration.
func NewFlow(
	ctx context.Context,
	cfg config.Config,
	logger *log.Logger,
	storage db.AggSenderStorage,
	l1Client types.EthClient,
	l2Client types.EthClient,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer,
) (types.AggsenderFlow, error) {
	switch types.AggsenderMode(cfg.Mode) {
	case types.PessimisticProofMode:
		signer, err := initializeSigner(ctx, cfg.AggsenderPrivateKey, logger)
		if err != nil {
			return nil, err
		}
		logger.Infof("Aggsender signer address: %s", signer.PublicAddress().Hex())

		return NewPPFlow(
			logger,
			cfg.MaxCertSize,
			storage,
			query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer),
			query.NewBridgeDataQuerier(l2Syncer),
			signer,
		), nil
	case types.AggchainProofMode:
		signer, err := initializeSigner(ctx, cfg.AggsenderPrivateKey, logger)
		if err != nil {
			return nil, err
		}
		logger.Infof("Aggsender signer address: %s", signer.PublicAddress().Hex())

		if cfg.AggchainProofURL == "" {
			return nil, fmt.Errorf("aggchain prover mode requires AggchainProofURL")
		}

		aggchainProofClient, err := agggchainproofclient.NewAggchainProofClient(
			cfg.AggchainProofURL,
			cfg.GenerateAggchainProofTimeout.Duration, cfg.UseAggkitProverTLS)
		if err != nil {
			return nil, fmt.Errorf("error creating aggkit prover client: %w", err)
		}

		gerReader, err := funcNewEVMChainGERReader(cfg.GlobalExitRootL2Addr, l2Client)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error creating L2Etherman: %w", err)
		}

		l1InfoTreeQuerier := query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer)

		startL2Block, err := getL2StartBlock(cfg.SovereignRollupAddr, l1Client)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error reading sovereign rollup: %w", err)
		}
		optimisticSigner, optimisticModeQuerier, err := optimistic.NewOptimistic(
			ctx, logger, l1Client, cfg.OptimisticModeConfig)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error creating optimistic mode querier: %w", err)
		}

		return NewAggchainProverFlow(
			logger,
			AggchainProverFlowConfig{
				baseFlowConfig: BaseFlowConfig{
					MaxCertSize:  cfg.MaxCertSize,
					StartL2Block: startL2Block,
				},
				requireNoFEPBlockGap: cfg.RequireNoFEPBlockGap,
			},
			aggchainProofClient,
			storage,
			l1InfoTreeQuerier,
			query.NewBridgeDataQuerier(l2Syncer),
			query.NewGERDataQuerier(l1InfoTreeQuerier, gerReader),
			l1Client,
			signer,
			optimisticModeQuerier,
			optimisticSigner,
		), nil

	default:
		return nil, fmt.Errorf("unsupported Aggsender mode: %s", cfg.Mode)
	}
}

func initializeSigner(
	ctx context.Context,
	signerCfg signerTypes.SignerConfig,
	logger *log.Logger,
) (signerTypes.Signer, error) {
	signer, err := signer.NewSigner(ctx, 0, signerCfg, common.AGGSENDER, logger)
	if err != nil {
		return nil, fmt.Errorf("error NewSigner. Err: %w", err)
	}

	if err := signer.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("error signer.Initialize. Err: %w", err)
	}

	return signer, nil
}
