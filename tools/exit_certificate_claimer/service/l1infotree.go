package claimer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// L1 Info Tree syncer timing defaults, mirroring aggkit's [L1InfoTreeSync] config defaults. They
// must be non-zero: a zero WaitForNewBlocksPeriod makes the downloader's time.NewTicker panic.
const (
	defaultWaitForNewBlocksPeriod     = 100 * time.Millisecond
	defaultRetryAfterErrorPeriod      = time.Second
	defaultMaxRetryAttemptsAfterError = -1
)

// gerSyncPollInterval is how often OpenL1InfoTree polls the DB for the settlement GER while the L1
// sync catches up to it.
const gerSyncPollInterval = 2 * time.Second

// L1InfoTreeQuerier is the subset of the l1infotreesync API the claimer needs to assemble the
// rollup-side claimAsset parameters. *l1infotreesync.L1InfoTreeSync satisfies it.
type L1InfoTreeQuerier interface {
	GetInfoByGlobalExitRoot(ger common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetLocalExitRoot(ctx context.Context, networkID uint32, rollupExitRoot common.Hash) (common.Hash, error)
	GetRollupExitTreeMerkleProof(ctx context.Context, networkID uint32, root common.Hash) (treetypes.Proof, error)
}

// gerProber is the minimal surface gerIndexed/waitForGER need to check whether a GER is indexed.
// *l1infotreesync.L1InfoTreeSync satisfies it.
type gerProber interface {
	GetInfoByGlobalExitRoot(ger common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error)
}

// OpenL1InfoTree opens the L1 Info Tree syncer, anchored on the certificate's settlement GER.
//
// It first checks, read-only, whether settlementGER is already indexed in the local DB. If it is,
// the database is already caught up to settlement and no L1 sync is started — the read-only syncer
// is returned regardless of cfg.Enabled. If the GER is not yet indexed it must be synced from L1,
// which requires cfg.Enabled: when sync is disabled this is a hard error; when enabled it dials L1,
// wires a multidownloader-based syncer, and syncs until the settlement GER is indexed — then stops
// the sync (the DB now has everything the claimer needs) and returns the syncer for queries.
func OpenL1InfoTree(
	ctx context.Context, cfg L1SyncConfig, dbPath string, settlementGER common.Hash, logger *log.Logger,
) (*l1infotreesync.L1InfoTreeSync, error) {
	// Read-only probe for the settlement GER. NewReadOnly only attaches to the SQLite DB, so this is
	// cheap and safe whether or not sync is enabled.
	readOnly, err := l1infotreesync.NewReadOnly(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening read-only L1 info tree at %q: %w", dbPath, err)
	}
	indexed, err := gerIndexed(readOnly, settlementGER)
	if err != nil {
		return nil, err
	}
	if indexed {
		logger.Infof("settlement GER %s already indexed in the L1 info tree; L1 sync not needed",
			settlementGER.Hex())
		return readOnly, nil
	}

	// The settlement GER is not in the DB yet. It can only be obtained by syncing from L1, which
	// requires sync to be enabled. (The read-only handle above stays open — L1InfoTreeSync exposes no
	// Close — but it is just an idle WAL reader alongside the syncer opened below.)
	if !cfg.Enabled {
		return nil, fmt.Errorf(
			"settlement GER %s is not in the L1 info tree DB %q and L1 sync is disabled: "+
				"enable l1Sync (enabled=true with rpcUrl/contracts) so the claimer can sync it from L1",
			settlementGER.Hex(), dbPath)
	}

	logger.Infof("settlement GER %s not yet indexed; starting L1 info tree sync from L1", settlementGER.Hex())

	finality, err := resolveBlockFinality(cfg.BlockFinality)
	if err != nil {
		return nil, err
	}

	l1Client, err := etherman.NewRPCClient(ctx, logger, ethermanconfig.RPCClientConfig{
		URL:  cfg.RPCURL,
		Mode: ethermanconfig.RPCModeBasic,
	})
	if err != nil {
		return nil, fmt.Errorf("dialing L1 RPC %q: %w", cfg.RPCURL, err)
	}

	// The multidownloader keeps its own storage and reorg processor next to the L1 Info Tree DB,
	// so no separate reorg detector is needed.
	mdCfg := multidownloader.NewConfigDefault("l1infotree", filepath.Dir(dbPath))
	mdCfg.BlockFinality = finality
	if cfg.SyncBlockChunkSize > 0 {
		mdCfg.BlockChunkSize = uint32(cfg.SyncBlockChunkSize)
	}

	l1MultiDownloader, err := multidownloader.NewEVMMultidownloader(
		logger, mdCfg, "l1",
		l1Client, // ethClient
		l1Client, // rpcClient
		nil,      // storage (created inside the multidownloader)
		nil,      // blockNotifierManager (created inside the multidownloader)
		nil,      // reorgProcessor (created inside the multidownloader)
	)
	if err != nil {
		return nil, fmt.Errorf("creating L1 multidownloader: %w", err)
	}

	syncer, err := l1infotreesync.NewMultidownloadBased(
		ctx,
		l1infotreesync.Config{
			DBPath:                     dbPath,
			GlobalExitRootAddr:         common.HexToAddress(cfg.GlobalExitRootAddr),
			RollupManagerAddr:          common.HexToAddress(cfg.RollupManagerAddr),
			BlockFinality:              finality,
			SyncBlockChunkSize:         cfg.SyncBlockChunkSize,
			InitialBlock:               cfg.InitialBlock,
			WaitForNewBlocksPeriod:     configtypes.Duration{Duration: defaultWaitForNewBlocksPeriod},
			RetryAfterErrorPeriod:      configtypes.Duration{Duration: defaultRetryAfterErrorPeriod},
			MaxRetryAttemptsAfterError: defaultMaxRetryAttemptsAfterError,
		},
		l1MultiDownloader,
		l1infotreesync.FlagNone,
	)
	if err != nil {
		return nil, fmt.Errorf("creating L1 info tree syncer: %w", err)
	}

	// Initialize must run after NewMultidownloadBased has registered the syncer.
	if err := l1MultiDownloader.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initializing L1 multidownloader: %w", err)
	}

	// Sync only until the settlement GER is indexed: run the syncer under a child context, wait for
	// the GER to land in the DB, then cancel it. Cancelling stops the sync goroutines without closing
	// the DB, so the returned syncer keeps serving reads from the synced-up-to-settlement state.
	syncCtx, cancelSync := context.WithCancel(ctx)
	go func() {
		if startErr := l1MultiDownloader.Start(syncCtx); startErr != nil && syncCtx.Err() == nil {
			logger.Errorf("L1 multidownloader stopped: %v", startErr)
		}
	}()
	go syncer.Start(syncCtx)

	err = waitForGER(syncCtx, syncer, settlementGER, logger)
	cancelSync()
	if err != nil {
		return nil, fmt.Errorf("syncing settlement GER %s from L1: %w", settlementGER.Hex(), err)
	}

	logger.Infof("settlement GER %s indexed; stopped L1 sync", settlementGER.Hex())
	return syncer, nil
}

// resolveBlockFinality maps the configured l1Sync.blockFinality string to a BlockNumberFinality,
// defaulting to LatestBlock when the string is empty. An unparseable value is a hard error.
func resolveBlockFinality(blockFinality string) (aggkittypes.BlockNumberFinality, error) {
	if blockFinality == "" {
		return aggkittypes.LatestBlock, nil
	}
	f, err := aggkittypes.NewBlockNumberFinality(blockFinality)
	if err != nil {
		return aggkittypes.BlockNumberFinality{},
			fmt.Errorf("invalid l1Sync.blockFinality %q: %w", blockFinality, err)
	}
	return *f, nil
}

// waitForGER polls the L1 info tree DB until the given GER is indexed, returning nil once it is.
// It returns the context error if ctx is cancelled (e.g. the operator interrupts the process)
// before the GER appears, or any query error from the probe.
func waitForGER(
	ctx context.Context, syncer gerProber, ger common.Hash, logger *log.Logger,
) error {
	ticker := time.NewTicker(gerSyncPollInterval)
	defer ticker.Stop()
	for {
		indexed, err := gerIndexed(syncer, ger)
		if err != nil {
			return err
		}
		if indexed {
			return nil
		}
		logger.Debugf("waiting for settlement GER %s to be indexed by the L1 sync", ger.Hex())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// gerIndexed reports whether the given Global Exit Root is already present in the L1 info tree DB.
// A missing GER (db.ErrNotFound) is reported as not indexed; any other error is propagated.
func gerIndexed(syncer gerProber, ger common.Hash) (bool, error) {
	_, err := syncer.GetInfoByGlobalExitRoot(ger)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, db.ErrNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("querying L1 info tree for GER %s: %w", ger.Hex(), err)
	}
}
