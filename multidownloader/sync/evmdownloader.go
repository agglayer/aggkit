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
	percentTotallyCompleted = 100.0
)

var (
	ErrLogsNotAvailable = fmt.Errorf("logs not available")
)

type EVMDownloader struct {
	multidownloader mdrsynctypes.MultidownloaderInterface
	logger          aggkitcommon.Logger
	rh              *sync.RetryHandler
	appender        sync.LogAppenderMap
	// Maximum duration to wait to catch up the maximum request
	waitPeriodToCatchUpMaximumLogRange time.Duration
	pullingPeriod                      time.Duration
	// Optional hooks. They must be set before the downloader is used (they are not thread-safe).
	// logsHook can modify the list of logs of a response before they are processed
	logsHook sync.LogsHookFunc
	// logsEnricher runs after logsHook and before the appenders build the events
	logsEnricher sync.LogsEnricherFunc
	// contextualAppender takes precedence over appender for the topics it contains
	contextualAppender sync.ContextualLogAppenderMap
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
		multidownloader:                    mdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           appender,
		waitPeriodToCatchUpMaximumLogRange: waitPeriodToCatchUpMaximumLogRange,
		pullingPeriod:                      pullingPeriod,
	}
}

// SetLogsHook sets an optional hook that is called with the logs of each response and can modify
// them before they are processed. It must be called before the downloader is used.
func (d *EVMDownloader) SetLogsHook(hook sync.LogsHookFunc) {
	d.logsHook = hook
}

// SetLogsEnricher sets an optional enricher that is invoked with the active download context after
// the logs hook and before the appenders build the events. It must be called before the downloader
// is used. If the enricher fails the whole download step fails and is retried by the driver.
func (d *EVMDownloader) SetLogsEnricher(enricher sync.LogsEnricherFunc) {
	d.logsEnricher = enricher
}

// SetContextualLogAppenders sets the optional context-aware appenders, keyed by event topic. A
// contextual appender takes precedence over the plain appender of the same topic and receives the
// active download context. It must be called before the downloader is used.
func (d *EVMDownloader) SetContextualLogAppenders(appenders sync.ContextualLogAppenderMap) {
	d.contextualAppender = appenders
}

func (d *EVMDownloader) Finality() aggkittypes.BlockNumberFinality {
	return d.multidownloader.Finality()
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
			result, err = d.executeLogQuery(ctx, maxLogQuery, syncerConfig)
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
		return nil, fmt.Errorf("Multidownloader_EVMDownloader.DownloadNextBlocks: "+
			"logs not available for query: %s after waiting %s: %w",
			maxLogQuery.String(), d.waitPeriodToCatchUpMaximumLogRange.String(), ErrLogsNotAvailable)
	}
	if err != nil {
		return nil, err
	}
	if !conditionMet {
		return nil, fmt.Errorf("Multidownloader_EVMDownloader.DownloadNextBlocks: "+
			"logs not available for query: %s. Err: %w",
			maxLogQuery.String(), ErrLogsNotAvailable)
	}
	if result == nil {
		return nil, fmt.Errorf("Multidownloader_EVMDownloader.DownloadNextBlocks: executeLogQuery "+
			"return result=nil. Range: %s", maxLogQuery.BlockRange.String())
	}
	// Before returning we check again that lastBlockHeader is not reorged
	err = d.checkReorgedBlock(ctx, lastBlockHeader)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (d *EVMDownloader) ChainID(ctx context.Context) (uint64, error) {
	return d.multidownloader.ChainID(ctx)
}

// executeLogQuery executes the log query, checking for partial availability
// if there are no logs available returns an error
func (d *EVMDownloader) executeLogQuery(ctx context.Context,
	fullLogQuery mdrtypes.LogQuery, syncerConfig aggkittypes.SyncerConfig) (*mdrsynctypes.DownloadResult, error) {
	logQuery := fullLogQuery
	if !d.multidownloader.IsAvailable(fullLogQuery) {
		isPartial, partialLogQuery := d.multidownloader.IsPartiallyAvailable(fullLogQuery)
		if !isPartial {
			return nil, fmt.Errorf("DownloadNextBlocks: logs not available for query: %s. Err: %w", fullLogQuery.String(),
				ErrLogsNotAvailable)
		}
		logQuery = *partialLogQuery
	}

	logQueryResponse, err := d.multidownloader.LogQuery(ctx, logQuery)
	if err != nil {
		return nil, fmt.Errorf("EVMMultidownloader.FilterLogs: cannot get logs: %w", err)
	}
	totalLogs := logQueryResponse.CountLogs()
	percentComplete, err := d.calculatePercentCompletation(ctx, syncerConfig, logQuery.BlockRange)
	if err != nil {
		d.logger.Warn(err.Error())
	}
	blocks, err := d.logQueryResponseToEVMBlocks(ctx, logQueryResponse)
	if err != nil {
		return nil, fmt.Errorf("Multidownloader_EVMDownloader.executeLogQuery: building blocks for range %s: %w",
			logQuery.BlockRange.String(), err)
	}
	result := &mdrsynctypes.DownloadResult{
		Data:                 blocks,
		CompletionPercentage: percentComplete,
	}
	err = d.addLastBlockIfNotIncluded(ctx, result,
		logQueryResponse.ResponseRange, logQueryResponse.UnsafeRange)
	if err != nil {
		return nil, fmt.Errorf("Multidownloader_EVMDownloader.executeLogQuery: adding last block: %w", err)
	}
	d.logger.Debugf("Multidownloader_EVMDownloader.executeLogQuery(block:%s): "+
		"len(logs)= %d", logQuery.BlockRange.String(), totalLogs)
	return result, nil
}

func (d *EVMDownloader) getFullBlockRange(ctx context.Context,
	syncerConfig aggkittypes.SyncerConfig) (*aggkitcommon.BlockRange, error) {
	blockTo, err := d.multidownloader.HeaderByNumber(ctx, &syncerConfig.ToBlock)
	if err != nil || blockTo == nil {
		return nil, fmt.Errorf("Multidownloader_EVMDownloader.getFullBlockRange: error getting 'to' block header: %w", err)
	}
	br := aggkitcommon.NewBlockRange(syncerConfig.FromBlock, blockTo.Number)
	return &br, nil
}

// Return the percent of completion of synchronization
func (d *EVMDownloader) calculatePercentCompletation(ctx context.Context,
	syncerConfig aggkittypes.SyncerConfig, lastRange aggkitcommon.BlockRange) (float64, error) {
	fullRange, err := d.getFullBlockRange(ctx, syncerConfig)
	if err != nil {
		return 0, fmt.Errorf("Multidownloader_EVMDownloader.calculatePercentCompletation: "+
			"error getting full block range: %w", err)
	}
	totalBlocks := fullRange.CountBlocks()
	pendingRange := aggkitcommon.NewBlockRange(lastRange.ToBlock+1, fullRange.ToBlock)
	if pendingRange.CountBlocks() == 0 {
		return percentTotallyCompleted, nil
	}
	blocksCompleted := totalBlocks - pendingRange.CountBlocks()
	percent := (float64(blocksCompleted) / float64(totalBlocks)) * percentTotallyCompleted
	return percent, nil
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

	hdr, _, err := d.multidownloader.StorageHeaderByNumber(ctx, aggkittypes.NewBlockNumber(lastBlockNumber))
	if err != nil {
		d.logger.Errorf("Multidownloader_EVMDownloader: error getting block header for block number %d: %v",
			lastBlockNumber, err)
		return nil
	}
	isFinalizedBlock := !unsafeRange.ContainsBlockNumber(lastBlockNumber)
	if hdr == nil {
		// Check that we are not in the unsafe zone. Because in that case we can't fake the Hash and it's an error
		// because the block must in in storage
		if !isFinalizedBlock {
			err := fmt.Errorf("Multidownloader_EVMDownloader: "+
				"cannot get block header for block number %d in unsafe zone", lastBlockNumber)
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
		IsFinalizedBlock: isFinalizedBlock,
		Events:           []interface{}{},
	}
	if hdr.ParentHash != nil {
		emptyBlock.ParentHash = *hdr.ParentHash
	}
	d.logger.Debugf("Multidownloader_EVMDownloader.addLastBlockIfNotIncluded: "+
		"to response %s adding empty block number %d / %s",
		responseRange.String(),
		lastBlockNumber, hdr.Hash.Hex())
	result.Data = append(result.Data, emptyBlock)
	return nil
}

func (d *EVMDownloader) logQueryResponseToEVMBlocks(
	ctx context.Context, response mdrtypes.LogQueryResponse) (sync.EVMBlocks, error) {
	logsPerBlock, err := d.responseLogsPerBlock(ctx, response)
	if err != nil {
		return nil, err
	}
	blocks := make(sync.EVMBlocks, 0, len(response.Blocks))
	for i, blockWithLogs := range response.Blocks {
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
		for _, ethLog := range logsPerBlock[i] {
			if err := d.appendLog(ctx, evmBlock, ethLog); err != nil {
				return nil, err
			}
		}
		blocks = append(blocks, evmBlock)
	}
	return blocks, nil
}

// responseLogsPerBlock converts the logs of the response to types.Log, grouped by block in the
// same order as response.Blocks, after running the optional logs hook and logs enricher over them.
func (d *EVMDownloader) responseLogsPerBlock(
	ctx context.Context, response mdrtypes.LogQueryResponse) ([][]types.Log, error) {
	logsPerBlock := make([][]types.Log, len(response.Blocks))
	totalLogs := 0
	for i, blockWithLogs := range response.Blocks {
		blockLogs := make([]types.Log, 0, len(blockWithLogs.Logs))
		// Convert mdrtypes.Log to types.Log
		for _, mdrLog := range blockWithLogs.Logs {
			blockLogs = append(blockLogs, types.Log{
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
			})
		}
		logsPerBlock[i] = blockLogs
		totalLogs += len(blockLogs)
	}
	if d.logsHook == nil && d.logsEnricher == nil {
		return logsPerBlock, nil
	}

	// The hooks work over the flat list of logs of the whole response
	allLogs := make([]types.Log, 0, totalLogs)
	for _, blockLogs := range logsPerBlock {
		allLogs = append(allLogs, blockLogs...)
	}
	if d.logsHook != nil {
		allLogs = d.logsHook(ctx, response.ResponseRange.FromBlock, response.ResponseRange.ToBlock, allLogs)
	}
	if d.logsEnricher != nil {
		if err := d.logsEnricher(ctx, allLogs); err != nil {
			return nil, fmt.Errorf("Multidownloader_EVMDownloader: error enriching logs: %w", err)
		}
	}
	// Group them again by block. The hook can drop logs but they keep their relative order
	blockIndex := make(map[uint64]int, len(response.Blocks))
	for i, blockWithLogs := range response.Blocks {
		blockIndex[blockWithLogs.Header.Number] = i
		logsPerBlock[i] = logsPerBlock[i][:0]
	}
	for _, ethLog := range allLogs {
		i, ok := blockIndex[ethLog.BlockNumber]
		if !ok {
			return nil, fmt.Errorf("Multidownloader_EVMDownloader: logs hook returned a log of block %d "+
				"that is not part of the response %s", ethLog.BlockNumber, response.ResponseRange.String())
		}
		logsPerBlock[i] = append(logsPerBlock[i], ethLog)
	}
	return logsPerBlock, nil
}

// appendLog builds the events of the log into the block through the appender of its topic.
// The contextual appender of the topic, if any, takes precedence over the plain one.
// Errors are retried, except:
//   - aggkittypes.ErrTransactionInclusionMismatch: the log is stale (its block was reorged away), so
//     retrying the same log can never succeed. It is returned for the caller to download the range again.
//   - a canceled context: its error is returned.
func (d *EVMDownloader) appendLog(ctx context.Context, block *sync.EVMBlock, log types.Log) error {
	// A log without topics cannot be attributed to an appender function
	if len(log.Topics) == 0 {
		d.logger.Warnf("skipping log without topics at block %d", log.BlockNumber)
		return nil
	}
	contextualAppenderFn := d.contextualAppender[log.Topics[0]]
	appenderFn := d.appender[log.Topics[0]]
	if contextualAppenderFn == nil && appenderFn == nil {
		// d.logger.Debugf("no appender function found for topic: %s", log.Topics[0].Hex())
		return nil
	}
	attempts := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var err error
		if contextualAppenderFn != nil {
			err = contextualAppenderFn(ctx, block, log)
		} else {
			err = appenderFn(block, log)
		}
		if err == nil {
			return nil
		}
		if errors.Is(err, aggkittypes.ErrTransactionInclusionMismatch) {
			return fmt.Errorf("Multidownloader_EVMDownloader: stale log at block %d (tx %s): %w",
				log.BlockNumber, log.TxHash.Hex(), err)
		}
		attempts++
		sync.LogAppendRetry(d.logger, attempts, err)
		d.rh.Handle(ctx, "appendLogs", attempts)
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
	isValid, reorgID, err := d.multidownloader.CheckValidBlock(ctx, blockHeader.Number, blockHeader.Hash)
	if err != nil {
		return err
	}
	if !isValid {
		reorgData, err := d.multidownloader.GetReorgedDataByReorgID(ctx, reorgID)
		if err != nil {
			return err
		}
		// TODO: if reorgData is nil?? can't happen
		if reorgData == nil {
			return fmt.Errorf("reorg data not found for reorg ID %d", reorgID)
		}
		return mdrtypes.NewReorgedError(reorgData.BlockRangeAffected, reorgID,
			fmt.Sprintf("detected at block number %d", blockHeader.Number),
		)
	}
	return nil
}
