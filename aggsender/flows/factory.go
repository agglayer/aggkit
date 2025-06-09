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
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/go_signer/signer"
	signerTypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

// funcGetL2StartBlock is a intermediate func that allow to override this call in UT
var funcGetL2StartBlock = getL2StartBlock

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
	startLER common.Hash,
) (types.AggsenderFlow, error) {
	logger.Infof("StartLER from rollup manager contract: %s", startLER.String())
	switch types.AggsenderMode(cfg.Mode) {
	case types.PessimisticProofMode:
		signer, err := initializeSigner(ctx, cfg.AggsenderPrivateKey, logger)
		if err != nil {
			return nil, err
		}
		l2BridgeQuerier := query.NewBridgeDataQuerier(l2Syncer)
		l1InfoTreeQuerier := query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer)
		logger.Infof("Aggsender signer address: %s", signer.PublicAddress().Hex())
		baseFlow := NewBaseFlow(
			logger, l2BridgeQuerier, storage, l1InfoTreeQuerier,
			NewBaseFlowConfig(cfg.MaxCertSize, 0, startLER),
		)
		return NewPPFlow(
			logger,
			baseFlow,
			storage,
			l1InfoTreeQuerier,
			l2BridgeQuerier,
			signer,
			cfg.RequireNoFEPBlockGap,
		), nil
	case types.AggchainProofMode:
		if err := cfg.AggkitProverClient.Validate(); err != nil {
			return nil, fmt.Errorf("invalid aggkit prover client config: %w", err)
		}

		signer, err := initializeSigner(ctx, cfg.AggsenderPrivateKey, logger)
		if err != nil {
			return nil, err
		}
		logger.Infof("Aggsender signer address: %s", signer.PublicAddress().Hex())

		aggchainProofClient, err := aggchainproofclient.NewAggchainProofClient(cfg.AggkitProverClient)
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
		baseFlow := NewBaseFlow(
			logger, l2BridgeQuerier, storage, l1InfoTreeQuerier,
			NewBaseFlowConfig(cfg.MaxCertSize, startL2Block, startLER),
		)

		return NewAggchainProverFlow(
			logger,
			baseFlow,
			NewAggchainProverFlowConfig(cfg.RequireNoFEPBlockGap),
			aggchainProofClient,
			storage,
			l1InfoTreeQuerier,
			l2BridgeQuerier,
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
	signer, err := signer.NewSigner(ctx, 0, signerCfg, aggkitcommon.AGGSENDER, logger)
	if err != nil {
		return nil, fmt.Errorf("error NewSigner. Err: %w", err)
	}

	if err := signer.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("error signer.Initialize. Err: %w", err)
	}

	return signer, nil
}
