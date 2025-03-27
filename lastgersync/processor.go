package lastgersync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/lastgersync/migrations"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	ethCommon "github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

type BlockNum struct {
	Num uint64 `meddler:"num"`
}

type Event struct {
	GlobalExitRoot  ethCommon.Hash `meddler:"global_exit_root,hash"`
	L1InfoTreeIndex uint32         `meddler:"l1_info_tree_index"`
}

type eventWithBlockNum struct {
	GlobalExitRoot  ethCommon.Hash `meddler:"global_exit_root,hash"`
	L1InfoTreeIndex uint32         `meddler:"l1_info_tree_index"`
	BlockNum        uint64         `meddler:"block_num"`
}

type processor struct {
	db  *sql.DB
	log *log.Logger
}

func newProcessor(dbPath string) (*processor, error) {
	err := migrations.RunMigrations(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}
	db, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}
	logger := log.WithFields("lastger-syncer", reorgDetectorID)
	return &processor{
		db:  db,
		log: logger,
	}, nil
}

// ProcessBlock stores a block and its related events in the lastgersync database
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	tx, err := db.NewTx(ctx, p.db)
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
	for _, e := range block.Events {
		event, ok := e.(Event)
		if !ok {
			return errors.New("failed to convert sync.Block.Event to Event")
		}
		if err = meddler.Insert(tx, "imported_global_exit_root", &eventWithBlockNum{
			GlobalExitRoot:  event.GlobalExitRoot,
			L1InfoTreeIndex: event.L1InfoTreeIndex,
			BlockNum:        block.Num,
		}); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	p.log.Debugf("processed %d events until block %d", len(block.Events), block.Num)
	return nil
}

// GetLastProcessedBlock retrieves the most recent block processed by the processor,
// including those without events.
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	var block BlockNum
	if err := meddler.QueryRow(
		p.db,
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

// GetLastIndex retrieves the highest L1InfoTreeIndex recorded in the imported_global_exit_root table
func (p *processor) getLastIndex() (uint32, error) {
	var lastIndex uint32
	row := p.db.QueryRow(`
		SELECT l1_info_tree_index 
		FROM imported_global_exit_root 
		ORDER BY l1_info_tree_index DESC LIMIT 1;
	`)
	err := row.Scan(&lastIndex)
	if err != nil {
		return 0, db.ReturnErrNotFound(err)
	}
	return lastIndex, nil
}

// Reorg removes all blocks and associated data starting from a specific block number from lastgersync database
func (p *processor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM block WHERE num >= $1;`, firstReorgedBlock)
	if err != nil {
		return fmt.Errorf("error processing reorg: %w", err)
	}
	return nil
}

// GetFirstGERAfterL1InfoTreeIndex returns the first GER injected into the chain that is associated with
// or greater than the specified l1InfoTreeIndex.
func (p *processor) GetFirstGERAfterL1InfoTreeIndex(
	ctx context.Context, l1InfoTreeIndex uint32,
) (Event, error) {
	var e Event
	err := meddler.QueryRow(p.db, &e, `
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
