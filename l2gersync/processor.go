package l2gersync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/compatibility"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/l2gersync/migrations"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

const (
	deleteGERSql = "DELETE FROM imported_global_exit_root_v2 WHERE global_exit_root = $1;"
)

type GlobalExitRootInfo struct {
	GlobalExitRoot  ethcommon.Hash `meddler:"global_exit_root,hash"`
	L1InfoTreeIndex uint32         `meddler:"l1_info_tree_index"`
	BlockNum        uint64         `meddler:"block_num"`
	BlockPosition   uint64         `meddler:"block_pos"`
	// Timestamp of the L2 block where the GER was injected. Nullable: rows written before this
	// column existed have it NULL, and GetFirstGERAfterL1InfoTreeIndex backfills it lazily from
	// the L2 RPC (see L2GERSync.GetFirstGERAfterL1InfoTreeIndex) rather than eagerly migrating
	// every historical row. Always NULL for chains synced in Legacy mode: BlockNum there is the
	// polling head rather than the real injection block, so no accurate timestamp can be derived
	// (see evm_downloader_legacy.go).
	Timestamp *uint64 `meddler:"block_timestamp"`
	Removed   bool    `meddler:"-"`
	// BlockHash is the hash of the block BlockNum, as stored in the block table. It is not a column of
	// imported_global_exit_root_v2: GetFirstGERAfterL1InfoTreeIndex joins it from the block table in the
	// same statement that reads the row, so it is the hash of the block the GER belonged to when it was
	// read. Zero if that block has no hash, or when the GER was read by any other query.
	BlockHash ethcommon.Hash `meddler:"-"`
}

// globalExitRootInfoWithBlockHash is the read model of an imported_global_exit_root_v2 row joined with
// the hash of its block (see GetFirstGERAfterL1InfoTreeIndex). It can't be GlobalExitRootInfo itself:
// meddler.Insert writes every meddler-tagged field and block_hash is not a column of
// imported_global_exit_root_v2, and meddler doesn't flatten embedded structs. It has exactly the fields
// of GlobalExitRootInfo, in the same order, so that GlobalExitRootInfo(row) compiles only while both
// match.
type globalExitRootInfoWithBlockHash struct {
	GlobalExitRoot  ethcommon.Hash `meddler:"global_exit_root,hash"`
	L1InfoTreeIndex uint32         `meddler:"l1_info_tree_index"`
	BlockNum        uint64         `meddler:"block_num"`
	BlockPosition   uint64         `meddler:"block_pos"`
	Timestamp       *uint64        `meddler:"block_timestamp"`
	Removed         bool           `meddler:"-"`
	BlockHash       ethcommon.Hash `meddler:"block_hash,hash"`
}

// RemoveGEREvent represents a remove GER event stored in the database
type RemoveGEREvent struct {
	GlobalExitRoot ethcommon.Hash `meddler:"global_exit_root,hash"`
	BlockNum       uint64         `meddler:"block_num"`
	BlockPos       uint64         `meddler:"block_pos"`
	CreatedAt      uint64         `meddler:"created_at"`
}

// newGlobalExitRootInfo creates a new GlobalExitRootInfo instance with the provided parameters.
func newGlobalExitRootInfo(
	globalExitRoot ethcommon.Hash, l1InfoTreeIndex uint32, blockNum uint64, blockPosition uint64,
) *GlobalExitRootInfo {
	return &GlobalExitRootInfo{
		GlobalExitRoot:  globalExitRoot,
		L1InfoTreeIndex: l1InfoTreeIndex,
		BlockNum:        blockNum,
		BlockPosition:   blockPosition,
	}
}

type processor struct {
	database *sql.DB
	log      *log.Logger
	compatibility.CompatibilityDataStorager[sync.RuntimeData]
}

// newProcessor initializes a new processor with the given database path.
func newProcessor(dbPath string) (*processor, error) {
	err := migrations.RunMigrations(dbPath)
	if err != nil {
		return nil, err
	}
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}
	logger := log.WithFields("module", reorgDetectorID)
	return &processor{
		database: database,
		log:      logger,
		CompatibilityDataStorager: compatibility.NewKeyValueToCompatibilityStorage[sync.RuntimeData](
			db.NewKeyValueStorage(database),
			reorgDetectorID,
		),
	}, nil
}

// ProcessBlock stores a block and its related events in the l2 ger syncer database
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	tx, err := db.NewTx(ctx, p.database)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	shouldRollback := true

	defer func() {
		if shouldRollback {
			p.log.Errorf("transaction rollback due to an error")
			if errRollback := tx.Rollback(); errRollback != nil {
				log.Errorf("error while rolling back tx %v", errRollback)
			}
		}
	}()

	if _, err := tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, block.Num, block.Hash.String()); err != nil {
		return fmt.Errorf("failed to insert block %d: %w", block.Num, err)
	}

	for _, genericEvt := range block.Events {
		event, ok := genericEvt.(*Event)
		if !ok {
			return fmt.Errorf("unexpected event type %T", genericEvt)
		}

		if err := p.handleGEREvent(tx, event.GERInfo, event.EventType); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	shouldRollback = false // Commit was successful, no need to rollback
	p.log.Debugf("processed %d events until block %d", len(block.Events), block.Num)
	return nil
}

// handleGEREvent either inserts or removes the global exit root entry from `imported_global_exit_root_v2` table,
func (p *processor) handleGEREvent(tx dbtypes.Txer, gerInfo *GlobalExitRootInfo, eventType GEREventType) error {
	switch eventType {
	case GEREventTypeInsert:
		// Insert the global exit root into the database
		if err := meddler.Insert(tx, "imported_global_exit_root_v2", gerInfo); err != nil {
			return fmt.Errorf("failed to insert imported global exit root (value=%x, block=%d): %w",
				gerInfo.GlobalExitRoot, gerInfo.BlockNum, err)
		}
	case GEREventTypeRemove:
		// Remove the global exit root from the database
		_, err := tx.Exec(deleteGERSql, gerInfo.GlobalExitRoot.Hex())
		if err != nil {
			return fmt.Errorf("failed to remove global exit root %s: %w", gerInfo.GlobalExitRoot.Hex(), err)
		}

		// Store the remove event in the new table
		removeEvent := &RemoveGEREvent{
			GlobalExitRoot: gerInfo.GlobalExitRoot,
			BlockNum:       gerInfo.BlockNum,
			BlockPos:       gerInfo.BlockPosition,
			CreatedAt:      uint64(time.Now().Unix()),
		}
		if err := meddler.Insert(tx, "remove_ger_events", removeEvent); err != nil {
			return fmt.Errorf("failed to insert remove GER event (value=%x, block=%d, pos=%d): %w",
				gerInfo.GlobalExitRoot, gerInfo.BlockNum, gerInfo.BlockPosition, err)
		}
	}

	return nil
}

// GetLastProcessedBlock retrieves the most recent block processed by the processor,
// including those without events.
// Returns (0, false, nil) if no blocks have been processed yet.
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, bool, error) {
	var lastProcessedBlockNum uint64
	row := p.database.QueryRowContext(ctx, "SELECT num FROM block ORDER BY num DESC LIMIT 1;")
	if err := row.Scan(&lastProcessedBlockNum); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return lastProcessedBlockNum, true, nil
}

// getLatestL1InfoTreeIndex retrieves the highest L1InfoTreeIndex recorded in the imported_global_exit_root_v2 table
func (p *processor) getLatestL1InfoTreeIndex() (uint32, error) {
	var latestGERInfo GlobalExitRootInfo
	err := meddler.QueryRow(p.database, &latestGERInfo,
		`SELECT l1_info_tree_index FROM imported_global_exit_root_v2
			ORDER BY l1_info_tree_index DESC LIMIT 1;`)
	if err != nil {
		return 0, db.ReturnErrNotFound(err)
	}
	return latestGERInfo.L1InfoTreeIndex, nil
}

// GetRemoveGEREvents retrieves remove GER events from the database with optional filters
// Returns remove GER events limited by the limit parameter
// If globalExitRoot is provided, filters by that GER as well
func (p *processor) GetRemoveGEREvents(
	ctx context.Context,
	globalExitRoot *ethcommon.Hash,
	limit uint32,
) ([]*RemoveGEREvent, error) {
	var (
		events []*RemoveGEREvent
		query  string
		args   []interface{}
	)

	// Build query based on whether globalExitRoot filter is provided
	if globalExitRoot == nil {
		// No filter - return most recent events with limit
		query = "SELECT * FROM remove_ger_events ORDER BY block_num DESC, block_pos DESC LIMIT $1"
		args = []interface{}{limit}
	} else {
		// Filter by global exit root and apply limit
		query = "SELECT * FROM remove_ger_events WHERE global_exit_root = $1 ORDER BY block_num DESC, block_pos DESC LIMIT $2"
		args = []interface{}{globalExitRoot.Hex(), limit}
	}

	if err := meddler.QueryAll(p.database, &events, query, args...); err != nil {
		return nil, fmt.Errorf("failed to query remove GER events: %w", err)
	}
	return events, nil
}

// Reorg removes all blocks and associated data starting from a specific block number from l2 ger sync database
func (p *processor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	_, err := p.database.ExecContext(ctx, `DELETE FROM block WHERE num >= $1;`, firstReorgedBlock)
	if err != nil {
		return fmt.Errorf("error processing reorg: %w", err)
	}
	return nil
}

// GetFirstGERAfterL1InfoTreeIndex returns the first GER injected into the chain that is associated with
// or greater than the specified l1InfoTreeIndex.
func (p *processor) GetFirstGERAfterL1InfoTreeIndex(
	ctx context.Context, l1InfoTreeIndex uint32) (GlobalExitRootInfo, error) {
	// the hash of the GER's block is joined in the same statement, so it is the hash of the block the GER
	// belonged to when it was read (see GlobalExitRootInfo.BlockHash)
	rows, err := p.database.QueryContext(ctx, `
		SELECT g.l1_info_tree_index, g.block_num, g.global_exit_root, g.block_pos, g.block_timestamp,
			COALESCE(b.hash, '') AS block_hash
		FROM imported_global_exit_root_v2 g
		LEFT JOIN block b ON b.num = g.block_num
		WHERE g.l1_info_tree_index >= $1
		ORDER BY g.l1_info_tree_index ASC LIMIT 1;
	`, l1InfoTreeIndex)
	if err != nil {
		return GlobalExitRootInfo{}, fmt.Errorf("failed to get first GER after index %d: %w", l1InfoTreeIndex, err)
	}
	row := globalExitRootInfoWithBlockHash{}
	// meddler.ScanRow closes rows
	if err = meddler.ScanRow(rows, &row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GlobalExitRootInfo{}, db.ErrNotFound
		}
		return GlobalExitRootInfo{}, fmt.Errorf("failed to get first GER after index %d: %w", l1InfoTreeIndex, err)
	}

	return GlobalExitRootInfo(row), nil
}

// getBlockHash returns the hash stored for the given block. found is false if the block is not stored
// or it has no hash (blocks synced before the hash column existed).
func (p *processor) getBlockHash(ctx context.Context, blockNum uint64) (hash ethcommon.Hash, found bool, err error) {
	var hashStr sql.NullString
	err = p.database.QueryRowContext(ctx, `SELECT hash FROM block WHERE num = $1`, blockNum).Scan(&hashStr)
	if errors.Is(err, sql.ErrNoRows) {
		return ethcommon.Hash{}, false, nil
	}
	if err != nil {
		return ethcommon.Hash{}, false, fmt.Errorf("failed to get the hash of block %d: %w", blockNum, err)
	}
	if !hashStr.Valid || hashStr.String == "" {
		return ethcommon.Hash{}, false, nil
	}
	return ethcommon.HexToHash(hashStr.String), true, nil
}

// GetLastGER retrieves the most recently injected global exit root, i.e. the row with the
// highest L1 info tree index. Returns db.ErrNotFound if no GER has been injected yet.
func (p *processor) GetLastGER(ctx context.Context) (GlobalExitRootInfo, error) {
	e := GlobalExitRootInfo{}
	err := meddler.QueryRow(p.database, &e, `
		SELECT l1_info_tree_index, block_num, global_exit_root, block_pos, block_timestamp
		FROM imported_global_exit_root_v2
		ORDER BY l1_info_tree_index DESC LIMIT 1;
	`)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e, db.ErrNotFound
		}
		return e, fmt.Errorf("failed to get last injected GER: %w", err)
	}

	return e, nil
}

// UpdateTimestamp backfills the timestamp column of an already-inserted injected GER row,
// identified by its (block_num, block_pos) primary key. Used when a row predates timestamp
// persistence (nullable column, see l2gersync0006.sql) — see
// L2GERSync.GetFirstGERAfterL1InfoTreeIndex, which resolves the missing value from the L2 RPC and
// calls this to persist it so the RPC lookup is not repeated on every future read of that row.
func (p *processor) UpdateTimestamp(ctx context.Context, blockNum, blockPosition, timestamp uint64) error {
	_, err := p.database.ExecContext(ctx,
		`UPDATE imported_global_exit_root_v2 SET block_timestamp = $1 WHERE block_num = $2 AND block_pos = $3;`,
		timestamp, blockNum, blockPosition)
	if err != nil {
		return fmt.Errorf("failed to backfill timestamp for block=%d, pos=%d: %w", blockNum, blockPosition, err)
	}
	return nil
}

// GetInjectedGERsForRange retrieves all injected global exit roots within a specified block range.
// It returns a map where the keys are the global exit root hashes and the values are the
// corresponding GlobalExitRootInfo containing the L1 info tree index and block number.
func (p *processor) GetInjectedGERsForRange(
	ctx context.Context, fromBlock, toBlock uint64) (map[ethcommon.Hash]GlobalExitRootInfo, error) {
	if fromBlock > toBlock {
		return nil, fmt.Errorf("invalid block range: fromBlock(%d) > toBlock(%d)", fromBlock, toBlock)
	}

	var results []*GlobalExitRootInfo

	err := meddler.QueryAll(p.database, &results, `
		SELECT global_exit_root, l1_info_tree_index, block_num, block_pos, block_timestamp
		FROM imported_global_exit_root_v2
		WHERE block_num >= $1 AND block_num <= $2;
	`, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("failed to query injected GERs for block range [%d..%d]: %w",
			fromBlock, toBlock, err)
	}

	gerMap := make(map[ethcommon.Hash]GlobalExitRootInfo, len(results))
	for _, gerInfo := range results {
		gerMap[gerInfo.GlobalExitRoot] = *gerInfo
	}

	return gerMap, nil
}
