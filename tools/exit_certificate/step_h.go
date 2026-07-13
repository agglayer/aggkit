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

	return runStepH(ctx, cfg, client, gResult)
}

// fetchSettledNetworkState queries the agglayer network info, refuses to proceed on a pending
// (open) certificate, and derives the settled LER / next certificate height. Shared by Step
// CHECK's unsettled-bridge-exits check and Step H so both apply the exact same guard and derivation.
func fetchSettledNetworkState(
	ctx context.Context, cfg *Config, client agglayer.AgglayerClientInterface,
) (settledLER common.Hash, nextHeight uint64, err error) {
	info, err := client.GetNetworkInfo(ctx, cfg.L2NetworkID)
	if err != nil {
		return common.Hash{}, 0, fmt.Errorf("get network info (network %d): %w", cfg.L2NetworkID, err)
	}

	// Refuse to proceed when the agglayer still has a non-settled (open) certificate for this
	// network: building a new exit certificate on top of a pending one would conflict.
	if info.LatestPendingStatus != nil && info.LatestPendingStatus.IsOpen() {
		pendingHeight := "unknown"
		if info.LatestPendingHeight != nil {
			pendingHeight = fmt.Sprintf("%d", *info.LatestPendingHeight)
		}
		return common.Hash{}, 0, fmt.Errorf(
			"network %d has a pending certificate (status %s, height %s) that is not settled yet — "+
				"wait for it to settle before generating a new exit certificate",
			cfg.L2NetworkID, info.LatestPendingStatus, pendingHeight,
		)
	}

	if info.SettledLER != nil {
		settledLER = *info.SettledLER
	} else {
		log.Infof("No settled certificate for network %d — settled LER is zero", cfg.L2NetworkID)
	}
	if info.SettledHeight != nil {
		nextHeight = *info.SettledHeight + 1
	}
	return settledLER, nextHeight, nil
}

// runStepH is the client-injectable core of RunStepH (tests pass an agglayer client mock in place of
// the real gRPC client). It queries the network info, refuses to proceed on a pending certificate,
// and derives the PreviousLocalExitRoot / next height (optionally cross-checking gResult).
func runStepH(
	ctx context.Context, cfg *Config, client agglayer.AgglayerClientInterface, gResult *StepGResult,
) (*StepHResult, error) {
	prevLER, nextHeight, err := fetchSettledNetworkState(ctx, cfg, client)
	if err != nil {
		return nil, err
	}

	log.Infof("PreviousLocalExitRoot (agglayer): %s", prevLER.Hex())
	log.Infof("Next certificate height: %d", nextHeight)

	if gResult != nil {
		log.Infof("InitialLocalExitRoot  (L2 chain): %s", gResult.InitialLocalExitRoot.Hex())
		if gResult.InitialLocalExitRoot != prevLER {
			mismatchErr := fmt.Errorf(
				"LocalExitRoot mismatch: Step G started from %s (read from bridgeContract) but agglayer last settled %s — "+
					"re-running with the same target block will fail identically; the certificate must be generated "+
					"from a target block whose L2 bridge LER matches the agglayer settled LER: if the bridge LER is "+
					"ahead (bridge exits no settled certificate covers, e.g. exits made before the sequencer halt), "+
					"wait until the agglayer settles every bridge exit up to the target block (keep the aggsender "+
					"running after the sequencer halt until the last certificate settles) or move the target block "+
					"back to the settled state; if the settled LER is ahead (a certificate settled while this one "+
					"was being generated), move the target block forward",
				gResult.InitialLocalExitRoot.Hex(), prevLER.Hex(),
			)
			if err := suppressUnsettledExitsError(cfg, mismatchErr); err != nil {
				return nil, err
			}
		} else {
			log.Info("✅ InitialLocalExitRoot matches agglayer settled LER")
		}
	}

	log.Info("STEP H complete")
	return &StepHResult{PreviousLocalExitRoot: prevLER, Height: nextHeight}, nil
}
