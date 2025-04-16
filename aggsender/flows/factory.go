package flows

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/bridgequery"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/aggsender/l1infotreequery"
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
	l2BridgeSyncer types.L2BridgeSyncer,
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
			l1infotreequery.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer),
			bridgequery.NewBridgeDataQuerier(l2BridgeSyncer),
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

		return NewAggchainProverFlow(
			logger,
			cfg.MaxCertSize,
			cfg.BridgeMetadataAsHash,
			cfg.GlobalExitRootL2Addr,
			cfg.SovereignRollupAddr,
			aggchainProofClient,
			storage,
			l1infotreequery.NewL1InfoTreeDataQuerier(l1Client, l1InfoTreeSyncer),
			bridgequery.NewBridgeDataQuerier(l2BridgeSyncer),
			l1Client,
			l2Client,
		)

	default:
		return nil, fmt.Errorf("unsupported Aggsender mode: %s", cfg.Mode)
	}
}
