package multidownloader

import (
	"context"
	"errors"
	"fmt"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	percentComplete = 100.0
)

var (
	ErrLogsNotAvailable = fmt.Errorf("logs not available")
)

type EVMDownloader struct {
	mdr      mdrsynctypes.MultidownloaderInterface
	logger   aggkitcommon.Logger
	rh       *sync.RetryHandler
	appender sync.LogAppenderMap
	// Maximum duration to wait to catch up the maximum request
	waitPeriodToCatchUpMaximumLogRange time.Duration
	pullingPeriod                      time.Duration
}

func NewEVMDownloader(
	mdr mdrsynctypes.MultidownloaderInterface,
	logger aggkitcommon.Logger,
	rh *sync.RetryHandler,
	appender sync.LogAppenderMap,
	waitPeriodToCatchUpMaximumLogRange time.Duration,
	pullingPeriod time.Duration,
) *EVMDownloader {
	return &EVMDownloader{
		mdr:                                mdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           appender,
		waitPeriodToCatchUpMaximumLogRange: waitPeriodToCatchUpMaximumLogRange,
		pullingPeriod:                      pullingPeriod,
	}
}

func (d *EVMDownloader) Finality() aggkittypes.BlockNumberFinality {
	return d.mdr.Finality()
}

func (d *EVMDownloader) DownloadNextBlocks(ctx context.Context,
	lastBlockHeader *aggkittypes.BlockHeader,
	maxBlocks uint64,
	syncerConfig aggkittypes.SyncerConfig) (*mdrsynctypes.DownloadResult, error) {
	// Check Context cancellation
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	err := d.checkReorgedBlock(ctx, lastBlockHeader)
	if err != nil {
		return nil, err
	}
	maxLogQuery := d.newMaxLogQuery(lastBlockHeader, maxBlocks, syncerConfig)
	var result *mdrsynctypes.DownloadResult
	conditionMet, err := aggkitcommon.PollingWithTimeout(ctx, d.pullingPeriod,
		d.waitPeriodToCatchUpMaximumLogRange, func() (bool, error) {
			var err error
			err = d.checkReorgedBlock(ctx, lastBlockHeader)
			if err != nil {
				return false, err
			}
			result, err = d.executeLogQuery(ctx, maxLogQuery)
			if err != nil {
				// The only allowed error is ErrLogsNotAvailable
				if errors.Is(err, ErrLogsNotAvailable) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		})
	if errors.Is(err, aggkitcommon.ErrTimeoutReached) {
		return nil, fmt.Errorf("EVMDownloader.DownloadNextBlocks: logs not available for query: %s after waiting %s: %w",
			maxLogQuery.String(), d.waitPeriodToCatchUpMaximumLogRange.String(), ErrLogsNotAvailable)
	}
	if err != nil {
		return nil, err
	}
	if !conditionMet {
		return nil, fmt.Errorf("EVMDownloader.DownloadNextBlocks: logs not available for query: %s. Err: %w",
			maxLogQuery.String(), ErrLogsNotAvailable)
	}

	// TODO: Add extra empty block is is in unsafe zone
	err = d.checkReorgedBlock(ctx, lastBlockHeader)
	if err != nil {
		return nil, err
	}
	if result == nil {
		d.logger.Debugf("EVMDownloader.DownloadNextBlocks: no logs found for blocks %s", maxLogQuery.BlockRange.String())
		result = &mdrsynctypes.DownloadResult{
			Data:            nil,
			PercentComplete: percentComplete,
		}
	}
	return result, nil
}

func (d *EVMDownloader) ChainID(ctx context.Context) (uint64, error) {
	return d.mdr.ChainID(ctx)
}

// executeLogQuery executes the log query, checking for partial availability
// if there are no logs available returns an error
func (d *EVMDownloader) executeLogQuery(ctx context.Context,
	fullLogQuery mdrtypes.LogQuery) (*mdrsynctypes.DownloadResult, error) {
	logQuery := fullLogQuery
	if !d.mdr.IsAvailable(fullLogQuery) {
		isPartial, partialLogQuery := d.mdr.IsPartiallyAvailable(fullLogQuery)
		if !isPartial {
			return nil, fmt.Errorf("DownloadNextBlocks: logs not available for query: %s. Err: %w", fullLogQuery.String(),
				ErrLogsNotAvailable)
		}
		logQuery = *partialLogQuery
	}

	logQueryResponse, err := d.mdr.LogQuery(ctx, logQuery)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.FilterLogs: cannot get logs: %w", err)
	}
	totalLogs := logQueryResponse.CountLogs()

	result := &mdrsynctypes.DownloadResult{
		Data:            d.logQueryResponseToEVMBlocks(ctx, logQueryResponse),
		PercentComplete: 0.0,
	}
	err = d.addLastBlockIfNotIncluded(ctx, result,
		logQueryResponse.ResponseRange, logQueryResponse.UnsafeRange)
	if err != nil {
		return nil, fmt.Errorf("EVMDownloader.executeLogQuery: adding last block: %w", err)
	}
	d.logger.Infof("EVMDownloader.executeLogQuery(block:%s): len(logs)= %d", logQuery.BlockRange.String(), totalLogs)
	return result, nil
}
func (d *EVMDownloader) addLastBlockIfNotIncluded(ctx context.Context,
	result *mdrsynctypes.DownloadResult,
	responseRange aggkitcommon.BlockRange,
	unsafeRange aggkitcommon.BlockRange) error {
	lastBlockNumber := responseRange.ToBlock
	// If it's already included, return
	for _, b := range result.Data {
		if b.Num == lastBlockNumber {
			return nil
		}
	}

	hdr, _, err := d.mdr.StorageHeaderByNumber(ctx, aggkittypes.NewBlockNumber(lastBlockNumber))
	if err != nil {
		d.logger.Errorf("EVMDownloader: error getting block header for block number %d: %v", lastBlockNumber, err)
		return nil
	}
	if hdr == nil {
		// Check that we are not in the unsafe zone. Because in that case we can't fake the Hash and it's an error
		// because the block must in in storage
		if unsafeRange.ContainsBlockNumber(lastBlockNumber) {
			err := fmt.Errorf("EVMDownloader: cannot get block header for block number %d in unsafe zone", lastBlockNumber)
			d.logger.Error(err)
			return err
		}
		hdr = &aggkittypes.BlockHeader{
			Number:     lastBlockNumber,
			Hash:       aggkitcommon.ZeroHash,
			Time:       0,
			ParentHash: nil,
		}
	}
	// Add empty block
	emptyBlock := &sync.EVMBlock{
		EVMBlockHeader: sync.EVMBlockHeader{
			Num:       lastBlockNumber,
			Hash:      hdr.Hash,
			Timestamp: hdr.Time,
		},
		Events: []interface{}{},
	}
	if hdr.ParentHash != nil {
		emptyBlock.ParentHash = *hdr.ParentHash
	}
	d.logger.Debugf("EVMDownloader.addLastBlockIfNotIncluded: to response %s adding empty block number %d / %s",
		responseRange.String(),
		lastBlockNumber, hdr.Hash.Hex())
	result.Data = append(result.Data, emptyBlock)
	return nil
}

func (d *EVMDownloader) logQueryResponseToEVMBlocks(
	ctx context.Context, response mdrtypes.LogQueryResponse) sync.EVMBlocks {
	blocks := make(sync.EVMBlocks, 0, len(response.Blocks))
	for _, blockWithLogs := range response.Blocks {
		evmBlock := &sync.EVMBlock{
			EVMBlockHeader: sync.EVMBlockHeader{
				Num:       blockWithLogs.Header.Number,
				Hash:      blockWithLogs.Header.Hash,
				Timestamp: blockWithLogs.Header.Time,
			},
			IsFinalizedBlock: blockWithLogs.IsFinal,
			Events:           []interface{}{},
		}
		if blockWithLogs.Header.ParentHash != nil {
			evmBlock.ParentHash = *blockWithLogs.Header.ParentHash
		}
		// Convert mdrtypes.Log to types.Log and append
		for _, mdrLog := range blockWithLogs.Logs {
			ethLog := types.Log{
				Address:        mdrLog.Address,
				Topics:         mdrLog.Topics,
				Data:           mdrLog.Data,
				BlockNumber:    mdrLog.BlockNumber,
				TxHash:         mdrLog.TxHash,
				TxIndex:        mdrLog.TxIndex,
				BlockHash:      blockWithLogs.Header.Hash,
				Index:          mdrLog.Index,
				Removed:        mdrLog.Removed,
				BlockTimestamp: mdrLog.BlockTimestamp,
			}
			d.appendLog(ctx, evmBlock, ethLog)
		}
		blocks = append(blocks, evmBlock)
	}
	return blocks
}

func (d *EVMDownloader) appendLog(ctx context.Context, block *sync.EVMBlock, log types.Log) {
	appenderFn := d.appender[log.Topics[0]]
	if appenderFn == nil {
		// d.logger.Debugf("no appender function found for topic: %s", log.Topics[0].Hex())
		return
	}
	attempts := 0
	for {
		err := appenderFn(block, log)
		if err != nil {
			attempts++
			d.logger.Errorf("error trying to append log (attempt %d): %v", attempts, err)
			d.rh.Handle(ctx, "appendLogs", attempts)
			continue
		}
		break
	}
}

// newMaxLogQuery creates a new LogQuery based on the syncerConfig and maxBlocks
func (d *EVMDownloader) newMaxLogQuery(lastBlockHeader *aggkittypes.BlockHeader,
	maxBlocks uint64,
	syncerConfig aggkittypes.SyncerConfig) mdrtypes.LogQuery {
	var fromBlock uint64
	if lastBlockHeader != nil {
		fromBlock = lastBlockHeader.Number + 1
	} else {
		fromBlock = syncerConfig.FromBlock
	}
	toBlock := fromBlock + maxBlocks - 1
	logQuery := mdrtypes.NewLogQuery(fromBlock, toBlock, syncerConfig.ContractAddresses)
	return logQuery
}

func (d *EVMDownloader) checkReorgedBlock(ctx context.Context,
	blockHeader *aggkittypes.BlockHeader) error {
	// Check Context cancellation
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// If blockHeader is nil, there's nothing to check
	// if hash== ZeroHash means that is a 'fake' block added to mark the end of the log range
	if blockHeader == nil || blockHeader.Hash == aggkitcommon.ZeroHash {
		return nil
	}
	// Check blockHeader is not reorged
	isValid, reorgChainID, err := d.mdr.CheckValidBlock(ctx, blockHeader.Number, blockHeader.Hash)
	if err != nil {
		return err
	}
	if !isValid {
		reorgData, err := d.mdr.GetReorgedDataByChainID(ctx, reorgChainID)
		if err != nil {
			return err
		}
		// TODO: if reorgData is nil?? can't happen
		if reorgData == nil {
			return fmt.Errorf("reorg data not found for chain ID %d", reorgChainID)
		}
		return mdrtypes.NewReorgedError(reorgData.BlockRangeAffected, reorgChainID,
			fmt.Sprintf("detected at block number %d", blockHeader.Number),
		)
	}
	return nil
}
