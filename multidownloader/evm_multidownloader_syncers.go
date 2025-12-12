package multidownloader

// This is the implementation of types.MultiDownloader used by syncers
import (
	"context"
	"fmt"
	"math/big"
	"time"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

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
	dh.log.Debugf("EVMMultidownloader.FilterLogs: received query: %+v", query)
	defer dh.log.Debugf("EVMMultidownloader.FilterLogs: finished query: %+v", query)
	logQuery := mdrtypes.NewLogQueryFromEthereumFilter(query)
	for !dh.IsAvailable(logQuery) {
		dh.log.Infof("EVMMultidownloader.FilterLogs: waiting %s for logs to be available: %s",
			dh.cfg.WaitPeriodToCheckCatchUp.String(), logQuery.String())
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
	dh.log.Debugf("EVMMultidownloader.FilterLogs(%d - %d): len(logs)= %d", query.FromBlock, query.ToBlock, len(logs))

	return logs, nil
}

// HeaderByNumber gets the block header for the given block number from storage or ethClient
func (dh *EVMMultidownloader) HeaderByNumber(ctx context.Context, number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
	dh.log.Debugf("EVMMultidownloader.HeaderByNumber: received number: %s", number.String())
	defer dh.log.Debugf("EVMMultidownloader.HeaderByNumber: finished number: %s", number.String())
	if number.Cmp(big.NewInt(0)) < 0 {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: negative block numbers are not supported=%s",
			number.String())
	}

	block, _, err := dh.storage.GetBlockHeaderByNumber(nil, number.Uint64())
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: cannot get BlockHeader number=%d: %w",
			number.Uint64(), err)
	}
	if block != nil {
		return block, nil
	}
	// This is a fallback mechanism in case the block is not found in storage (it must be in storage!)
	dh.log.Debugf("EVMMultidownloader.HeaderByNumber: block number=%d not found in storage, fetching from ethClient",
		number.Uint64())
	blockHeader, err := dh.ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(number.Uint64()))
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.HeaderByNumber: ethClient.HeaderByNumber(%d) failed. Err: %w",
			number.Uint64(), err)
	}
	return blockHeader, nil
}

// EthClient returns the underlying eth client
func (dh *EVMMultidownloader) EthClient() aggkittypes.BaseEthereumClienter {
	return dh.ethClient
}
