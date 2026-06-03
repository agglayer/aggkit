package exit_certificate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
)

// liteDBSuffixes are the sqlite file plus its WAL/SHM sidecars, copied/removed together so the lite
// syncer DB is moved as a consistent unit.
var liteDBSuffixes = []string{"", "-wal", "-shm"}

// g1LiteDBPath returns the lite syncer sqlite file Step G1 populates with the genesis→fork L2
// bridges. It lives directly in the output dir (alongside the other step files). Step G2 copies it
// to g2LiteDBPath and works on that copy, leaving this one untouched.
func g1LiteDBPath(cfg *Config) string {
	return filepath.Join(cfg.Options.OutputDir, "step-g1-l2bridgesyncerlite.sqlite")
}

// g2LiteDBPath returns the lite syncer sqlite file Step G2 works on: a copy of g1LiteDBPath onto
// which G2 appends the replayed bridges and builds the exit tree, so Step G1's DB stays intact and
// reusable across G2 re-runs.
func g2LiteDBPath(cfg *Config) string {
	return filepath.Join(cfg.Options.OutputDir, "step-g-l2bridgesyncerlite.sqlite")
}

// RunStepG1 persists the L2 bridge history Step G2 needs and resolves the block Step G2 forks at.
//
// It syncs every L2 bridge from genesis up to targetBlock against the real L2 (cfg.L2RPCURL) with
// the lite bridge syncer, persisting them (no tree yet) so Step G2 can insert the replayed bridges
// on top and build the whole exit tree once. The full-history scan runs against the fast real L2
// rather than the slow Anvil fork. The shadow-fork block is exactly the resolved targetBlock (the
// lite syncer fetches that range, no overshoot), so Anvil forks there aligned to the contract's
// state at that block.
func RunStepG1(ctx context.Context, cfg *Config, targetBlock uint64) (*StepG1Result, error) {
	log.Info("═══════════════════════════════════════════")
	log.Info(" STEP G1 - Resolve shadow-fork block and sync l2 bridges")
	log.Info("═══════════════════════════════════════════")

	// Build the bridge history from genesis up to targetBlock with the lite bridge syncer, persisting
	// it so Step G2 can append the replayed shadow-fork leaves on top.
	if err := syncLiteToBlock(ctx, cfg, targetBlock); err != nil {
		return nil, fmt.Errorf("lite-sync L2 bridges up to block %d: %w", targetBlock, err)
	}
	log.Infof("STEP G1 complete: L2 bridges lite-synced up to block %d (shadow-fork block); DB: %s",
		targetBlock, g1LiteDBPath(cfg))
	return &StepG1Result{ShadowForkBlock: targetBlock}, nil
}

// syncLiteToBlock persists all L2 bridges from genesis up to targetBlock with the lite bridge
// syncer, reading BridgeEvent logs from the real L2 (cfg.L2RPCURL) in parallel into the DB at
// g1LiteDBPath(cfg) (directly in the output dir). It does NOT build the exit tree — Step G2 builds
// it once, after appending the replayed shadow-fork bridges, so the tree is assembled a single time
// from the full set. Any pre-existing DB is deleted first so a re-run reflects the current chain
// state. It aborts (via the lite syncer) if the chain emitted any event that would invalidate a
// BridgeEvent-only reconstruction (token remappings, legacy migrations, LET rollbacks/advances).
func syncLiteToBlock(ctx context.Context, cfg *Config, targetBlock uint64) error {
	dbPath := g1LiteDBPath(cfg)
	// Delete any pre-existing lite syncer DB (and its WAL/SHM sidecars) so a re-run reflects the
	// current chain state rather than resuming/duplicating a previous sync. The DB lives directly in
	// the output dir, so only these files are removed — the other step files are left untouched.
	if err := removeLiteDB(dbPath); err != nil {
		return err
	}

	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{
		RPCURL:                    cfg.L2RPCURL,
		BridgeAddr:                cfg.L2BridgeAddress,
		DBPath:                    dbPath,
		BlockChunkSize:            uint64(cfg.Options.BlockRange),
		Concurrency:               cfg.Options.ConcurrencyLimit,
		IgnoreUnsupportedL2Events: cfg.Options.IgnoreUnsupportedL2Events,
	}, log.WithFields("module", "exit-cert-bridgesyncerlite"))
	if err != nil {
		return err
	}
	defer func() {
		if cerr := syncer.Close(); cerr != nil {
			log.Warnf("error closing lite bridge syncer: %v", cerr)
		}
	}()

	log.Infof("Lite-syncing L2 bridges [0..%d] against the real L2 (%s)...", targetBlock, cfg.L2RPCURL)
	if err := syncer.Sync(ctx, 0, targetBlock); err != nil {
		return err
	}

	bridges, err := syncer.GetBridges(ctx)
	if err != nil {
		return err
	}
	log.Infof("Lite-synced %d L2 bridges up to block %d into %s (exit tree deferred to Step G2)",
		len(bridges), targetBlock, dbPath)
	return nil
}

// removeLiteDB deletes the lite syncer sqlite file and its WAL/SHM sidecars if present, logging when
// an existing DB is removed. Missing files are not an error.
func removeLiteDB(dbPath string) error {
	if _, err := os.Stat(dbPath); err == nil {
		log.Infof("Removing existing lite syncer DB %s", dbPath)
	}
	for _, suffix := range liteDBSuffixes {
		p := dbPath + suffix
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove lite syncer DB file %s: %w", p, err)
		}
	}
	return nil
}

// copyLiteDB copies the lite syncer sqlite file at srcPath (and its WAL/SHM sidecars, if present) to
// dstPath, replacing any existing destination first. Step G2 uses it to work on a copy of Step G1's
// DB, leaving the original intact. srcPath's main file must exist; absent sidecars are skipped.
func copyLiteDB(srcPath, dstPath string) error {
	if err := removeLiteDB(dstPath); err != nil {
		return err
	}
	for _, suffix := range liteDBSuffixes {
		src := srcPath + suffix
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				continue // sidecar may not exist (e.g. WAL checkpointed on close)
			}
			return fmt.Errorf("stat lite syncer DB file %s: %w", src, err)
		}
		if err := copyFile(src, dstPath+suffix); err != nil {
			return fmt.Errorf("copy lite syncer DB file %s: %w", src, err)
		}
	}
	return nil
}

// copyFile copies the contents of src to dst (truncating dst), streaming so large DBs are not held
// in memory.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
