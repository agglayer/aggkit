package bridgesyncerlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tree"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/russross/meddler"
)

const bridgeTableName = "bridge"

// Read queries. Built once as compile-time constant expressions (no runtime string formatting) so the
// only interpolated token is the trusted bridgeTableName constant — never user input.
const (
	queryCountBridges    = "SELECT COUNT(*) FROM " + bridgeTableName
	queryMaxDepositCount = "SELECT MAX(deposit_count) FROM " + bridgeTableName
	queryAllBridges      = "SELECT * FROM " + bridgeTableName + " ORDER BY deposit_count ASC"
)

// buildProgressLogInterval is how often StoreBridges/BuildTree report persist/tree-build progress
// with an ETA.
const buildProgressLogInterval = 15 * time.Second

// newBuildProgress returns a progress function for a sequential phase over `total` items. Calling it
// with the number of items done so far logs (at most once per buildProgressLogInterval, plus a final
// line at done==total) the percentage, elapsed time and an ETA extrapolated from the average rate.
func (s *BridgeSyncerLite) newBuildProgress(phase string, total int) func(done int) {
	start := time.Now()
	lastLog := start
	return func(done int) {
		now := time.Now()
		if done < total && now.Sub(lastLog) < buildProgressLogInterval {
			return
		}
		lastLog = now
		elapsed := now.Sub(start)
		var eta time.Duration
		if done > 0 && done < total {
			eta = time.Duration(float64(elapsed) / float64(done) * float64(total-done))
		}
		s.log.Infof("%s: %d/%d leaves (%.1f%%), elapsed %s, ETA %s",
			phase, done, total, float64(done)/float64(total)*percentMultiplier,
			elapsed.Truncate(time.Second), eta.Truncate(time.Second))
	}
}

// BridgeSyncerLite is a minimal bridge syncer: it reads BridgeEvent logs (event data only, no
// calldata) from a chain, persists them to a sqlite DB and builds the bridge exit tree. It keeps no
// sync checkpoint, so it cannot resume — each Sync/AddBlocks call processes the block range it is
// given. The exit tree is byte-for-byte compatible with the canonical bridgesync exit tree.
type BridgeSyncerLite struct {
	cfg      Config
	log      *log.Logger
	db       *sql.DB
	client   *ethclient.Client
	contract *agglayerbridge.Agglayerbridge
	exitTree treetypes.FullTreer
}

// New returns a ready-to-use syncer. When cfg.DBPath is set, the sqlite DB is created/migrated and
// the syncer can persist bridges (Sync, AddBlocks, StoreBridges) and build the exit tree
// (BuildTree). When cfg.RPCURL is set, the syncer dials it and can read bridges from the chain
// (FetchBridges, Sync, AddBlocks, LatestBlock). At least one of the two must be set:
//   - DBPath only → DB-only mode: no chain access, only StoreBridges/BuildTree/GetBridges/
//     LocalExitRoot are available (useful to insert pre-collected bridges and build the tree
//     without any RPC calls).
//   - RPCURL only → fetch-only mode: no DB or tree, only FetchBridges/LatestBlock.
//   - both → full mode.
//
// Call Close when done.
func New(ctx context.Context, cfg Config, logger *log.Logger) (*BridgeSyncerLite, error) {
	if cfg.RPCURL == "" && cfg.DBPath == "" {
		return nil, errors.New("at least one of RPCURL or DBPath is required")
	}
	if cfg.BlockChunkSize == 0 {
		cfg.BlockChunkSize = defaultBlockChunkSize
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = defaultConcurrency
	}
	if logger == nil {
		logger = log.WithFields("module", "bridgesyncerlite")
	}

	database, exitTree, err := openDatabase(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	client, contract, err := dialBridge(ctx, cfg)
	if err != nil {
		if database != nil {
			_ = database.Close()
		}
		return nil, err
	}

	return &BridgeSyncerLite{
		cfg:      cfg,
		log:      logger,
		db:       database,
		client:   client,
		contract: contract,
		exitTree: exitTree,
	}, nil
}

// openDatabase migrates and opens the sqlite DB at dbPath and creates the append-only exit tree.
// Returns (nil, nil, nil) when dbPath is empty (fetch-only mode, no DB or tree).
func openDatabase(dbPath string) (*sql.DB, treetypes.FullTreer, error) {
	if dbPath == "" {
		return nil, nil, nil
	}
	if err := runMigrations(dbPath); err != nil {
		return nil, nil, fmt.Errorf("run migrations on %s: %w", dbPath, err)
	}
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open sqlite DB %s: %w", dbPath, err)
	}
	return database, tree.NewAppendOnlyTree(database, ""), nil
}

// dialBridge dials cfg.RPCURL and instantiates the bridge contract binding. Returns (nil, nil, nil)
// when cfg.RPCURL is empty (DB-only mode). On binding failure it closes the client it opened; the
// caller owns any other resources.
func dialBridge(
	ctx context.Context, cfg Config,
) (*ethclient.Client, *agglayerbridge.Agglayerbridge, error) {
	if cfg.RPCURL == "" {
		return nil, nil, nil
	}
	client, err := ethclient.DialContext(ctx, cfg.RPCURL)
	if err != nil {
		return nil, nil, fmt.Errorf("dial RPC %s: %w", cfg.RPCURL, err)
	}
	contract, err := agglayerbridge.NewAgglayerbridge(cfg.BridgeAddr, client)
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("instantiate bridge contract binding: %w", err)
	}
	return client, contract, nil
}

// Close releases the RPC client (if any) and DB connection (if any).
func (s *BridgeSyncerLite) Close() error {
	if s.client != nil {
		s.client.Close()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// LatestBlock returns the current head block of the connected chain.
func (s *BridgeSyncerLite) LatestBlock(ctx context.Context) (uint64, error) {
	if s.client == nil {
		return 0, errors.New("LatestBlock requires an RPC-backed syncer (set Config.RPCURL)")
	}
	return s.client.BlockNumber(ctx)
}

// FetchBridges reads every BridgeEvent in [fromBlock, toBlock] (querying the range in parallel) and
// returns the leaves sorted by deposit count, without persisting them or touching the exit tree. It
// aborts if any forbidden event is present in the range. Use this to recover the on-chain deposit
// order of a block range; use Sync to also persist and build the tree.
func (s *BridgeSyncerLite) FetchBridges(ctx context.Context, fromBlock, toBlock uint64) ([]BridgeLeaf, error) {
	bridges, err := s.fetchBridges(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, err
	}
	sort.Slice(bridges, func(i, j int) bool { return bridges[i].DepositCount < bridges[j].DepositCount })
	return bridges, nil
}

// Sync reads every BridgeEvent in [fromBlock, toBlock] (querying in parallel) and persists the
// leaves. It does NOT build the exit tree — call BuildTree once all bridges (across every Sync /
// AddBlocks call) are persisted. This is the initial full-history pass.
func (s *BridgeSyncerLite) Sync(ctx context.Context, fromBlock, toBlock uint64) error {
	bridges, err := s.fetchBridges(ctx, fromBlock, toBlock)
	if err != nil {
		return err
	}
	return s.StoreBridges(ctx, bridges)
}

// AddBlocks reads the BridgeEvents in [fromBlock, toBlock] and persists them. Like Sync it does not
// build the tree; it is meant for adding more logs after the initial Sync (e.g. the shadow-fork
// blocks) before a single BuildTree call assembles the whole tree.
func (s *BridgeSyncerLite) AddBlocks(ctx context.Context, fromBlock, toBlock uint64) error {
	bridges, err := s.fetchBridges(ctx, fromBlock, toBlock)
	if err != nil {
		return err
	}
	return s.StoreBridges(ctx, bridges)
}

// StoreBridges persists the given bridges (ordered by deposit count) in a single transaction. It
// does not touch the exit tree — building it is deferred to BuildTree, which runs once after all
// bridges are stored.
func (s *BridgeSyncerLite) StoreBridges(ctx context.Context, bridges []BridgeLeaf) error {
	if s.db == nil {
		return errors.New("StoreBridges requires a DB-backed syncer (set Config.DBPath)")
	}
	if len(bridges) == 0 {
		s.log.Info("no bridges to store")
		return nil
	}

	sort.Slice(bridges, func(i, j int) bool { return bridges[i].DepositCount < bridges[j].DepositCount })

	tx, err := db.NewTx(ctx, s.db)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if rerr := tx.Rollback(); rerr != nil {
				s.log.Errorf("rollback failed: %v", rerr)
			}
		}
	}()

	progress := s.newBuildProgress("persisting bridges", len(bridges))
	for i := range bridges {
		if err := meddler.Insert(tx, bridgeTableName, &bridges[i]); err != nil {
			return fmt.Errorf("insert bridge (deposit_count %d): %w", bridges[i].DepositCount, err)
		}
		progress(i + 1)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	s.log.Infof("stored %d bridges", len(bridges))
	return nil
}

// BuildTree builds the exit tree from every persisted bridge, in deposit-count order, and returns
// the resulting local exit root. The tree must be empty (build it once after all bridges have been
// stored): the lowest deposit count must be 0 and the counts must be contiguous, or the build fails.
func (s *BridgeSyncerLite) BuildTree(ctx context.Context) (common.Hash, error) {
	if s.db == nil || s.exitTree == nil {
		return common.Hash{}, errors.New("BuildTree requires a DB-backed syncer (set Config.DBPath)")
	}

	bridges, err := s.GetBridges(ctx)
	if err != nil {
		return common.Hash{}, err
	}
	if len(bridges) == 0 {
		s.log.Info("no bridges stored; exit tree is empty")
		return common.Hash{}, nil
	}

	tx, err := db.NewTx(ctx, s.db)
	if err != nil {
		return common.Hash{}, fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if rerr := tx.Rollback(); rerr != nil {
				s.log.Errorf("rollback failed: %v", rerr)
			}
		}
	}()

	progress := s.newBuildProgress("building exit tree", len(bridges))
	for i := range bridges {
		b := &bridges[i]
		if _, err := s.exitTree.PutLeaf(tx, b.BlockNum, b.BlockPos, treetypes.Leaf{
			Index: b.DepositCount,
			Hash:  b.Hash(),
		}); err != nil {
			return common.Hash{}, fmt.Errorf("add leaf (deposit_count %d) to exit tree: %w", b.DepositCount, err)
		}
		progress(i + 1)
	}

	if err := tx.Commit(); err != nil {
		return common.Hash{}, fmt.Errorf("commit transaction: %w", err)
	}
	committed = true

	root, err := s.LocalExitRoot()
	if err != nil {
		return common.Hash{}, err
	}
	s.log.Infof("built exit tree from %d bridges; local exit root = %s", len(bridges), root.Hex())
	return root, nil
}

// LocalExitRoot returns the current root of the exit tree, or the zero hash if the tree is empty.
func (s *BridgeSyncerLite) LocalExitRoot() (common.Hash, error) {
	if s.exitTree == nil {
		return common.Hash{}, errors.New("LocalExitRoot requires a DB-backed syncer (set Config.DBPath)")
	}
	root, err := s.exitTree.GetLastRoot(nil)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return common.Hash{}, nil
		}
		return common.Hash{}, fmt.Errorf("get last exit tree root: %w", err)
	}
	return root.Hash, nil
}

// CountBridges returns the number of persisted bridge leaves. It runs a single COUNT(*) aggregate
// query rather than loading every bridge into memory, so it stays O(1) on mainnet-scale histories.
func (s *BridgeSyncerLite) CountBridges(ctx context.Context) (int, error) {
	if s.db == nil {
		return 0, errors.New("CountBridges requires a DB-backed syncer (set Config.DBPath)")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, queryCountBridges).Scan(&count); err != nil {
		return 0, fmt.Errorf("count bridges: %w", err)
	}
	return count, nil
}

// NextDepositCount returns the deposit count the next inserted bridge should get: one past the
// highest deposit count currently persisted, or 0 when the DB is empty. It runs a single aggregate
// query (MAX(deposit_count)) rather than loading every bridge into memory, so it stays O(1) on
// mainnet-scale histories.
func (s *BridgeSyncerLite) NextDepositCount(ctx context.Context) (uint32, error) {
	if s.db == nil {
		return 0, errors.New("NextDepositCount requires a DB-backed syncer (set Config.DBPath)")
	}
	var maxDepositCount sql.NullInt64
	if err := s.db.QueryRowContext(ctx, queryMaxDepositCount).Scan(&maxDepositCount); err != nil {
		return 0, fmt.Errorf("query max deposit count: %w", err)
	}
	if !maxDepositCount.Valid {
		return 0, nil
	}
	return uint32(maxDepositCount.Int64) + 1, nil
}

// GetBridges returns all persisted bridge leaves ordered by deposit count.
func (s *BridgeSyncerLite) GetBridges(ctx context.Context) ([]BridgeLeaf, error) {
	if s.db == nil {
		return nil, errors.New("GetBridges requires a DB-backed syncer (set Config.DBPath)")
	}
	var ptrs []*BridgeLeaf
	if err := meddler.QueryAll(s.db, &ptrs, queryAllBridges); err != nil {
		return nil, fmt.Errorf("query bridges: %w", err)
	}
	bridges := make([]BridgeLeaf, len(ptrs))
	for i, p := range ptrs {
		bridges[i] = *p
	}
	return bridges, nil
}
