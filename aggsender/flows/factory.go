package flows

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/aggchainproofclient"
	"github.com/agglayer/aggkit/aggsender/certificatebuild"
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

// funcGetL2StartBlock is a intermediate func that allow to override this call in UT
var funcGetL2StartBlock = getL2StartBlock

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
		certificateSigner, err := initializeSigner(ctx, cfg.AggsenderPrivateKey, logger)
		if err != nil {
			return nil, err
		}
		l2BridgeQuerier := query.NewBridgeDataQuerier(l2Syncer)
		l1InfoTreeQuerier := query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer)
		logger.Infof("Aggsender signer address: %s", certificateSigner.PublicAddress().Hex())

		certificateBuilder := certificatebuild.NewCertificateBuilder(
			logger,
			storage,
			l1InfoTreeQuerier,
			l2BridgeQuerier,
			certificatebuild.NewCertificateBuilderConfig(cfg.MaxCertSize, 0),
		)
		certificateVerifier := certificatebuild.NewCertificateBuildVerifier()

		return NewPPFlow(
			logger,
			storage,
			l1InfoTreeQuerier,
			l2BridgeQuerier,
			certificateBuilder,
			certificateVerifier,
			certificateSigner,
		), nil
	case types.AggchainProofMode:
		certificateSigner, err := initializeSigner(ctx, cfg.AggsenderPrivateKey, logger)
		if err != nil {
			return nil, err
		}
		logger.Infof("Aggsender signer address: %s", certificateSigner.PublicAddress().Hex())

		if cfg.AggchainProofURL == "" {
			return nil, fmt.Errorf("aggchain prover mode requires AggchainProofURL")
		}

		aggchainProofClient, err := aggchainproofclient.NewAggchainProofClient(
			cfg.AggchainProofURL,
			cfg.GenerateAggchainProofTimeout.Duration, cfg.UseAggkitProverTLS)
		if err != nil {
			return nil, fmt.Errorf("error creating aggkit prover client: %w", err)
		}

		gerReader, err := funcNewEVMChainGERReader(cfg.GlobalExitRootL2Addr, l2Client)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error creating VMChainGERReader L2Etherman: %w", err)
		}

		l1InfoTreeQuerier := query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer)

		startL2Block, err := funcGetL2StartBlock(cfg.SovereignRollupAddr, l1Client)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error reading sovereign rollup: %w", err)
		}
		optimisticSigner, optimisticModeQuerier, err := optimistic.NewOptimistic(
			ctx, logger, l1Client, cfg.OptimisticModeConfig)
		if err != nil {
			return nil, fmt.Errorf("aggchainProverFlow - error creating optimistic mode querier: %w", err)
		}
		l2BridgeQuerier := query.NewBridgeDataQuerier(l2Syncer)
		certificateBuilder := certificatebuild.NewCertificateBuilder(
			logger,
			storage,
			l1InfoTreeQuerier,
			l2BridgeQuerier,
			certificatebuild.NewCertificateBuilderConfig(cfg.MaxCertSize, startL2Block),
		)
		certificateVerifier := certificatebuild.NewCertificateBuildVerifier()

		aggchainProofQuerier := query.NewAggchainProofQuery(
			logger,
			aggchainProofClient,
			certificateBuilder.GetImportedBridgeExitsConverter(),
			l1InfoTreeQuerier,
			optimisticSigner,
			certificateBuilder,
			query.NewGERDataQuerier(l1InfoTreeQuerier, gerReader),
		)

		return NewAggchainProverFlow(
			logger,
			NewAggchainProverFlowConfig(cfg.RequireNoFEPBlockGap, startL2Block),
			storage,
			l2BridgeQuerier,
			certificateSigner,
			optimisticModeQuerier,
			certificateBuilder,
			certificateVerifier,
			aggchainProofQuerier,
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
