package sync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	DefaultWaitPeriodBlockNotFound = time.Millisecond * 100
)

type EthClienter interface {
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	ChainID(ctx context.Context) (*big.Int, error)
}

type EVMDownloaderInterface interface {
	WaitForNewBlocks(ctx context.Context, lastBlockSeen uint64) (newLastBlock uint64)
	GetEventsByBlockRange(ctx context.Context, fromBlock, toBlock uint64) EVMBlocks
	GetLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log
	GetBlockHeader(ctx context.Context, blockNum uint64) (EVMBlockHeader, bool)
	GetLastFinalizedBlock(ctx context.Context) (*types.Header, error)
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
	finalizedBlockType         etherman.BlockNumberFinality
	stopDownloaderOnIterationN int
	adressessToQuery           []common.Address
}

func NewEVMDownloader(
	syncerID string,
	ethClient EthClienter,
	syncBlockChunkSize uint64,
	blockFinalityType etherman.BlockNumberFinality,
	waitForNewBlocksPeriod time.Duration,
	appender LogAppenderMap,
	adressessToQuery []common.Address,
	rh *RetryHandler,
	finalizedBlockType etherman.BlockNumberFinality,
) (*EVMDownloader, error) {
	logger := log.WithFields("syncer", syncerID)
	finality, err := blockFinalityType.ToBlockNum()
	if err != nil {
		return nil, err
	}

	fbtEthermanType := finalizedBlockType
	fbt, err := finalizedBlockType.ToBlockNum()
	if err != nil {
		return nil, err
	}

	if fbt.Cmp(finality) > 0 {
		// if someone configured the syncer to query blocks by Safe or Finalized block
		// finalized block type should be at least the same as the block finality
		fbt = finality
		fbtEthermanType = blockFinalityType
		logger.Warnf("finalized block type %s is greater than block finality %s, setting finalized block type to %s",
			finalizedBlockType, blockFinalityType, fbtEthermanType)
	}

	logger.Infof("downloader initialized with block finality: %s, finalized block type: %s. SyncChunkSize: %d",
		blockFinalityType, fbtEthermanType, syncBlockChunkSize)

	return &EVMDownloader{
		syncBlockChunkSize: syncBlockChunkSize,
		log:                logger,
		finalizedBlockType: fbtEthermanType,
		adressessToQuery:   adressessToQuery,
		EVMDownloaderInterface: NewEVMDownloaderImplementation(
			syncerID,
			ethClient,
			finality,
			waitForNewBlocksPeriod,
			appender,
			adressessToQuery,
			rh,
			fbt,
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
		Addresses: d.adressessToQuery,
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
		lastFinalizedBlockNumber := min(lastBlock, lastFinalizedBlock.Number.Uint64())

		requestToBlock := toBlock
		if toBlock >= lastBlock {
			requestToBlock = lastBlock
			reachTop = true
		}
		d.log.Debugf("getting events from blocks [%d to  %d] toBlock: %d. lastFinalizedBlock: %d lastBlock: %d",
			fromBlock, requestToBlock, toBlock, lastFinalizedBlockNumber, lastBlock)
		blocks := d.GetEventsByBlockRange(ctx, fromBlock, requestToBlock)
		d.log.Debugf("result events from blocks [%d to  %d] -> len(blocks)=%d",
			fromBlock, requestToBlock, len(blocks))
		if requestToBlock <= lastFinalizedBlockNumber {
			d.log.Debugf("range in safe zone: requestToBlock:%d <= finalized: %d",
				requestToBlock, lastFinalizedBlockNumber)
			d.reportBlocks(downloadedCh, blocks, lastFinalizedBlockNumber)
			if blocks.Len() == 0 || blocks[blocks.Len()-1].Num < requestToBlock {
				d.reportEmptyBlock(ctx, downloadedCh, requestToBlock, lastFinalizedBlockNumber)
			}
			fromBlock = requestToBlock + 1
			toBlock = fromBlock + d.syncBlockChunkSize
		} else {
			d.log.Debugf("range in not in safe zone: requestToBlock:%d <= finalized: %d",
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
		d.log.Infof("sending block %d to the driver (with events)", block.Num)
		block.IsFinalizedBlock = d.finalizedBlockType.IsFinalized() && block.Num <= lastFinalizedBlock
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
		IsFinalizedBlock: d.finalizedBlockType.IsFinalized() && header.Num <= lastFinalizedBlock,
		EVMBlockHeader:   header,
	}
}

type EVMDownloaderImplementation struct {
	ethClient              EthClienter
	blockFinality          *big.Int
	waitForNewBlocksPeriod time.Duration
	appender               LogAppenderMap
	topicsToQuery          []common.Hash
	addressesToQuery       []common.Address
	rh                     *RetryHandler
	log                    *log.Logger
	finalizedBlockType     *big.Int
}

func NewEVMDownloaderImplementation(
	syncerID string,
	ethClient EthClienter,
	blockFinality *big.Int,
	waitForNewBlocksPeriod time.Duration,
	appender LogAppenderMap,
	addressesToQuery []common.Address,
	rh *RetryHandler,
	finalizedBlockType *big.Int,
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
	}
}

func (d *EVMDownloaderImplementation) ChainID(ctx context.Context) (uint64, error) {
	chainID, err := d.ethClient.ChainID(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to retrieve chain id. Err: %w", err)
	}

	if chainID == nil {
		return 0, errors.New("chain id is undefined")
	}

	return chainID.Uint64(), nil
}

func (d *EVMDownloaderImplementation) GetLastFinalizedBlock(ctx context.Context) (*types.Header, error) {
	// if the finalized block type is nil, it means that the reorgs are not happening on the network
	if d.finalizedBlockType == nil {
		return d.ethClient.HeaderByNumber(ctx, d.blockFinality)
	}

	return d.ethClient.HeaderByNumber(ctx, d.finalizedBlockType)
}

func (d *EVMDownloaderImplementation) WaitForNewBlocks(
	ctx context.Context, lastBlockSeen uint64,
) (newLastBlock uint64) {
	attempts := 0
	ticker := time.NewTicker(d.waitForNewBlocksPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			d.log.Info("context cancelled")
			return lastBlockSeen
		case <-ticker.C:
			header, err := d.ethClient.HeaderByNumber(ctx, d.blockFinality)
			if err != nil {
				if ctx.Err() == nil {
					attempts++
					d.log.Error("error getting last block num from eth client: ", err)
					d.rh.Handle("waitForNewBlocks", attempts)
				} else {
					d.log.Warn("context has been canceled while trying to get header by number")
				}
				continue
			}
			if header.Number.Uint64() > lastBlockSeen {
				return header.Number.Uint64()
			}
		}
	}
}

func (d *EVMDownloaderImplementation) GetEventsByBlockRange(ctx context.Context, fromBlock, toBlock uint64) EVMBlocks {
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
							"for block %d: %s vs %s. Retrying.",
						l.BlockNumber, b.Hash, l.BlockHash,
					)
					return d.GetEventsByBlockRange(ctx, fromBlock, toBlock)
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

			for {
				attempts := 0
				err := d.appender[l.Topics[0]](latestBlock, l)
				if err != nil {
					attempts++
					d.log.Error("error trying to append log: ", err)
					d.rh.Handle("getLogs", attempts)
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

func (d *EVMDownloaderImplementation) GetLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log {
	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		Addresses: d.addressesToQuery,
		ToBlock:   new(big.Int).SetUint64(toBlock),
	}
	var (
		attempts       = 0
		unfilteredLogs []types.Log
		err            error
	)
	for {
		unfilteredLogs, err = d.ethClient.FilterLogs(ctx, query)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// context is canceled, we don't want to fatal on max attempts in this case
				return nil
			}

			attempts++
			d.log.Errorf("error calling FilterLogs to eth client: filter: %s err:%w ",
				filterQueryToString(query),
				err,
			)
			d.rh.Handle("getLogs", attempts)
			continue
		}
		break
	}
	logs := make([]types.Log, 0, len(unfilteredLogs))
	for _, l := range unfilteredLogs {
		if l.Removed {
			d.log.Warnf("log removed: %+v", l)
		}
		for _, topic := range d.topicsToQuery {
			if l.Topics[0] == topic && !l.Removed {
				logs = append(logs, l)
				break
			}
		}
	}
	return logs
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
			d.rh.Handle("getBlockHeader", attempts)
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
