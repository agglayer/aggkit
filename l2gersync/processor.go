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
	deleteGERSql = "DELETE FROM imported_global_exit_root WHERE global_exit_root = $1;"
)

type BlockNum struct {
	Num uint64 `meddler:"num"`
}

type GlobalExitRootInfo struct {
	GlobalExitRoot  ethcommon.Hash `meddler:"global_exit_root,hash"`
	L1InfoTreeIndex uint32         `meddler:"l1_info_tree_index"`
	BlockNum        uint64         `meddler:"block_num"`
	BlockPosition   *uint64        `meddler:"block_pos"`
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
		BlockPosition:   &blockPosition,
	}
}

// newLegacyGlobalExitRootInfo creates a new GlobalExitRootInfo instance without block position
func newLegacyGlobalExitRootInfo(
	globalExitRoot ethcommon.Hash, l1InfoTreeIndex uint32, blockNum uint64) *GlobalExitRootInfo {
	return &GlobalExitRootInfo{
		GlobalExitRoot:  globalExitRoot,
		L1InfoTreeIndex: l1InfoTreeIndex,
		BlockNum:        blockNum,
		BlockPosition:   nil,
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

// handleGEREvent either inserts or removes the global exit root entry from `imported_global_exit_root` table,
func (p *processor) handleGEREvent(tx dbtypes.Txer, gerInfo *GlobalExitRootInfo, eventType GEREventType) error {
	switch eventType {
	case GEREventTypeInsert:
		// Insert the global exit root into the database
		if err := meddler.Insert(tx, "imported_global_exit_root", gerInfo); err != nil {
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
			BlockPos:       *gerInfo.BlockPosition,
			CreatedAt:      uint64(time.Now().Unix()),
		}
		if err := meddler.Insert(tx, "remove_ger_events", removeEvent); err != nil {
			return fmt.Errorf("failed to insert remove GER event (value=%x, block=%d, pos=%d): %w",
				gerInfo.GlobalExitRoot, gerInfo.BlockNum, *gerInfo.BlockPosition, err)
		}
	}

	return nil
}

// GetLastProcessedBlock retrieves the most recent block processed by the processor,
// including those without events.
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	var block BlockNum
	if err := meddler.QueryRow(
		p.database,
		&block,
		"SELECT num FROM block ORDER BY num DESC LIMIT 1;",
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return block.Num, nil
}

// getLatestL1InfoTreeIndex retrieves the highest L1InfoTreeIndex recorded in the imported_global_exit_root table
func (p *processor) getLatestL1InfoTreeIndex() (uint32, error) {
	var latestGERInfo GlobalExitRootInfo
	err := meddler.QueryRow(p.database, &latestGERInfo,
		`SELECT l1_info_tree_index FROM imported_global_exit_root
			ORDER BY l1_info_tree_index DESC LIMIT 1;`)
	if err != nil {
		return 0, db.ReturnErrNotFound(err)
	}
	return latestGERInfo.L1InfoTreeIndex, nil
}

// GetRemoveGEREvents retrieves remove GER events from the database with optional filters
// When no globalExitRoot filter is provided, returns the 50 most recent remove GER events
// When globalExitRoot filter is provided, returns all matching events for that GER
func (p *processor) GetRemoveGEREvents(
	ctx context.Context,
	globalExitRoot *ethcommon.Hash,
) ([]*RemoveGEREvent, error) {
	var events []*RemoveGEREvent
	var query string
	var args []interface{}

	// Check if no filters are provided - return 50 most recent events
	if globalExitRoot == nil {
		query = "SELECT * FROM remove_ger_events ORDER BY block_num DESC, block_pos DESC LIMIT 50"
	} else {
		// Filter by global exit root only
		query = "SELECT * FROM remove_ger_events WHERE global_exit_root = $1 ORDER BY block_num, block_pos"
		args = []interface{}{globalExitRoot.Hex()}
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
	e := GlobalExitRootInfo{}
	err := meddler.QueryRow(p.database, &e, `
		SELECT l1_info_tree_index, block_num, global_exit_root, block_pos
		FROM imported_global_exit_root
		WHERE l1_info_tree_index >= $1
		ORDER BY l1_info_tree_index ASC LIMIT 1;
	`, l1InfoTreeIndex)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e, db.ErrNotFound
		}
		return e, fmt.Errorf("failed to get first GER after index %d: %w", l1InfoTreeIndex, err)
	}

	return e, nil
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
		SELECT global_exit_root, l1_info_tree_index, block_num, block_pos
		FROM imported_global_exit_root
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
