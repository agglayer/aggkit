package exit_certificate

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
)

// lerReaderFn reads the bridge contract's local exit root (getRoot()) at blockTag. It matches
// readLocalExitRoot's signature so tests can inject a stub in place of the real RPC call.
type lerReaderFn func(
	ctx context.Context, rpcURL string, bridgeAddr common.Address, blockTag string,
) (common.Hash, error)

// RunStepLERPreflight fails the pipeline early when the L2 bridge's local exit root at the
// target block does not match the agglayer's last settled LER (AET-11). A permissionless L2→L1
// bridge exit made before the sequencer halt but never settled by the agglayer advances the L2
// LER past the settled state; the certificate cannot include that exit, so Step H would abort —
// but only after the expensive scan (Steps A/B) and replay (Step G) phases already ran. This
// check runs right after Step 0 (the first point where the target block is resolved) so the
// operator gets the actionable error before any expensive work.
//
// It requires options.agglayerClient.grpc.url — the same requirement Step H enforces later in
// the pipeline, so this only moves the failure earlier.
func RunStepLERPreflight(ctx context.Context, cfg *Config, targetBlock uint64) error {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP LER PREFLIGHT - Unsettled bridge exits check")
	log.Info("═══════════════════════════════════════════")

	agglayerClientCfg := cfg.Options.AgglayerClient
	if agglayerClientCfg.GRPC == nil || agglayerClientCfg.GRPC.URL == "" {
		return fmt.Errorf("agglayerClient.grpc.url is required for the LER preflight check (and later for step H)")
	}

	client, err := agglayer.NewAgglayerClient(agglayerClientCfg, log.GetDefaultLogger())
	if err != nil {
		return fmt.Errorf("create agglayer client: %w", err)
	}

	return runStepLERPreflight(ctx, cfg, client, readLocalExitRoot, targetBlock)
}

// runStepLERPreflight is the injectable core of RunStepLERPreflight (tests pass an agglayer
// client mock and a stub LER reader). It applies the same pending-certificate guard and settled
// LER derivation as Step H (fetchSettledNetworkState), then compares the L2 bridge's LER at the
// target block against the settled LER: whatever Step H would reject at the end of the pipeline,
// this rejects before Step A.
func runStepLERPreflight(
	ctx context.Context, cfg *Config, client agglayer.AgglayerClientInterface,
	readLER lerReaderFn, targetBlock uint64,
) error {
	settledLER, _, err := fetchSettledNetworkState(ctx, cfg, client)
	if err != nil {
		return err
	}

	l2LER, err := readLER(ctx, cfg.L2RPCURL, cfg.L2BridgeAddress, toBlockTag(targetBlock))
	if err != nil {
		return fmt.Errorf("read L2 bridge local exit root at target block %d: %w", targetBlock, err)
	}

	log.Infof("L2 bridge LER at target block %d: %s", targetBlock, l2LER.Hex())
	log.Infof("Agglayer settled LER:            %s", settledLER.Hex())

	if l2LER != settledLER {
		return fmt.Errorf(
			"target block %d has unsettled L2 bridge exits: L2 bridge LER %s != agglayer settled LER %s — "+
				"every L2→L1 bridge exit up to the target block must be settled by the agglayer before the "+
				"certificate can be generated; wait until the agglayer settles them (keep the aggsender running "+
				"after the sequencer halt until the last certificate settles) and re-run, or choose a target "+
				"block at or below the settled state",
			targetBlock, l2LER.Hex(), settledLER.Hex(),
		)
	}

	log.Info("✅ L2 bridge LER at the target block matches the agglayer settled LER")
	log.Info("STEP LER PREFLIGHT complete")
	return nil
}
