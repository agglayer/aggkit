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
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	DefaultWaitPeriodBlockNotFound = time.Millisecond * 100
)

type EthClienter interface {
	ethereum.LogFilterer
	ethereum.BlockNumberReader
	ethereum.ChainReader
	bind.ContractBackend
}

type EVMDownloaderInterface interface {
	WaitForNewBlocks(ctx context.Context, lastBlockSeen uint64) (newLastBlock uint64)
	GetEventsByBlockRange(ctx context.Context, fromBlock, toBlock uint64) EVMBlocks
	GetLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log
	GetBlockHeader(ctx context.Context, blockNum uint64) (EVMBlockHeader, bool)
	GetLastFinalizedBlock(ctx context.Context) (*types.Header, error)
}

type LogAppenderMap map[common.Hash]func(b *EVMBlock, l types.Log) error

type EVMDownloader struct {
	syncBlockChunkSize uint64
	EVMDownloaderInterface
	log                        *log.Logger
	finalizedBlockType         etherman.BlockNumberFinality
	stopDownloaderOnIterationN int
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

	topicsToQuery := make([]common.Hash, 0, len(appender))
	for topic := range appender {
		topicsToQuery = append(topicsToQuery, topic)
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
		EVMDownloaderInterface: &EVMDownloaderImplementation{
			ethClient:              ethClient,
			blockFinality:          finality,
			waitForNewBlocksPeriod: waitForNewBlocksPeriod,
			appender:               appender,
			topicsToQuery:          topicsToQuery,
			adressessToQuery:       adressessToQuery,
			rh:                     rh,
			log:                    logger,
			finalizedBlockType:     fbt,
		},
	}, nil
}

// setStopDownloaderOnIterationN sets the block number to stop the downloader (just for unittest)
func (d *EVMDownloader) setStopDownloaderOnIterationN(iteration int) {
	d.stopDownloaderOnIterationN = iteration
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
		if toBlock <= lastFinalizedBlockNumber {
			d.reportBlocks(downloadedCh, blocks, lastFinalizedBlockNumber)
			if blocks.Len() == 0 || blocks[blocks.Len()-1].Num < toBlock {
				d.reportEmptyBlock(ctx, downloadedCh, toBlock, lastFinalizedBlockNumber)
			}
			fromBlock = toBlock + 1
			toBlock = fromBlock + d.syncBlockChunkSize
		} else {
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
	adressessToQuery       []common.Address
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
	topicsToQuery []common.Hash,
	adressessToQuery []common.Address,
	rh *RetryHandler,
) *EVMDownloaderImplementation {
	logger := log.WithFields("syncer", syncerID)
	return &EVMDownloaderImplementation{
		ethClient:              ethClient,
		blockFinality:          blockFinality,
		waitForNewBlocksPeriod: waitForNewBlocksPeriod,
		appender:               appender,
		topicsToQuery:          topicsToQuery,
		adressessToQuery:       adressessToQuery,
		rh:                     rh,
		log:                    logger,
	}
}

func (d *EVMDownloaderImplementation) GetLastFinalizedBlock(ctx context.Context) (*types.Header, error) {
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
		blocks := EVMBlocks{}
		logs := d.GetLogs(ctx, fromBlock, toBlock)
		for _, l := range logs {
			if len(blocks) == 0 || blocks[len(blocks)-1].Num < l.BlockNumber {
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
				blocks = append(blocks, &EVMBlock{
					EVMBlockHeader: EVMBlockHeader{
						Num:        l.BlockNumber,
						Hash:       l.BlockHash,
						Timestamp:  b.Timestamp,
						ParentHash: b.ParentHash,
					},
					Events: []interface{}{},
				})
			}

			for {
				attempts := 0
				err := d.appender[l.Topics[0]](blocks[len(blocks)-1], l)
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
		Addresses: d.adressessToQuery,
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
