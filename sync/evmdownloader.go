package sync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	DefaultWaitPeriodBlockNotFound = time.Millisecond * 100
	MaxRetryCountBlockHashMismatch = 5
	// DefaultFilterLogsTimeout is the default timeout for filter logs operations to prevent hanging
	DefaultFilterLogsTimeout = 2 * time.Minute
)

var (
	errChainIDUndefined = errors.New("chain id is undefined")
)

type EVMDownloaderInterface interface {
	WaitForNewBlocks(ctx context.Context, lastBlockSeen uint64) (newLastBlock uint64)
	GetEventsByBlockRange(ctx context.Context, fromBlock, toBlock uint64) EVMBlocks
	GetLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log
	GetBlockHeader(ctx context.Context, blockNum uint64) (EVMBlockHeader, bool)
	GetLastFinalizedBlock(ctx context.Context) (uint64, error)
	ChainID(ctx context.Context) (uint64, error)
}

type LogAppenderMap map[common.Hash]func(b *EVMBlock, l types.Log) error

// GetTopics returns the EVM event topics that are being queried
func (m LogAppenderMap) GetTopics() []common.Hash {
	topics := make([]common.Hash, 0, len(m))
	for topic := range m {
		topics = append(topics, topic)
	}
	return topics
}

type EVMDownloader struct {
	syncBlockChunkSize uint64
	EVMDownloaderInterface
	log                        *log.Logger
	finalizedBlockType         *aggkittypes.BlockNumberFinality
	stopDownloaderOnIterationN int
	addressesToQuery           []common.Address
	reorgDetector              ReorgDetector
	reorgDetectorID            string
}

func NewEVMDownloader(
	syncerID string,
	ethClient aggkittypes.BaseEthereumClienter,
	syncBlockChunkSize uint64,
	finality aggkittypes.BlockNumberFinality,
	waitForNewBlocksPeriod time.Duration,
	appender LogAppenderMap,
	addressesToQuery []common.Address,
	rh *RetryHandler,
	finalizedBlockType aggkittypes.BlockNumberFinality,
	reorgDetector ReorgDetector,
	reorgDetectorID string,
) (*EVMDownloader, error) {
	if finality.IsEmpty() {
		return nil, fmt.Errorf("block finality must be set")
	}

	logger := log.WithFields("syncer", syncerID)
	if finalizedBlockType.LessFinalThan(finality) {
		finalizedBlockType = finality
		logger.Warnf("finalized block type %s is less final than block finality %s, setting finalized block type to %s",
			finalizedBlockType.String(), finality.String(), finalizedBlockType.String())
	}

	logger.Infof("downloader initialized with block finality: %s, finalized block type: %s. SyncChunkSize: %d",
		finality.String(), finalizedBlockType.String(), syncBlockChunkSize)

	return &EVMDownloader{
		syncBlockChunkSize: syncBlockChunkSize,
		log:                logger,
		finalizedBlockType: &finalizedBlockType,
		addressesToQuery:   addressesToQuery,
		reorgDetector:      reorgDetector,
		reorgDetectorID:    reorgDetectorID,
		EVMDownloaderInterface: NewEVMDownloaderImplementation(
			syncerID,
			ethClient,
			finality,
			waitForNewBlocksPeriod,
			appender,
			addressesToQuery,
			rh,
			&finalizedBlockType,
			reorgDetector,
			reorgDetectorID,
		),
	}, nil
}

// setStopDownloaderOnIterationN sets the block number to stop the downloader (just for unittest)
func (d *EVMDownloader) setStopDownloaderOnIterationN(iteration int) {
	d.stopDownloaderOnIterationN = iteration
}

// RuntimeData returns the runtime data: chainID + addresses to query
func (d *EVMDownloader) RuntimeData(ctx context.Context) (RuntimeData, error) {
	chainID, err := d.ChainID(ctx)
	if err != nil {
		return RuntimeData{}, err
	}
	return RuntimeData{
		ChainID:   chainID,
		Addresses: d.addressesToQuery,
	}, nil
}

func (d *EVMDownloader) Download(ctx context.Context, fromBlock uint64, downloadedCh chan EVMBlock) {
	lastBlock := d.WaitForNewBlocks(ctx, 0)
	toBlock := fromBlock + d.syncBlockChunkSize
	iteration := 0
	reachTop := false
	for {
		select {
		case <-ctx.Done():
			d.log.Info("closing evm downloader channel")
			close(downloadedCh)
			return
		default:
		}
		d.log.Debugf("range: %d to %d, last block: %d", fromBlock, toBlock, lastBlock)

		if fromBlock > lastBlock || (reachTop && toBlock >= lastBlock) {
			d.log.Debugf(
				"waiting for new blocks, current range: [%d to %d], last block seen: %d",
				fromBlock, toBlock, lastBlock,
			)
			lastBlock = d.WaitForNewBlocks(ctx, lastBlock)
			d.log.Debugf("new last block seen: %d", lastBlock)

			if fromBlock-toBlock < d.syncBlockChunkSize {
				toBlock = fromBlock + d.syncBlockChunkSize
			}
		}
		reachTop = false
		lastFinalizedBlock, err := d.GetLastFinalizedBlock(ctx)
		if err != nil {
			d.log.Error("error getting last finalized block: ", err)
			continue
		}
		// lastFinalizedBlock can't be > lastBlock
		lastFinalizedBlockNumber := min(lastBlock, lastFinalizedBlock)

		requestToBlock := toBlock
		if toBlock >= lastBlock {
			requestToBlock = lastBlock
			reachTop = true
		}
		d.log.Debugf("getting events from blocks [%d to %d] toBlock: %d. lastFinalizedBlock: %d lastBlock: %d",
			fromBlock, requestToBlock, toBlock, lastFinalizedBlockNumber, lastBlock)
		blocks := d.GetEventsByBlockRange(ctx, fromBlock, requestToBlock)
		d.log.Debugf("result events from blocks [%d to %d] -> len(blocks)=%d",
			fromBlock, requestToBlock, len(blocks))
		if requestToBlock <= lastFinalizedBlockNumber {
			d.log.Debugf("range is in a safe zone (requestToBlock: %d <= finalized: %d)",
				requestToBlock, lastFinalizedBlockNumber)
			d.reportBlocks(downloadedCh, blocks, lastFinalizedBlockNumber)
			if blocks.Len() == 0 || blocks[blocks.Len()-1].Num < requestToBlock {
				d.reportEmptyBlock(ctx, downloadedCh, requestToBlock, lastFinalizedBlockNumber)
			}
			fromBlock = requestToBlock + 1
			toBlock = fromBlock + d.syncBlockChunkSize
		} else {
			d.log.Debugf("range is not in a safe zone (requestToBlock: %d > finalized: %d)",
				requestToBlock, lastFinalizedBlockNumber)
			if blocks.Len() == 0 {
				if lastFinalizedBlockNumber >= fromBlock {
					emptyBlock := lastFinalizedBlockNumber
					d.reportEmptyBlock(ctx, downloadedCh, emptyBlock, lastFinalizedBlockNumber)
					fromBlock = emptyBlock + 1
					toBlock = fromBlock + d.syncBlockChunkSize
				} else {
					// Extend range until find logs or reach the last finalized block
					toBlock += d.syncBlockChunkSize
				}
			} else {
				d.reportBlocks(downloadedCh, blocks, lastFinalizedBlockNumber)
				fromBlock = blocks[blocks.Len()-1].Num + 1
				toBlock = fromBlock + d.syncBlockChunkSize
			}
		}
		iteration++
		if d.stopDownloaderOnIterationN != 0 && iteration >= d.stopDownloaderOnIterationN {
			d.log.Infof("stop downloader on iteration %d", iteration)
			return
		}
	}
}

func (d *EVMDownloader) reportBlocks(downloadedCh chan EVMBlock, blocks EVMBlocks, lastFinalizedBlock uint64) {
	for _, block := range blocks {
		d.log.Debugf("sending block %d to the driver (with events)", block.Num)
		block.IsFinalizedBlock = block.Num <= lastFinalizedBlock
		downloadedCh <- *block
	}
}

func (d *EVMDownloader) reportEmptyBlock(ctx context.Context, downloadedCh chan EVMBlock,
	blockNum, lastFinalizedBlock uint64) {
	// Indicate the last downloaded block if there are not events on it
	d.log.Debugf("sending block %d to the driver (without events)", blockNum)
	header, isCanceled := d.GetBlockHeader(ctx, blockNum)
	if isCanceled {
		return
	}

	downloadedCh <- EVMBlock{
		IsFinalizedBlock: header.Num <= lastFinalizedBlock,
		EVMBlockHeader:   header,
	}
}

type EVMDownloaderImplementation struct {
	ethClient              aggkittypes.BaseEthereumClienter
	blockFinality          aggkittypes.BlockNumberFinality
	waitForNewBlocksPeriod time.Duration
	appender               LogAppenderMap
	topicsToQuery          []common.Hash
	addressesToQuery       []common.Address
	rh                     *RetryHandler
	log                    *log.Logger
	finalizedBlockType     *aggkittypes.BlockNumberFinality
	reorgDetector          ReorgDetector
	reorgDetectorID        string
}

// NewEVMDownloaderImplementation creates a new EVMDownloaderImplementation
// finalizedBlockType can be nil, in this case, it means that the reorgs are not happening on the network
func NewEVMDownloaderImplementation(
	syncerID string,
	ethClient aggkittypes.BaseEthereumClienter,
	blockFinality aggkittypes.BlockNumberFinality,
	waitForNewBlocksPeriod time.Duration,
	appender LogAppenderMap,
	addressesToQuery []common.Address,
	rh *RetryHandler,
	finalizedBlockType *aggkittypes.BlockNumberFinality,
	reorgDetector ReorgDetector,
	reorgDetectorID string,
) *EVMDownloaderImplementation {
	logger := log.WithFields("syncer", syncerID)
	var topics []common.Hash
	if appender != nil {
		topics = appender.GetTopics()
	}

	return &EVMDownloaderImplementation{
		ethClient:              ethClient,
		blockFinality:          blockFinality,
		waitForNewBlocksPeriod: waitForNewBlocksPeriod,
		appender:               appender,
		topicsToQuery:          topics,
		addressesToQuery:       addressesToQuery,
		rh:                     rh,
		log:                    logger,
		finalizedBlockType:     finalizedBlockType,
		reorgDetector:          reorgDetector,
		reorgDetectorID:        reorgDetectorID,
	}
}

func (d *EVMDownloaderImplementation) ChainID(ctx context.Context) (uint64, error) {
	chainID, err := d.ethClient.ChainID(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to retrieve chain id. Err: %w", err)
	}

	if chainID == nil {
		return 0, errChainIDUndefined
	}

	return chainID.Uint64(), nil
}

func (d *EVMDownloaderImplementation) GetLastFinalizedBlock(ctx context.Context) (uint64, error) {
	blockFinality := d.finalizedBlockType
	// if the finalized block type is nil, it means that the reorgs are not happening on the network
	if blockFinality == nil {
		blockFinality = &d.blockFinality
	}
	blockNumber, err := blockFinality.BlockNumber(ctx, d.ethClient)
	return blockNumber, err
}

func (d *EVMDownloaderImplementation) WaitForNewBlocks(
	ctx context.Context, latestSyncedBlock uint64) (newLatestBlock uint64) {
	attempts := 0
	ticker := time.NewTicker(d.waitForNewBlocksPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			d.log.Info("context cancelled")
			return latestSyncedBlock
		case <-ticker.C:
			blockHeader, err := d.blockFinality.BlockHeaderWithOffset(ctx, d.ethClient)
			if err != nil {
				if ctx.Err() == nil {
					attempts++
					d.log.Error("error getting last block num from eth client: ", err)
					d.rh.Handle(ctx, "WaitForNewBlocks", attempts)
				} else {
					d.log.Warn("context has been canceled while trying to get header by number")
				}
				continue
			}
			blockNumber := blockHeader.Number.Uint64()
			headerHash := blockHeader.Hash()
			if blockNumber > latestSyncedBlock {
				if d.reorgDetector != nil {
					if err := d.reorgDetector.AddBlockToTrack(ctx, d.reorgDetectorID, blockNumber, headerHash); err != nil {
						d.log.Errorf("Failed to notify reorg detector: %v", err)
					}
				}

				return blockNumber
			}
			// If blockNumber <= latestSyncedBlock, a reorg may have occurred
			// Get the block header to verify the hash and notify the reorg detector
			if blockNumber <= latestSyncedBlock && d.reorgDetector != nil {
				d.log.Debugf("Getting tracked block for block number %d and latest synced block %d", blockNumber, latestSyncedBlock)
				trackedBlock, err := d.reorgDetector.GetTrackedBlockByBlockNumber(d.reorgDetectorID, blockNumber)
				if err != nil {
					d.log.Errorf("Failed to get tracked block: %v, block number: %d", err, blockNumber)
					return latestSyncedBlock
				}

				if trackedBlock != nil && trackedBlock.Hash != headerHash {
					d.log.Warnf("Potential reorg detected: current block number %d (hash: %s) is different from "+
						"latest synced block %d (hash: %s)",
						blockNumber, headerHash.Hex(), latestSyncedBlock, trackedBlock.Hash.Hex())
					if err := d.reorgDetector.AddBlockToTrack(ctx, d.reorgDetectorID, blockNumber, headerHash); err != nil {
						d.log.Errorf("Failed to notify reorg detector: %v", err)
					}
					return blockNumber
				}
			}
		}
	}
}

func (d *EVMDownloaderImplementation) GetEventsByBlockRange(ctx context.Context, fromBlock, toBlock uint64) EVMBlocks {
	return d.getEventsByBlockRangeWithRetry(ctx, fromBlock, toBlock, 0)
}

func (d *EVMDownloaderImplementation) GetLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log {
	unfilteredLogs := d.getUnfilteredLogs(ctx, fromBlock, toBlock)
	return d.filterLogs(unfilteredLogs)
}

func (d *EVMDownloaderImplementation) getEventsByBlockRangeWithRetry(
	ctx context.Context,
	fromBlock, toBlock uint64, retryCount int,
) EVMBlocks {
	select {
	case <-ctx.Done():
		return nil
	default:
		logs := d.GetLogs(ctx, fromBlock, toBlock)
		blocks := make(EVMBlocks, 0, len(logs))
		var latestBlock *EVMBlock
		for _, l := range logs {
			if latestBlock == nil || latestBlock.Num < l.BlockNumber {
				b, canceled := d.GetBlockHeader(ctx, l.BlockNumber)
				if canceled {
					return nil
				}

				if b.Hash != l.BlockHash {
					d.log.Infof(
						"there has been a block hash change between the event query and the block query "+
							"for block %d: %s vs %s. Retrying attempt %d/%d.",
						l.BlockNumber, b.Hash, l.BlockHash, retryCount, MaxRetryCountBlockHashMismatch,
					)
					if retryCount >= MaxRetryCountBlockHashMismatch {
						// Log an error and return nil if the maximum retry count is reached.
						d.log.Errorf(
							"max retry attempts %d reached for block hash mismatch on block %d, returning nil",
							MaxRetryCountBlockHashMismatch, l.BlockNumber,
						)
						return nil
					}
					// Retry the operation with an incremented retry count.
					return d.getEventsByBlockRangeWithRetry(ctx, fromBlock, toBlock, retryCount+1)
				}
				latestBlock = &EVMBlock{
					EVMBlockHeader: EVMBlockHeader{
						Num:        l.BlockNumber,
						Hash:       l.BlockHash,
						Timestamp:  b.Timestamp,
						ParentHash: b.ParentHash,
					},
					Events: []interface{}{},
				}
				blocks = append(blocks, latestBlock)
			}

			appenderFn := d.appender[l.Topics[0]]
			attempts := 0
			for {
				err := appenderFn(latestBlock, l)
				if err != nil {
					attempts++
					d.log.Error("error trying to append log: ", err)
					d.rh.Handle(ctx, "appendLogs", attempts)
					continue
				}
				break
			}
		}

		return blocks
	}
}

func filterQueryToString(query ethereum.FilterQuery) string {
	return fmt.Sprintf("FromBlock: %s, ToBlock: %s, Addresses: %s, Topics: %s",
		query.FromBlock.String(), query.ToBlock.String(), query.Addresses, query.Topics)
}

func (d *EVMDownloaderImplementation) getUnfilteredLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log {
	initialBatchSize := toBlock - fromBlock + 1
	var (
		results   []types.Log
		batchSize = initialBatchSize
	)

	for start := fromBlock; start <= toBlock; {
		end := start + batchSize - 1
		if end > toBlock {
			end = toBlock
		}

		query := ethereum.FilterQuery{
			Addresses: d.addressesToQuery,
			FromBlock: new(big.Int).SetUint64(start),
			ToBlock:   new(big.Int).SetUint64(end),
		}

		var attempts int
		for {
			ctx, cancel := context.WithTimeout(ctx, DefaultFilterLogsTimeout)
			defer cancel()

			logs, err := d.ethClient.FilterLogs(ctx, query)
			if err == nil {
				results = append(results, logs...)
				break
			}

			if errors.Is(err, context.Canceled) {
				// context is canceled, we don't want to fatal on max attempts in this case
				d.log.Errorf("context is canceled getUnfilteredLogs, returning nil")
				return nil
			}

			if strings.Contains(err.Error(), "Query returned more than") {
				if batchSize == 1 {
					d.log.Errorf("too many logs even in single block %d", start)
					return nil
				}

				batchSize /= 2
				d.log.Warnf("too many logs in range [%d,%d], reducing batch size to %d", start, end, batchSize)
				end = start + batchSize - 1
				if end > toBlock {
					end = toBlock
				}
				// Update query with new range
				query.ToBlock = new(big.Int).SetUint64(end)
				continue
			}

			attempts++
			d.log.Errorf("error calling FilterLogs to eth client: filter: %s err:%w ",
				filterQueryToString(query),
				err,
			)
			d.rh.Handle(ctx, "getUnfilteredLogs", attempts)
		}
		start = end + 1
	}

	return results
}

func (d *EVMDownloaderImplementation) filterLogs(unfilteredLogs []types.Log) []types.Log {
	filteredLogs := make([]types.Log, 0, len(unfilteredLogs))
	for _, l := range unfilteredLogs {
		if l.Removed {
			d.log.Warnf("log removed: %+v", l)
			continue
		}
		if slices.Contains(d.topicsToQuery, l.Topics[0]) {
			filteredLogs = append(filteredLogs, l)
		}
	}
	return filteredLogs
}

func (d *EVMDownloaderImplementation) GetBlockHeader(ctx context.Context, blockNum uint64) (EVMBlockHeader, bool) {
	attempts := 0
	for {
		header, err := d.ethClient.HeaderByNumber(ctx, new(big.Int).SetUint64(blockNum))
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// context is canceled, we don't want to fatal on max attempts in this case
				return EVMBlockHeader{}, true
			}
			if errors.Is(err, ethereum.NotFound) {
				// block num can temporary disappear from the execution client due to a reorg,
				// in this case, we want to wait and not panic
				log.Warnf("block %d not found on the ethereum client: %v", blockNum, err)
				if d.rh.RetryAfterErrorPeriod != 0 {
					time.Sleep(d.rh.RetryAfterErrorPeriod)
				} else {
					time.Sleep(DefaultWaitPeriodBlockNotFound)
				}
				continue
			}

			attempts++
			d.log.Errorf("error getting block header for block %d, err: %v", blockNum, err)
			d.rh.Handle(ctx, "getBlockHeader", attempts)
			continue
		}
		return EVMBlockHeader{
			Num:        header.Number.Uint64(),
			Hash:       header.Hash(),
			ParentHash: header.ParentHash,
			Timestamp:  header.Time,
		}, false
	}
}
