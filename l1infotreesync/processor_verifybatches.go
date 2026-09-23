package l1infotreesync

import (
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	treeTypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

func (p *processor) processVerifyBatches(tx dbtypes.Txer, blockNumber uint64, event *VerifyBatches) error {
	if event == nil {
		return fmt.Errorf("processVerifyBatches: event is nil")
	}
	if tx == nil {
		return fmt.Errorf("processVerifyBatches: tx is nil, is mandatory to pass a tx")
	}
	log.Debugf("VerifyBatches: rollupExitTree.UpsertLeaf (blockNumber=%d, event=%s)", blockNumber, event.String())
	// If ExitRoot is zero, the leaf doesn't exist and doesn't change the root of the tree.
	//  	if leaf already exists doesn't make sense to 'empty' the leaf, so we keep previous value
	if event.ExitRoot == (common.Hash{}) {
		log.Infof("skipping VerifyBatches event with empty ExitRoot (blockNumber=%d, event=%s)", blockNumber, event.String())
		return nil
	}
	isNewLeaf, err := p.isNewValueForRollupExitTree(tx, event)
	if err != nil {
		return fmt.Errorf("isNewValueForrollupExitTree. err: %w", err)
	}
	if !isNewLeaf {
		log.Infof("skipping VerifyBatches event with same ExitRoot (blockNumber=%d, event=%s)", blockNumber, event.String())
		return nil
	}
	log.Infof("UpsertLeaf VerifyBatches event (blockNumber=%d, event=%s)", blockNumber, event.String())
	newRoot, err := p.rollupExitTree.PutLeaf(tx, blockNumber, event.BlockPosition,
		treeTypes.Leaf{
			Index: event.RollupID - 1,
			Hash:  event.ExitRoot,
		})
	if err != nil {
		return fmt.Errorf("error rollupExitTree.UpsertLeaf. err: %w", err)
	}
	verifyBatches := event
	verifyBatches.BlockNumber = blockNumber
	verifyBatches.RollupExitRoot = newRoot
	if err = meddler.Insert(tx, "verify_batches", verifyBatches); err != nil {
		return fmt.Errorf("error inserting verify_batches. err: %w", err)
	}
	return nil
}

func (p *processor) isNewValueForRollupExitTree(tx dbtypes.Querier, event *VerifyBatches) (bool, error) {
	currentRoot, err := p.rollupExitTree.GetLastRoot(tx)
	if err != nil && errors.Is(err, db.ErrNotFound) {
		// The tree is empty, so is a new value for sure
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("error rollupExitTree.GetLastRoot. err: %w", err)
	}
	leaf, err := p.rollupExitTree.GetLeaf(tx, event.RollupID-1, currentRoot.Hash)
	if err != nil && errors.Is(err, db.ErrNotFound) {
		// The leaf doesn't exist, so is a new value
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("error rollupExitTree.GetLeaf. err: %w", err)
	}
	return leaf != event.ExitRoot, nil
}

func (p *processor) GetLastVerifiedBatches(rollupID uint32) (*VerifyBatches, error) {
	verified := &VerifyBatches{}
	err := meddler.QueryRow(p.db, verified, `
		SELECT * FROM verify_batches
		WHERE rollup_id = $1
		ORDER BY block_num DESC, block_pos DESC
		LIMIT 1;
	`, rollupID)
	return verified, db.ReturnErrNotFound(err)
}

func (p *processor) GetFirstVerifiedBatches(rollupID uint32) (*VerifyBatches, error) {
	verified := &VerifyBatches{}
	err := meddler.QueryRow(p.db, verified, `
		SELECT * FROM verify_batches
		WHERE rollup_id = $1
		ORDER BY block_num ASC, block_pos ASC
		LIMIT 1;
	`, rollupID)
	return verified, db.ReturnErrNotFound(err)
}

func (p *processor) GetFirstVerifiedBatchesAfterBlock(rollupID uint32, blockNum uint64) (*VerifyBatches, error) {
	verified := &VerifyBatches{}
	err := meddler.QueryRow(p.db, verified, `
		SELECT * FROM verify_batches
		WHERE rollup_id = $1 AND block_num >= $2
		ORDER BY block_num ASC, block_pos ASC
		LIMIT 1;
	`, rollupID, blockNum)
	return verified, db.ReturnErrNotFound(err)
}

// GetVerifiedBatchesInBlockRange returns every verify_batches row whose block_num is in the
// inclusive range [fromBlock, toBlock], across all rollups (the rollup manager emits
// VerifyBatchesTrustedAggregator for both zkEVM and pessimistic verifications), ordered by
// block_num ASC, block_pos ASC. An empty range returns an empty slice and no error.
func (p *processor) GetVerifiedBatchesInBlockRange(fromBlock, toBlock uint64) ([]*VerifyBatches, error) {
	var verified []*VerifyBatches
	err := meddler.QueryAll(p.db, &verified, `
		SELECT * FROM verify_batches
		WHERE block_num >= $1 AND block_num <= $2
		ORDER BY block_num ASC, block_pos ASC;
	`, fromBlock, toBlock)
	if err != nil {
		return nil, err
	}
	return verified, nil
}

// GetVerifiedBatchesPaged returns a page of verify_batches rows for rollupID, most recent
// settlement first (block_num DESC, block_pos DESC), each enriched with its settlement block's
// hash (joined from the block table; nil if that block has no recorded hash). pageNumber is
// 1-based. Returns the page's rows and the total row count for rollupID (0, nil when there are
// none).
func (p *processor) GetVerifiedBatchesPaged(
	rollupID, pageNumber, pageSize uint32,
) ([]*VerifiedBatchWithBlockHash, int, error) {
	var count int
	if err := p.db.QueryRow(`
		SELECT COUNT(*) FROM verify_batches WHERE rollup_id = $1;
	`, rollupID).Scan(&count); err != nil {
		return nil, 0, fmt.Errorf("error counting verify_batches for rollup %d: %w", rollupID, err)
	}
	if count == 0 {
		return []*VerifiedBatchWithBlockHash{}, 0, nil
	}

	offset := (pageNumber - 1) * pageSize
	var verified []*VerifiedBatchWithBlockHash
	err := meddler.QueryAll(p.db, &verified, `
		SELECT vb.*, b.hash AS block_hash
		FROM verify_batches vb
		LEFT JOIN block b ON b.num = vb.block_num
		WHERE vb.rollup_id = $1
		ORDER BY vb.block_num DESC, vb.block_pos DESC
		LIMIT $2 OFFSET $3;
	`, rollupID, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("error querying verify_batches page for rollup %d: %w", rollupID, err)
	}
	return verified, count, nil
}
