package exit_certificate

import (
	"context"
	"encoding/json"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// RunStepH fetches the PreviousLocalExitRoot for the L2 network from the agglayer
// by calling interop_getNetworkInfo and reading the SettledLER field.
// Skipped when options.agglayerRpcUrl is not set; returns a zero hash in that case.
func RunStepH(ctx context.Context, cfg *Config) (*StepHResult, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP H - Fetch PreviousLocalExitRoot")
	log.Info("═══════════════════════════════════════════")

	if cfg.Options.AgglayerRPCURL == "" {
		return nil, fmt.Errorf("agglayerRpcUrl is required for step H")
	}

	raw, err := singleRPC(ctx, cfg.Options.AgglayerRPCURL, "interop_getNetworkInfo",
		[]any{cfg.L2NetworkID}, defaultRetries)
	if err != nil {
		return nil, fmt.Errorf("interop_getNetworkInfo (network %d): %w", cfg.L2NetworkID, err)
	}

	var info agglayertypes.NetworkInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("parse interop_getNetworkInfo response: %w", err)
	}

	var prevLER common.Hash
	if info.SettledLER != nil {
		prevLER = *info.SettledLER
	} else {
		log.Infof("No settled certificate for network %d — PreviousLocalExitRoot is zero", cfg.L2NetworkID)
	}

	log.Infof("PreviousLocalExitRoot: %s", prevLER.Hex())
	log.Info("STEP H complete")
	return &StepHResult{PreviousLocalExitRoot: prevLER}, nil

}
