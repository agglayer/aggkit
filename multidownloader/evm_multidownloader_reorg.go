package multidownloader

import (
	"context"
	"fmt"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
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
	// Not found anywhere, consider invalid
	return false, 0, fmt.Errorf(
		"EVMMultidownloader.CheckValidBlock: blockNumber=%d, blockHash=%s not found in storage or blocks_reorged",
		blockNumber, blockHash.Hex())
}

func (dh *EVMMultidownloader) GetReorgedDataByReorgID(ctx context.Context,
	reorgID uint64) (*mdrtypes.ReorgData, error) {
	return dh.storage.GetReorgedDataByReorgID(nil, reorgID)
}
