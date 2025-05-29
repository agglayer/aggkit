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
	database *sql.DB
	log      *log.Logger
	compatibility.CompatibilityDataStorager[sync.RuntimeData]
}

func newProcessor(dbPath string, loggerPrefix string) (*processor, error) {
	err := migrations.RunMigrations(dbPath)
	if err != nil {
		return nil, err
	}
	database, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}
	logger := log.WithFields("lastger-syncer", loggerPrefix)
	return &processor{
		database: database,
		log:      logger,
		CompatibilityDataStorager: compatibility.NewKeyValueToCompatibilityStorage[sync.RuntimeData](
			db.NewKeyValueStorage(database),
			loggerPrefix,
		),
	}, nil
}

// GetLastProcessedBlock returns the last processed block by the processor, including blocks
// that don't have events
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	var lastProcessedBlock uint64

	row := p.database.QueryRow("SELECT num FROM BLOCK ORDER BY num DESC LIMIT 1;")
	err := row.Scan(&lastProcessedBlock)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return lastProcessedBlock, err
}

func (p *processor) getLastIndex() (uint32, error) {
	var lastIndex uint32
	row := p.database.QueryRow(`
		SELECT l1_info_tree_index 
		FROM imported_global_exit_root 
		ORDER BY l1_info_tree_index DESC LIMIT 1;
	`)
	err := row.Scan(&lastIndex)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return lastIndex, err
}

func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	tx, err := db.NewTx(ctx, p.database)
	if err != nil {
		return err
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			if errRollback := tx.Rollback(); errRollback != nil {
				log.Errorf("error while rolling back tx %v", errRollback)
			}
		}
	}()

	if _, err = tx.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, block.Num, block.Hash.String()); err != nil {
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

	if err = tx.Commit(); err != nil {
		return err
	}
	shouldRollback = false
	p.log.Debugf("processed %d events until block %d", len(block.Events), block.Num)
	return nil
}

func (p *processor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	_, err := p.database.Exec(`DELETE FROM block WHERE num >= $1;`, firstReorgedBlock)
	return fmt.Errorf("error processing reorg: %w", err)
}

// GetFirstGERAfterL1InfoTreeIndex returns the first GER injected on the chain that is related to l1InfoTreeIndex
// or greater
func (p *processor) GetFirstGERAfterL1InfoTreeIndex(
	ctx context.Context, l1InfoTreeIndex uint32,
) (Event, error) {
	e := Event{}
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
		return e, err
	}
	return e, nil
}
