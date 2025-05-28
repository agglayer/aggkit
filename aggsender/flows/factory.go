package flows

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/go_signer/signer"
	signerTypes "github.com/agglayer/go_signer/signer/types"
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
) (types.AggsenderFlow, error) {
	switch types.AggsenderMode(cfg.Mode) {
	case types.PessimisticProofMode:
		signer, err := initializeSigner(ctx, cfg.AggsenderPrivateKey, logger)
		if err != nil {
			return nil, err
		}

		return NewPPFlow(
			logger,
			cfg.MaxCertSize,
			storage,
			query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer),
			query.NewBridgeDataQuerier(l2Syncer),
			signer,
		), nil
	case types.AggchainProofMode:
		if err := cfg.AggkitProverClient.Validate(); err != nil {
			return nil, fmt.Errorf("invalid aggkit prover client config: %w", err)
		}

		signer, err := initializeSigner(ctx, cfg.AggsenderPrivateKey, logger)
		if err != nil {
			return nil, err
		}

		aggchainProofClient, err := grpc.NewAggchainProofClient(cfg.AggkitProverClient,
			cfg.GenerateAggchainProofTimeout.Duration)
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

		return NewAggchainProverFlow(
			logger,
			cfg.MaxCertSize,
			startL2Block,
			aggchainProofClient,
			storage,
			l1InfoTreeQuerier,
			query.NewBridgeDataQuerier(l2Syncer),
			query.NewGERDataQuerier(l1InfoTreeQuerier, gerReader),
			l1Client,
			cfg.RequireNoFEPBlockGap,
			signer,
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
