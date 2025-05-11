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
	"github.com/agglayer/go_signer/signer"
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
		signer, err := signer.NewSigner(ctx, 0, cfg.AggsenderPrivateKey, common.AGGSENDER, logger)
		if err != nil {
			return nil, fmt.Errorf("error NewSigner. Err: %w", err)
		}
		err = signer.Initialize(ctx)
		if err != nil {
			return nil, fmt.Errorf("error signer.Initialize. Err: %w", err)
		}

		return NewPPFlow(
			logger,
			cfg.MaxCertSize,
			cfg.BridgeMetadataAsHash,
			storage,
			query.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer),
			query.NewBridgeDataQuerier(l2Syncer),
			signer,
		), nil
	case types.AggchainProofMode:
		if cfg.AggchainProofURL == "" {
			return nil, fmt.Errorf("aggchain prover mode requires AggchainProofURL")
		}

		aggchainProofClient, err := grpc.NewAggchainProofClient(
			cfg.AggchainProofURL,
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
			cfg.BridgeMetadataAsHash,
			startL2Block,
			aggchainProofClient,
			storage,
			l1InfoTreeQuerier,
			query.NewBridgeDataQuerier(l2Syncer),
			query.NewGERDataQuerier(l1InfoTreeQuerier, gerReader),
			l1Client,
		), nil

	default:
		return nil, fmt.Errorf("unsupported Aggsender mode: %s", cfg.Mode)
	}
}
