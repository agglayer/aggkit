package multidownloader

import (
	"context"
	"fmt"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// CheckValidBlock checks if the given blockNumber and blockHash are still valid
// returns: isValid bool, reorgID uint64, err error
func (dh *EVMMultidownloader) CheckValidBlock(ctx context.Context, blockNumber uint64,
	blockHash common.Hash) (bool, uint64, error) {
	// Check if is stored as valid block
	storedBlock, _, err := dh.storage.GetBlockHeaderByNumber(nil, blockNumber)
	if err != nil {
		return true, 0, fmt.Errorf("EVMMultidownloader.CheckValidBlock: cannot get BlockHeader number=%d: %w",
			blockNumber, err)
	}
	if storedBlock != nil {
		// Is valid?
		if storedBlock.Hash == blockHash {
			return true, 0, nil
		}
	}
	// From this point is invalid or unknown
	// Check in blocks_reorged
	reorgID, found, err := dh.storage.GetBlockReorgedReorgID(nil, blockNumber, blockHash)
	if err != nil {
		return true, 0, fmt.Errorf("EVMMultidownloader.CheckValidBlock: cannot check blocks_reorged for blockNumber=%d: %w",
			blockNumber, err)
	}
	if found {
		dh.log.Infof("EVMMultidownloader.CheckValidBlock: blockNumber=%d, blockHash=%s found in blocks_reorged (reorgID=%d)",
			blockNumber, blockHash.Hex(), reorgID)
		return false, reorgID, nil
	}
	// The block is neither stored nor recorded as reorged. This is the expected situation
	// after upgrading from the legacy syncer (issue #1638): the processor reports a checkpoint
	// block that the legacy syncer downloaded and that this multidownloader storage never
	// contained. Before treating it as an inconsistency, check against L1: if the block is at or
	// below the finalized block it should be a stable, immutable block, so we can ask the RPC for
	// the canonical block at that height and compare hashes.
	return dh.checkValidBlockAgainstL1(ctx, blockNumber, blockHash)
}

// checkValidBlockAgainstL1 is the fallback for a block that is not present in the local storage
// (neither in `blocks` nor in `blocks_reorged`). If the block is at or below the finalized block,
// it queries L1 for the canonical block at that height and compares the hashes:
//   - hashes match  -> the block is canonical, it just was never downloaded by this multidownloader
//     (e.g. a legacy checkpoint after an upgrade, or a pruned block) -> valid.
//   - hashes differ -> the requested block is on an orphaned branch of a finalized height,
//     which is a severe inconsistency that requires manual intervention -> error.
//
// If the block is above the finalized block it is not stable yet, so we keep treating it as an
// inconsistency and let the caller retry.
func (dh *EVMMultidownloader) checkValidBlockAgainstL1(ctx context.Context, blockNumber uint64,
	blockHash common.Hash) (bool, uint64, error) {
	finalizedBlockNumber, err := dh.GetFinalizedBlockNumber(ctx)
	if err != nil {
		return true, 0, fmt.Errorf(
			"EVMMultidownloader.CheckValidBlock: cannot get finalized block number for blockNumber=%d: %w",
			blockNumber, err)
	}
	if blockNumber > finalizedBlockNumber {
		// Not stable yet, can't safely validate against L1 by hash.
		return false, 0, fmt.Errorf(
			"EVMMultidownloader.CheckValidBlock: blockNumber=%d, blockHash=%s not found in storage or blocks_reorged "+
				"(block is above finalized block %d)",
			blockNumber, blockHash.Hex(), finalizedBlockNumber)
	}

	canonicalHeader, err := dh.ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber))
	if err != nil {
		return true, 0, fmt.Errorf(
			"EVMMultidownloader.CheckValidBlock: cannot get canonical header from L1 for blockNumber=%d: %w",
			blockNumber, err)
	}
	if canonicalHeader == nil {
		return true, 0, fmt.Errorf(
			"EVMMultidownloader.CheckValidBlock: got nil canonical header from L1 for blockNumber=%d", blockNumber)
	}

	if canonicalHeader.Hash == blockHash {
		dh.log.Infof("EVMMultidownloader.CheckValidBlock: blockNumber=%d, blockHash=%s not in storage but matches the "+
			"canonical finalized block on L1; treating as valid (likely a legacy checkpoint after upgrade)",
			blockNumber, blockHash.Hex())
		return true, 0, nil
	}

	// The hash does not match the canonical finalized block: the requested block is on an orphaned
	// branch of a finalized height. The multidownloader never observed this reorg, so it cannot
	// produce reorg data; surface it as an error requiring manual intervention.
	return false, 0, fmt.Errorf(
		"EVMMultidownloader.CheckValidBlock: blockNumber=%d, blockHash=%s does not match the canonical finalized "+
			"block on L1 (canonical hash=%s); inconsistent state requires manual intervention",
		blockNumber, blockHash.Hex(), canonicalHeader.Hash.Hex())
}

func (dh *EVMMultidownloader) GetReorgedDataByReorgID(ctx context.Context,
	reorgID uint64) (*mdrtypes.ReorgData, error) {
	return dh.storage.GetReorgedDataByReorgID(nil, reorgID)
}
