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
//
// It is exactly a single-element batch of the shared lifecycle: it delegates to
// processBlocks, so the legacy one-block path keeps one transaction per call (the
// driver batch capability stays opt-in) while sharing the transaction, rollback,
// cancellation and post-commit logging logic with the batched path. This
// guarantees a single block and a batch of one are byte-for-byte identical.
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
	return p.processBlocks(ctx, []sync.Block{block})
}

// processBlocks stores an ordered batch of blocks and their claim-related events
// atomically in a SINGLE SQL transaction: either every block in the batch is
// committed, or the whole batch is rolled back. This is the OPTIONAL batch
// capability of the claimsync processor, wired by the syncer constructor through
// sync.EnableBlockBatching; the legacy one-block ProcessBlock delegates here.
//
// The batch must be ordered and bounded to sync.MaxBatchBlocks blocks (the driver
// guarantees both, and the limit is validated here too so the helper is safe to
// call directly). Cancellation is checked before every block and before the
// commit, so a cancelled batch is rolled back without any partial insert. The
// per-block semantics are exactly those of ProcessBlock, because both paths share
// processBlockInTx (block row, claim/unset_claim/set_claim handlers, full proof and
// metadata fields, and empty checkpoints included).
func (p *processor) processBlocks(ctx context.Context, blocks []sync.Block) error {
	if len(blocks) == 0 {
		return nil
	}
	if len(blocks) > sync.MaxBatchBlocks {
		return fmt.Errorf("claimsync processBlocks: batch of %d blocks exceeds the %d-block limit",
			len(blocks), sync.MaxBatchBlocks)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	tx, err := p.storage.NewTx(dbCtx)
	if err != nil {
		p.log.Errorf("failed to start transaction for batch of %d blocks (first %d): %v",
			len(blocks), blocks[0].Num, err)
		return err
	}
	shouldRollback := true
	defer func() {
		if shouldRollback {
			p.rollbackTx(tx)
		}
	}()

	for _, block := range blocks {
		if err := dbCtx.Err(); err != nil {
			return err
		}
		if err := p.processBlockInTx(dbCtx, tx, block); err != nil {
			// A cancellation arriving in the middle of a block makes the
			// database/sql layer roll the transaction back underneath us, so the
			// next statement of the same block fails with a spurious "transaction
			// has already been committed or rolled back" error instead of the
			// cancellation itself. Surface the context error in that case, so a
			// cancelled batch is always reported as the cancellation that caused
			// it (the whole batch is rolled back by the deferred rollback either
			// way, so no partial rows are visible).
			if ctxErr := dbCtx.Err(); ctxErr != nil {
				return ctxErr
			}
			return err
		}
	}

	if err := dbCtx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		p.log.Errorf("failed to commit batch of %d blocks (first %d): %v",
			len(blocks), blocks[0].Num, err)
		return err
	}
	shouldRollback = false
	for _, block := range blocks {
		p.log.Debugf("claimSyncer: successfully processed block %d with %d events", block.Num, len(block.Events))
	}
	return nil
}

// processBlockInTx inserts the block row and applies all of its claim-related
// events through the caller-provided transaction. It performs no commit or
// rollback: the caller owns the transaction lifecycle. It is the exact inner body
// shared by the legacy one-block ProcessBlock and the batch processBlocks, so a
// batched block keeps byte-for-byte the same per-block semantics (block row,
// claim/unset_claim/set_claim handlers, full proof/metadata fields, empty blocks).
func (p *processor) processBlockInTx(dbCtx context.Context, tx dbtypes.Txer, block sync.Block) error {
	if err := p.storage.InsertBlock(dbCtx, tx, block.Num, block.Hash); err != nil {
		p.log.Errorf("failed to insert block %d: %v", block.Num, err)
		return err
	}
	for _, e := range block.Events {
		result := p.embeddedProcessor.ProcessBlockWithTx(dbCtx, tx, block, e)
		if result != nil {
			return result
		}
	}
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

func (p *processor) withDatabaseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, p.dbQueryTimeout)
}

func (p *processor) rollbackTx(tx dbtypes.SQLTxer) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		p.log.Errorf("error rolling back tx: %v", err)
	}
}
