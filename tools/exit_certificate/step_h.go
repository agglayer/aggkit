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
// gResult is the output of Step G; when provided, its InitialLocalExitRoot is compared
// against the agglayer's settled LER and an error is returned on mismatch.
func RunStepH(ctx context.Context, cfg *Config, gResult *StepGResult) (*StepHResult, error) {
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

	log.Infof("PreviousLocalExitRoot (agglayer): %s", prevLER.Hex())
	log.Infof("Next certificate height: %d", nextHeight)

	if gResult != nil {
		log.Infof("InitialLocalExitRoot  (L2 chain): %s", gResult.InitialLocalExitRoot.Hex())
		if gResult.InitialLocalExitRoot != prevLER {
			return nil, fmt.Errorf(
				"LocalExitRoot mismatch: L2 chain has %s but agglayer settled %s — "+
					"the chain may have unaccounted bridge exits",
				gResult.InitialLocalExitRoot.Hex(), prevLER.Hex(),
			)
		}
		log.Info("✅ InitialLocalExitRoot matches agglayer settled LER")
	}

	log.Info("STEP H complete")
	return &StepHResult{PreviousLocalExitRoot: prevLER, Height: nextHeight}, nil
}
