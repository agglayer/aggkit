package multidownloader

// This is the implementation of types.MultiDownloader used by syncers
import (
	"context"
	"fmt"
	"time"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const debugSyncerInterface = false

// ChainID gets the chain ID directly from ethClient
func (dh *EVMMultidownloader) ChainID(ctx context.Context) (uint64, error) {
	chainID, err := dh.ethClient.ChainID(ctx)
	if err != nil {
		return 0, fmt.Errorf("EVMMultidownloader.ChainID: cannot get chainID: %w", err)
	}
	return chainID.Uint64(), nil
}

// BlockNumber gets the block number for the given finality type
func (dh *EVMMultidownloader) BlockNumber(ctx context.Context,
	finality aggkittypes.BlockNumberFinality) (uint64, error) {
	return dh.blockNotifierManager.GetCurrentBlockNumber(ctx, finality)
}

// BlockHeader gets the block header for the given finality type
func (dh *EVMMultidownloader) BlockHeader(ctx context.Context,
	finality aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
	number, err := dh.blockNotifierManager.GetCurrentBlockNumber(ctx, finality)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.BlockHeader: cannot get block number for finality=%s: %w",
			finality.String(), err)
	}
	header, err := dh.ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(number))
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.BlockHeader: cannot get header for block number=%d: %w",
			number, err)
	}
	return header, nil
}

// FilterLogs filters the logs. It gets them from storage or waits until they are available
func (dh *EVMMultidownloader) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	if !dh.IsInitialized() {
		return nil, fmt.Errorf("EVMMultidownloader.FilterLogs: multidownloader not initialized")
	}
	if debugSyncerInterface {
		dh.log.Debugf("EVMMultidownloader.FilterLogs: received query: %+v", query)
		defer dh.log.Debugf("EVMMultidownloader.FilterLogs: finished query: %+v", query)
	}
	logQuery := mdrtypes.NewLogQueryFromEthereumFilter(query)
	for !dh.IsAvailable(logQuery) {
		if debugSyncerInterface {
			dh.log.Debugf("EVMMultidownloader.FilterLogs: waiting %s for logs to be available: %s",
				dh.cfg.WaitPeriodToCheckCatchUp.String(), logQuery.String())
		}
		select {
		case <-time.After(dh.cfg.WaitPeriodToCheckCatchUp.Duration):
		case <-ctx.Done():
			return nil, fmt.Errorf("EVMMultidownloader.FilterLogs: "+
				"context done while waiting for logs %s to be available: %w",
				logQuery.String(), ctx.Err())
		}
	}
	logs, err := dh.storage.GetEthLogs(nil, logQuery)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.FilterLogs: cannot get logs: %w", err)
	}
	if debugSyncerInterface {
		dh.log.Debugf("EVMMultidownloader.FilterLogs(%d - %d): len(logs)= %d", query.FromBlock, query.ToBlock, len(logs))
	}
	return logs, nil
}

// HeaderByNumber gets the block header for the given block number from storage or ethClient
func (dh *EVMMultidownloader) HeaderByNumber(ctx context.Context,
	number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
	if debugSyncerInterface {
		dh.log.Debugf("EVMMultidownloader.HeaderByNumber: received number: %s", number.String())
		defer dh.log.Debugf("EVMMultidownloader.HeaderByNumber: finished number: %s", number.String())
	}
	if !number.IsConstant() {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: only numeric blockNumbers are supported (got=%s)",
			number.String())
	}
	blockNumber := number.Specific
	block, _, err := dh.storage.GetBlockHeaderByNumber(nil, blockNumber)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: cannot get BlockHeader number=%s: %w",
			number.String(), err)
	}
	if block != nil {
		return block, nil
	}
	if debugSyncerInterface {
		dh.log.Debugf("EVMMultidownloader.HeaderByNumber: block number=%s not found in storage, fetching from ethClient",
			number.String())
	}
	blockHeader, err := dh.ethClient.CustomHeaderByNumber(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: ethClient.HeaderByNumber(%s) failed. Err: %w",
			number.String(), err)
	}
	return blockHeader, nil
}

// EthClient returns the underlying eth client
func (dh *EVMMultidownloader) EthClient() aggkittypes.BaseEthereumClienter {
	return dh.ethClient
}

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
