package lastgersync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/lastgersync/migrations"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	ethCommon "github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

const (
	deleteGERSql = "DELETE FROM imported_global_exit_root WHERE global_exit_root = $1;"
)

type BlockNum struct {
	Num uint64 `meddler:"num"`
}

type GlobalExitRootInfo struct {
	GlobalExitRoot  ethCommon.Hash `meddler:"global_exit_root,hash"`
	L1InfoTreeIndex uint32         `meddler:"l1_info_tree_index"`
}

type gerInfoWithBlockNum struct {
	GlobalExitRoot  ethCommon.Hash `meddler:"global_exit_root,hash"`
	L1InfoTreeIndex uint32         `meddler:"l1_info_tree_index"`
	BlockNum        uint64         `meddler:"block_num"`
}

type GEREvent struct {
	BlockNum        uint64
	GlobalExitRoot  ethCommon.Hash
	L1InfoTreeIndex uint32
	IsRemove        bool
}

type processor struct {
	database *sql.DB
	log      *log.Logger
	compatibility.CompatibilityDataStorager[sync.RuntimeData]
}

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

// ProcessBlock stores a block and its related events in the lastgersync database
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	tx, err := db.NewTx(ctx, p.database)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() {
		if err != nil {
			p.log.Errorf("transaction rollback due to error: %v", err)
			if errRollback := tx.Rollback(); errRollback != nil {
				log.Errorf("error while rolling back tx %v", errRollback)
			}
		}
	}()

	if err := meddler.Insert(tx, "block", &BlockNum{Num: block.Num}); err != nil {
		return err
	}

	for _, genericEvt := range block.Events {
		event, ok := genericEvt.(*Event)
		if !ok {
			return fmt.Errorf("unexpected event type %T", event)
		}

		switch {
		case event.GERInfo != nil:
			gerEvent := &GEREvent{
				BlockNum:        block.Num,
				GlobalExitRoot:  event.GERInfo.GlobalExitRoot,
				L1InfoTreeIndex: event.GERInfo.L1InfoTreeIndex,
			}
			if err := p.handleGERInsertion(tx, gerEvent); err != nil {
				return err
			}

		case event.GEREvent != nil:
			if err := p.handleGEREvent(tx, event.GEREvent); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	p.log.Debugf("processed %d events until block %d", len(block.Events), block.Num)
	return nil
}

// handleGERInsertion inserts the given global exit root entry to `imported_global_exit_root`
func (*processor) handleGERInsertion(tx db.Txer, gerInfo *GEREvent) error {
	gerInfoWithBlockNum := &gerInfoWithBlockNum{
		GlobalExitRoot:  gerInfo.GlobalExitRoot,
		L1InfoTreeIndex: gerInfo.L1InfoTreeIndex,
		BlockNum:        gerInfo.BlockNum,
	}

	if err := meddler.Insert(tx, "imported_global_exit_root", gerInfoWithBlockNum); err != nil {
		return fmt.Errorf("failed to insert GER entry (value=%x, block=%d): %w",
			gerInfo.GlobalExitRoot, gerInfo.BlockNum, err)
	}
	return nil
}

// handleGEREvent either inserts or removes the global exit root entry from `imported_global_exit_root` table,
func (p *processor) handleGEREvent(tx db.Txer, event *GEREvent) error {
	if event.IsRemove {
		_, err := tx.Exec(deleteGERSql, event.GlobalExitRoot.Hex())
		if err != nil {
			return fmt.Errorf("failed to remove global exit root %s: %w", event.GlobalExitRoot.Hex(), err)
		}
	} else {
		err := p.handleGERInsertion(tx, event)
		if err != nil {
			return fmt.Errorf("failed to insert global exit root %s: %w", event.GlobalExitRoot.Hex(), err)
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

// Reorg removes all blocks and associated data starting from a specific block number from lastgersync database
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
		SELECT l1_info_tree_index, global_exit_root
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
