package multidownloader

import (
	"context"
	"fmt"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/ethereum/go-ethereum/common"
)

// CheckValidBlock checks if the given blockNumber and blockHash are still valid
// returns: isValid bool, reorgChainID uint64, err error
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
	chainID, found, err := dh.storage.GetBlockReorgedChainID(nil, blockNumber, blockHash)
	if err != nil {
		return true, 0, fmt.Errorf("EVMMultidownloader.CheckValidBlock: cannot check blocks_reorged for blockNumber=%d: %w",
			blockNumber, err)
	}
	if found {
		dh.log.Infof("EVMMultidownloader.CheckValidBlock: blockNumber=%d, blockHash=%s found in blocks_reorged (chainID=%d)",
			blockNumber, blockHash.Hex(), chainID)
		return false, chainID, nil
	}
	// Not found anywhere, consider invalid
	return false, 0, fmt.Errorf(
		"EVMMultidownloader.CheckValidBlock: blockNumber=%d, blockHash=%s not found in storage or blocks_reorged",
		blockNumber, blockHash.Hex())
}

func (dh *EVMMultidownloader) GetReorgedDataByChainID(ctx context.Context,
	reorgChainID uint64) (*mdrtypes.ReorgData, error) {
	return dh.storage.GetReorgedDataByChainID(nil, reorgChainID)
}
