package claimsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/sync"
)

type processor struct {
	storage        claimsynctypes.ClaimStorager
	log            aggkitcommon.Logger
	dbQueryTimeout time.Duration
	compatibility.CompatibilityDataStorager[sync.RuntimeData]
	embeddedProcessor claimsynctypes.EmbeddedProcessor
}

func newProcessor(
	logger aggkitcommon.Logger, storage claimsynctypes.ClaimStorager, dbQueryTimeout time.Duration,
) *processor {
	return &processor{
		storage:                   storage,
		log:                       logger,
		dbQueryTimeout:            dbQueryTimeout,
		CompatibilityDataStorager: storage,
		embeddedProcessor:         newEmbeddedProcessor(logger, storage),
	}
}

// ProcessBlock stores the block and its claim-related events atomically.
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	tx, err := p.storage.NewTx(dbCtx)
	if err != nil {
		p.log.Errorf("failed to start transaction for block %d: %v", block.Num, err)
		return err
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			p.rollbackTx(tx)
		}
	}()

	if err := p.storage.InsertBlock(ctx, tx, block.Num, block.Hash); err != nil {
		p.log.Errorf("failed to insert block %d: %v", block.Num, err)
		return err
	}
	for _, e := range block.Events {
		result := p.embeddedProcessor.ProcessBlockWithTx(dbCtx, tx, block, e)
		if result != nil {
			return result
		}
	}
	if err := tx.Commit(); err != nil {
		p.log.Errorf("failed to commit block %d: %v", block.Num, err)
		return err
	}
	shouldRollback = false
	p.log.Debugf("claimSyncer: successfully processed block %d with %d events", block.Num, len(block.Events))
	return nil
}

// Reorg deletes all blocks >= firstReorgedBlock (cascade-deletes claims, unset_claims, set_claims via FK).
func (p *processor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	p.log.Infof("reorg detected at block %d", firstReorgedBlock)

	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	tx, err := p.storage.NewTx(dbCtx)
	if err != nil {
		return fmt.Errorf("claimsync Reorg: start tx: %w", err)
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			p.rollbackTx(tx)
		}
	}()

	rowsAffected, err := p.embeddedProcessor.ReorgWithTx(dbCtx, tx, firstReorgedBlock)
	if err != nil {
		return fmt.Errorf("claimsync Reorg: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("claimsync Reorg: commit: %w", err)
	}
	shouldRollback = false

	p.log.Infof("reorged to block %d, %d rows deleted", firstReorgedBlock, rowsAffected)
	return nil
}

// GetFirstProcessedBlock returns the lowest block number stored.
func (p *processor) GetFirstProcessedBlock(ctx context.Context) (uint64, bool, error) {
	return p.storage.GetFirstProcessedBlock(ctx, nil)
}

// GetLastProcessedBlock returns the highest block number stored.
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, bool, error) {
	return p.storage.GetLastProcessedBlock(ctx, nil)
}

// GetBoundaryBlockForClaimType returns the max block_num for claims of the given type.
func (p *processor) GetBoundaryBlockForClaimType(
	ctx context.Context, tx dbtypes.Querier, claimType ClaimType,
) (uint64, error) {
	return p.storage.GetBoundaryBlockForClaimType(ctx, tx, claimType)
}

func (p *processor) withDatabaseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, p.dbQueryTimeout)
}

func (p *processor) rollbackTx(tx dbtypes.SQLTxer) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		p.log.Errorf("error rolling back tx: %v", err)
	}
}
