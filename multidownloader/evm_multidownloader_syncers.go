package multidownloader

// This is the implementation of types.MultiDownloader used by syncers
import (
	"context"
	"fmt"
	"time"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
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
	if number == nil {
		number = &aggkittypes.LatestBlock
	}
	// Resolve blockNumber
	blockNumber, err := dh.blockNotifierManager.GetCurrentBlockNumber(ctx, *number)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: cannot get block number for finality=%s: %w",
			number.String(), err)
	}
	// Is this block in storage?
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
	// Get from ethClient
	blockHeader, err := dh.ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber))
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: ethClient.HeaderByNumber(%s) failed. Err: %w",
			number.String(), err)
	}
	return blockHeader, nil
}

// HeaderByNumber gets the block header for the given block number from storage or ethClient
func (dh *EVMMultidownloader) StorageHeaderByNumber(ctx context.Context,
	number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, mdrtypes.FinalizedType, error) {
	if number == nil {
		number = &aggkittypes.LatestBlock
	}
	// Resolve blockNumber
	blockNumber, err := dh.blockNotifierManager.GetCurrentBlockNumber(ctx, *number)
	if err != nil {
		return nil, false, fmt.Errorf("EVMMultidownloader.StorageHeaderByNumber: cannot get block number for finality=%s: %w",
			number.String(), err)
	}
	// Is this block in storage?
	block, finalized, err := dh.storage.GetBlockHeaderByNumber(nil, blockNumber)
	if err != nil {
		return nil, false, fmt.Errorf("EVMMultidownloader.StorageHeaderByNumber: cannot get BlockHeader number=%s: %w",
			number.String(), err)
	}
	return block, finalized, nil
}

// EthClient returns the underlying eth client
func (dh *EVMMultidownloader) EthClient() aggkittypes.BaseEthereumClienter {
	return dh.ethClient
}

func (dh *EVMMultidownloader) LogQuery(ctx context.Context,
	query mdrtypes.LogQuery) (mdrtypes.LogQueryResponse, error) {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	isAval, availQuery := dh.state.IsPartiallyAvailable(query)
	if !isAval {
		return mdrtypes.LogQueryResponse{},
			fmt.Errorf("EVMMultidownloader.LogQuery: logs not synced for query: %s",
				query.String())
	}
	finalizedBlockNumber, err := dh.GetFinalizedBlockNumber(ctx)
	if err != nil {
		return mdrtypes.LogQueryResponse{},
			fmt.Errorf("EVMMultidownloader.LogQuery: cannot get finalized block number: %w",
				err)
	}
	// Calculate UnsafeRange

	result, err := dh.storage.LogQuery(nil, *availQuery)
	if err != nil {
		// Calculate UnsafeRange
		_, unsafePendingBlockRange := result.ResponseRange.SplitByBlockNumber(finalizedBlockNumber)
		result.UnsafeRange = unsafePendingBlockRange
	}
	return result, err
}

func (dh *EVMMultidownloader) Finality() aggkittypes.BlockNumberFinality {
	return dh.cfg.BlockFinality
}
