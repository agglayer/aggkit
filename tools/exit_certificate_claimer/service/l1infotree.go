package claimer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
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

// OpenL1InfoTree opens the L1 Info Tree, anchored on the certificate's settlement GER.
//
// It first checks, read-only, whether settlementGER is already indexed in the local DB. If it is,
// the database is already caught up to settlement and no L1 sync is started, regardless of
// cfg.Enabled. If the GER is not yet indexed it must be synced from L1, which requires cfg.Enabled:
// when sync is disabled this is a hard error; when enabled it dials L1, wires a multidownloader-based
// syncer, and syncs until the settlement GER is indexed — then stops the sync (the DB now has
// everything the claimer needs). The handle returned serves the queries.
func OpenL1InfoTree(
	ctx context.Context, cfg L1SyncConfig, dbPath string, settlementGER common.Hash, logger *log.Logger,
) (L1InfoTreeQuerier, error) {
	return newL1InfoTree(ctx, cfg, dbPath, logger).Open(ctx, settlementGER)
}

// l1InfoTree gives a claimer its L1 Info Tree for as many runs as it serves (follow mode asks for it
// again for every new run, and again on every retry). l1infotreesync has no Close, so what is opened
// stays open for the life of the process; l1InfoTree therefore opens as little as possible:
//   - one read-only handle, opened once, answers every query of every run (the database is the same);
//   - at most one L1 sync at a time, which is kept running when an attempt to wait for a GER gives up
//     and reused by the next attempt, and only stopped once the GER it is needed for is indexed.
type l1InfoTree struct {
	cfg    L1SyncConfig
	dbPath string
	logger *log.Logger
	// syncCtx is the context L1 syncs run under: the life of the process, not of one attempt.
	syncCtx      context.Context
	pollInterval time.Duration
	// openReader and startSync are replaced in tests.
	openReader func(ctx context.Context) (L1InfoTreeQuerier, error)
	startSync  func(ctx context.Context) (*l1SyncSession, error)

	mu     sync.Mutex
	reader L1InfoTreeQuerier
	sync   *l1SyncSession
}

// l1SyncSession is a running L1 sync.
type l1SyncSession struct {
	// cancel stops the sync.
	cancel context.CancelFunc
	// done is closed when the sync stopped, by cancel or on its own.
	done chan struct{}
}

// stopped reports whether the sync is no longer running.
func (s *l1SyncSession) stopped() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// newL1InfoTree prepares the L1 Info Tree at dbPath; nothing is opened until Open. syncCtx bounds the L1
// syncs it starts.
func newL1InfoTree(syncCtx context.Context, cfg L1SyncConfig, dbPath string, logger *log.Logger) *l1InfoTree {
	t := &l1InfoTree{cfg: cfg, dbPath: dbPath, logger: logger, syncCtx: syncCtx, pollInterval: gerSyncPollInterval}
	t.openReader = func(ctx context.Context) (L1InfoTreeQuerier, error) {
		// NewReadOnly only attaches to the SQLite DB, so this is cheap and safe whether or not sync is
		// enabled.
		return l1infotreesync.NewReadOnly(ctx, dbPath)
	}
	t.startSync = t.startL1Sync
	return t
}

// Open returns the L1 Info Tree once settlementGER is indexed in it, syncing from L1 until then when
// needed (see OpenL1InfoTree). ctx bounds the wait: when it ends first, Open fails and the sync keeps
// running for the next call.
func (t *l1InfoTree) Open(ctx context.Context, settlementGER common.Hash) (L1InfoTreeQuerier, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.reader == nil {
		reader, err := t.openReader(ctx)
		if err != nil {
			return nil, fmt.Errorf("opening read-only L1 info tree at %q: %w", t.dbPath, err)
		}
		t.reader = reader
	}
	indexed, err := gerIndexed(t.reader, settlementGER)
	if err != nil {
		return nil, err
	}
	if indexed {
		t.logger.Infof("settlement GER %s already indexed in the L1 info tree; L1 sync not needed",
			settlementGER.Hex())
		t.stopSync()
		return t.reader, nil
	}

	// The settlement GER is not in the DB yet. It can only be obtained by syncing from L1, which
	// requires sync to be enabled.
	if !t.cfg.Enabled {
		return nil, fmt.Errorf(
			"settlement GER %s is not in the L1 info tree DB %q and L1 sync is disabled: "+
				"enable l1Sync (enabled=true with rpcUrl/contracts) so the claimer can sync it from L1",
			settlementGER.Hex(), t.dbPath)
	}

	if t.sync != nil && t.sync.stopped() {
		t.logger.Warnf("the L1 info tree sync stopped on its own; starting it again")
		t.sync = nil
	}
	if t.sync == nil {
		t.logger.Infof("settlement GER %s not yet indexed; starting L1 info tree sync from L1", settlementGER.Hex())
		session, err := t.startSync(t.syncCtx)
		if err != nil {
			return nil, err
		}
		t.sync = session
	} else {
		t.logger.Infof("settlement GER %s not yet indexed; the L1 info tree sync is still running", settlementGER.Hex())
	}

	// Sync only until the settlement GER is indexed: wait for it to land in the DB, then stop the sync.
	// Stopping it does not close the DB, and the read-only handle keeps serving reads from the
	// synced-up-to-settlement state.
	if err := waitForGEREvery(ctx, t.reader, settlementGER, t.pollInterval, t.logger); err != nil {
		return nil, fmt.Errorf("syncing settlement GER %s from L1: %w", settlementGER.Hex(), err)
	}
	t.stopSync()
	t.logger.Infof("settlement GER %s indexed; stopped L1 sync", settlementGER.Hex())
	return t.reader, nil
}

// stopSync stops the running L1 sync, if any.
func (t *l1InfoTree) stopSync() {
	if t.sync == nil {
		return
	}
	t.sync.cancel()
	t.sync = nil
}

// startL1Sync dials L1 and starts a multidownloader-based L1 Info Tree syncer, which runs until its
// context is cancelled.
func (t *l1InfoTree) startL1Sync(ctx context.Context) (*l1SyncSession, error) {
	cfg, dbPath, logger := t.cfg, t.dbPath, t.logger
	finality, err := resolveBlockFinality(cfg.BlockFinality)
	if err != nil {
		return nil, err
	}

	l1Client, err := etherman.NewRPCClient(ctx, logger, ethermanconfig.RPCClientConfig{
		URL:  cfg.RPCURL,
		Mode: ethermanconfig.RPCModeBasic,
	})
	if err != nil {
		return nil, fmt.Errorf("dialing L1 RPC %q: %w", aggkitcommon.URLSchemeHost(cfg.RPCURL), err)
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

	// Run the syncer under a child context: cancelling it stops the sync goroutines without closing
	// the DB.
	syncCtx, cancelSync := context.WithCancel(ctx)
	session := &l1SyncSession{cancel: cancelSync, done: make(chan struct{})}
	go func() {
		defer close(session.done)
		if startErr := l1MultiDownloader.Start(syncCtx); startErr != nil && syncCtx.Err() == nil {
			logger.Errorf("L1 multidownloader stopped: %v", startErr)
		}
	}()
	go syncer.Start(syncCtx)
	return session, nil
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
	return waitForGEREvery(ctx, syncer, ger, gerSyncPollInterval, logger)
}

// waitForGEREvery is waitForGER polling every interval.
func waitForGEREvery(
	ctx context.Context, syncer gerProber, ger common.Hash, interval time.Duration, logger *log.Logger,
) error {
	ticker := time.NewTicker(interval)
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
