package exit_certificate

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// RunStepH fetches the PreviousLocalExitRoot for the L2 network from the agglayer
// by calling GetNetworkInfo and reading the SettledLER field.
func RunStepH(ctx context.Context, cfg *Config) (*StepHResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP H - Fetch PreviousLocalExitRoot")
	log.Info("═══════════════════════════════════════════")

	if cfg.Options.AgglayerGRPCURL == "" {
		return nil, fmt.Errorf("agglayerGrpcUrl is required for step H")
	}
	grpcConfig := aggkitgrpc.DefaultConfig()
	grpcConfig.URL = cfg.Options.AgglayerGRPCURL
	client, err := agglayer.NewAgglayerClient(agglayer.ClientConfig{
		GRPC: grpcConfig,
	}, log.GetDefaultLogger())
	if err != nil {
		return nil, fmt.Errorf("create agglayer client: %w", err)
	}

	info, err := client.GetNetworkInfo(ctx, cfg.L2NetworkID)
	if err != nil {
		return nil, fmt.Errorf("get network info (network %d): %w", cfg.L2NetworkID, err)
	}

	var prevLER common.Hash
	var nextHeight uint64
	if info.SettledLER != nil {
		prevLER = *info.SettledLER
	} else {
		log.Infof("No settled certificate for network %d — PreviousLocalExitRoot is zero", cfg.L2NetworkID)
	}
	if info.SettledHeight != nil {
		nextHeight = *info.SettledHeight + 1
	}

	log.Infof("PreviousLocalExitRoot: %s", prevLER.Hex())
	log.Infof("Next certificate height: %d", nextHeight)
	log.Info("STEP H complete")
	return &StepHResult{PreviousLocalExitRoot: prevLER, Height: nextHeight}, nil
}
