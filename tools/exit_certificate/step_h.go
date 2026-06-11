package exit_certificate

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
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

	agglayerClientCfg := cfg.Options.AgglayerClient
	if agglayerClientCfg.GRPC == nil || agglayerClientCfg.GRPC.URL == "" {
		return nil, fmt.Errorf("agglayerClient.grpc.url is required for step H")
	}

	client, err := agglayer.NewAgglayerClient(agglayerClientCfg, log.GetDefaultLogger())
	if err != nil {
		return nil, fmt.Errorf("create agglayer client: %w", err)
	}

	info, err := client.GetNetworkInfo(ctx, cfg.L2NetworkID)
	if err != nil {
		return nil, fmt.Errorf("get network info (network %d) from %s: %w", cfg.L2NetworkID, agglayerClientCfg.GRPC.URL, err)
	}

	// Refuse to proceed when the agglayer still has a non-settled (open) certificate for this
	// network: building a new exit certificate on top of a pending one would conflict.
	if info.LatestPendingStatus != nil && info.LatestPendingStatus.IsOpen() {
		pendingHeight := "unknown"
		if info.LatestPendingHeight != nil {
			pendingHeight = fmt.Sprintf("%d", *info.LatestPendingHeight)
		}
		return nil, fmt.Errorf(
			"network %d has a pending certificate (status %s, height %s) that is not settled yet — "+
				"wait for it to settle before generating a new exit certificate",
			cfg.L2NetworkID, info.LatestPendingStatus, pendingHeight,
		)
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
				"LocalExitRoot mismatch: Step G started from %s (read from bridgeContract) but agglayer last settled %s — "+
					"this situation should not happen: the sequencer must be stopped before starting to generate "+
					"the certificate, so that the L2 state (and its LER) stays frozen throughout the whole pipeline; "+
					"if you see this, the chain advanced or a new certificate was settled while the certificate was "+
					"being generated — stop the sequencer and re-run from the beginning",
				gResult.InitialLocalExitRoot.Hex(), prevLER.Hex(),
			)
		}
		log.Info("✅ InitialLocalExitRoot matches agglayer settled LER")
	}

	log.Info("STEP H complete")
	return &StepHResult{PreviousLocalExitRoot: prevLER, Height: nextHeight}, nil
}
