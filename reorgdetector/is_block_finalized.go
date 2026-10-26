package reorgdetector

import (
	"context"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/db"
	"github.com/ethereum/go-ethereum/common"
)

// BlockTracker is the part of the ReorgDetector needed to know if a block is still tracked
type BlockTracker interface {
	GetTrackedBlockByBlockNumber(id string, blockNumber uint64) (*Header, error)
}

// StoredBlockHashFunc returns the hash the syncer stored for a block; found=false if not stored / no hash
type StoredBlockHashFunc func(ctx context.Context, blockNum uint64) (hash common.Hash, found bool, err error)

// IsBlockFinalized reports whether the block with the given number and hash, as read earlier from a syncer
// that is subscribed to the reorg detector with the given id, is finalized.
//
// The reorg detector tracks every block that was not final when the syncer processed it, and stops tracking
// it only once its hash was verified against the canonical chain and it is below the finalized block. So a
// processed block that is not tracked anymore is either finalized or was removed by a reorg.
//
// The order of the checks matters and must not be changed: the tracker is asked FIRST and the syncer's
// stored hash is read AFTERWARDS. On a reorg the reorg detector first notifies the syncer, which deletes the
// orphaned blocks from its DB, and only then stops tracking them. Reading the DB first would allow:
// (1) the DB still has the orphaned block, (2) the whole reorg completes, (3) the block is not tracked
// anymore => the orphaned block would be reported as finalized. With the tracker first, a block that is not
// tracked either was verified as final (the DB still has it, with the same hash) or its reorg completed
// (the DB doesn't have it, or has another block), so the DB read tells them apart. The hash given by the
// caller was read from the syncer earlier, so a block that was not final was already tracked by then (the
// driver tracks a block before processing it).
//
// Result:
//   - found=false when it can't be determined: no hash was given, the block tracked or stored is not the one
//     with the given hash, or it is not stored.
//   - found=true, finalized=false when the block is still tracked.
//   - found=true, finalized=true when the block is not tracked and the syncer stores it with the given hash.
//
// An unknown subscriber id is an error (ErrSubscriberNotFound), never "finalized".
func IsBlockFinalized(ctx context.Context, tracker BlockTracker, id string, blockNum uint64,
	blockHash common.Hash, storedHash StoredBlockHashFunc) (finalized, found bool, err error) {
	if blockHash == (common.Hash{}) {
		return false, false, nil
	}

	header, err := tracker.GetTrackedBlockByBlockNumber(id, blockNum)
	switch {
	case err == nil:
		if header == nil || header.Hash != blockHash {
			// the block the caller read is not the tracked one
			return false, false, nil
		}
		// still tracked: its hash was not confirmed against a finalized block yet
		return false, true, nil
	case errors.Is(err, ErrSubscriberNotFound):
		return false, false, fmt.Errorf("failed to check if block %d is tracked: %w", blockNum, err)
	case errors.Is(err, db.ErrNotFound):
		// not tracked anymore: finalized or reorged, the stored hash tells which
	default:
		return false, false, fmt.Errorf("failed to check if block %d is tracked: %w", blockNum, err)
	}

	stored, ok, err := storedHash(ctx, blockNum)
	if err != nil {
		return false, false, err
	}
	if !ok || stored != blockHash {
		return false, false, nil
	}
	return true, true, nil
}
