package sync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sort"
	"strings"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
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

// LogsHookFunc is a function that can modify the list of logs fetched before they are processed.
// It receives the block range and the original logs, and returns the (possibly modified) list.
type LogsHookFunc func(ctx context.Context, fromBlock, toBlock uint64, logs []types.Log) []types.Log

// LogsEnricherFunc is an optional hook that runs after the logs hook and before the appender
// builds the events for each block. It allows callers to attach contextual information to the
// fetched logs (for example resolving the transaction that produced each log) using the active
// download context.
//
// Ordinary retryable errors are retried by the downloader using its RetryHandler and the download
// context, so callers must not use it for conditions that will never succeed on retry. Returning an
// error wrapping aggkittypes.ErrTransactionInclusionMismatch signals that the logs are stale
// (typically because their block was reorged away): the downloader then drops the stale logs and
// refetches the whole block range instead of retrying the same stale log.
type LogsEnricherFunc func(ctx context.Context, logs []types.Log) error

// GetTopics returns the EVM event topics that are being queried
func (m LogAppenderMap) GetTopics() []common.Hash {
	topics := make([]common.Hash, 0, len(m))
	for topic := range m {
		topics = append(topics, topic)
	}
	// Sort topics to ensure deterministic output
	sort.Slice(topics, func(i, j int) bool {
		return topics[i].Hex() < topics[j].Hex()
	})
	return topics
}

type EVMDownloader struct {
	EVMDownloaderInterface
	syncBlockChunkSize          uint64
	log                         *log.Logger
	addressesToQuery            []common.Address
	finality                    aggkittypes.BlockNumberFinality
	stopDownloaderOnIterationN  int
	stopOnFinalizedBlockReached bool
}

func NewEVMDownloader(
	syncerID string,
	ethClient aggkittypes.MultiDownloaderLegacy,
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
	lessFinal, err := finalizedBlockType.LessFinalThan(finality)
	if err != nil {
		return nil, fmt.Errorf("error comparing finalized block type and block finality: %w", err)
	}
	if lessFinal {
		logger.Warnf("finalized block type: %s is less final than block finality: %s, overriding finalized block type to %s",
			finalizedBlockType.String(), finality.String(), finality.String())
		finalizedBlockType = finality
	}

	logger.Infof("downloader initialized with block finality: %s, finalized block type: %s. SyncChunkSize: %d",
		finality.String(), finalizedBlockType.String(), syncBlockChunkSize)

	return &EVMDownloader{
		syncBlockChunkSize: syncBlockChunkSize,
		log:                logger,
		addressesToQuery:   addressesToQuery,
		finality:           finality,
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

// Finality returns the block finality of the downloader
func (d *EVMDownloader) Finality() aggkittypes.BlockNumberFinality {
	return d.finality
}

// setStopDownloaderOnIterationN sets the block number to stop the downloader (just for unittest)
func (d *EVMDownloader) setStopDownloaderOnIterationN(iteration int) {
	d.stopDownloaderOnIterationN = iteration
}

func (d *EVMDownloader) SetStopOnFinalizedBlockReachedFlag() {
	d.log.Info("setting stop on finalized block reached flag")
	d.stopOnFinalizedBlockReached = true
}

// SetLogsHook sets an optional hook on the underlying EVMDownloaderImplementation.
// It returns the previous hook, if any. Returns nil if the underlying implementation does not support it.
func (d *EVMDownloader) SetLogsHook(hook LogsHookFunc) LogsHookFunc {
	if impl, ok := d.EVMDownloaderInterface.(*EVMDownloaderImplementation); ok {
		return impl.SetLogsHook(hook)
	}
	return nil
}

// SetLogsEnricher sets an optional contextual log enricher on the underlying
// EVMDownloaderImplementation. It returns the previous enricher, if any. Returns nil if the
// underlying implementation does not support it.
func (d *EVMDownloader) SetLogsEnricher(enricher LogsEnricherFunc) LogsEnricherFunc {
	if impl, ok := d.EVMDownloaderInterface.(*EVMDownloaderImplementation); ok {
		return impl.SetLogsEnricher(enricher)
	}
	return nil
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

func (d *EVMDownloader) Download(
	ctx context.Context, fromBlock uint64, downloadedCh chan EVMBlock,
	lastBlockNum *uint64, includeEmptyFirstBlock bool,
) {
	timeTracker := aggkitcommon.NewTimeTracker()
	timeTracker.Start()
	defer func() {
		timeTracker.Stop()
		d.log.Infof("EVMDownloader.Download finished in %s", timeTracker.String())
	}()
	lastBlock := d.WaitForNewBlocks(ctx, 0)
	initialFromBlock := fromBlock
	// initialBlockSent tracks whether fromBlock has been sent on the channel.
	// When includeEmptyFirstBlock is false we treat it as already sent (no special handling needed).
	initialBlockSent := !includeEmptyFirstBlock
	toBlock := fromBlock + d.syncBlockChunkSize
	if lastBlockNum != nil {
		toBlock = min(toBlock, *lastBlockNum)
	}
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
				if lastBlockNum != nil {
					toBlock = min(toBlock, *lastBlockNum)
				}
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
		if lastBlockNum != nil && requestToBlock > *lastBlockNum {
			requestToBlock = *lastBlockNum
		}
		d.log.Debugf("getting events from blocks [%d to %d] toBlock: %d. lastFinalizedBlock: %d lastBlock: %d",
			fromBlock, requestToBlock, toBlock, lastFinalizedBlockNumber, lastBlock)
		blocks := d.GetEventsByBlockRange(ctx, fromBlock, requestToBlock)
		d.log.Debugf("result events from blocks [%d to %d] -> len(blocks)=%d",
			fromBlock, requestToBlock, len(blocks))

		// If the download context was canceled while fetching/enriching this range, stop
		// before reporting any empty block or advancing progress: an empty report here would
		// otherwise be mistaken for real sync progress.
		if ctx.Err() != nil {
			d.log.Info("context canceled after getting events, closing evm downloader channel")
			close(downloadedCh)
			return
		}

		// Force-report initialFromBlock if not yet sent (even when it has no events).
		// preReportedInitial tracks whether we sent it as empty this iteration,
		// so we can avoid double-reporting it in the normal reporting paths below.
		preReportedInitial := false
		if !initialBlockSent && requestToBlock >= initialFromBlock {
			initialInBlocks := false
			for _, b := range blocks {
				if b.Num == initialFromBlock {
					initialInBlocks = true
					break
				}
			}
			if !initialInBlocks {
				if !d.reportEmptyBlock(ctx, downloadedCh, initialFromBlock, initialFromBlock, lastFinalizedBlockNumber) {
					// The first block is required as starting point: if it can't be reported (canceled or missing
					// on the node) there is nothing to do, closing the channel is the error signal for the caller.
					d.log.Errorf("cannot report the initial block %d (canceled or missing on the node), closing channel",
						initialFromBlock)
					close(downloadedCh)
					return
				}
				preReportedInitial = true
			}
			initialBlockSent = true
		}

		if requestToBlock <= lastFinalizedBlockNumber {
			d.log.Debugf("range is in a safe zone (requestToBlock: %d <= finalized: %d)",
				requestToBlock, lastFinalizedBlockNumber)
			if !d.reportBlocks(ctx, downloadedCh, blocks, lastFinalizedBlockNumber) {
				close(downloadedCh)
				return
			}
			skipEmpty := preReportedInitial && requestToBlock == initialFromBlock
			if !skipEmpty && (blocks.Len() == 0 || blocks[blocks.Len()-1].Num < requestToBlock) {
				// The blocks with events were already reported: a missing block must not resolve to one of them
				minBlock := fromBlock
				if blocks.Len() > 0 {
					minBlock = max(fromBlock, blocks[blocks.Len()-1].Num+1)
				}
				d.reportEmptyBlock(ctx, downloadedCh, requestToBlock, minBlock, lastFinalizedBlockNumber)
			}
			fromBlock = requestToBlock + 1
			toBlock = fromBlock + d.syncBlockChunkSize
			if lastBlockNum != nil {
				toBlock = min(toBlock, *lastBlockNum)
			}
		} else {
			d.log.Debugf("range is not in a safe zone (requestToBlock: %d > finalized: %d)",
				requestToBlock, lastFinalizedBlockNumber)
			if blocks.Len() == 0 {
				if lastFinalizedBlockNumber >= fromBlock {
					emptyBlock := lastFinalizedBlockNumber
					skipEmpty := preReportedInitial && emptyBlock == initialFromBlock
					if !skipEmpty {
						d.reportEmptyBlock(ctx, downloadedCh, emptyBlock, fromBlock, lastFinalizedBlockNumber)
					}
					fromBlock = emptyBlock + 1
					toBlock = fromBlock + d.syncBlockChunkSize
					if lastBlockNum != nil {
						toBlock = min(toBlock, *lastBlockNum)
					}
				} else if reachTop {
					// We've swept the whole observed range up to the chain tip (requestToBlock ==
					// lastBlock) without finding any event and the reorg detector's finalized block
					// hasn't caught up yet. Report requestToBlock as an (unfinalized) empty block so
					// GetLastProcessedBlock reflects real sync progress instead of staying pinned to
					// the last block that had an event; the driver tracks it for reorgs like any other
					// non-finalized block.
					skipEmpty := preReportedInitial && requestToBlock == initialFromBlock
					if !skipEmpty {
						d.reportEmptyBlock(ctx, downloadedCh, requestToBlock, fromBlock, lastFinalizedBlockNumber)
					}
					fromBlock = requestToBlock + 1
					toBlock = fromBlock + d.syncBlockChunkSize
					if lastBlockNum != nil {
						toBlock = min(toBlock, *lastBlockNum)
					}
				} else if lastBlockNum == nil {
					// Not at the tip yet: extend range until we find logs or reach the last
					// finalized/observed block, instead of re-querying tiny increments.
					toBlock += d.syncBlockChunkSize
					// If lastBlockNum is set, don't extend; stop condition below handles it
				}
			} else {
				if !d.reportBlocks(ctx, downloadedCh, blocks, lastFinalizedBlockNumber) {
					close(downloadedCh)
					return
				}
				fromBlock = blocks[blocks.Len()-1].Num + 1
				toBlock = fromBlock + d.syncBlockChunkSize
				if lastBlockNum != nil {
					toBlock = min(toBlock, *lastBlockNum)
				}
			}
		}

		// Stop once we have processed up to lastBlockNum
		if lastBlockNum != nil && (fromBlock > *lastBlockNum || requestToBlock >= *lastBlockNum) {
			close(downloadedCh)
			return
		}

		iteration++
		if d.stopDownloaderOnIterationN != 0 && iteration >= d.stopDownloaderOnIterationN {
			d.log.Infof("stop downloader on iteration %d", iteration)
			return
		}
		if d.stopOnFinalizedBlockReached && fromBlock >= lastFinalizedBlockNumber {
			d.log.Infof("stop downloader on finalized block reached at block %d", lastFinalizedBlockNumber)
			return
		}
	}
}

// reportBlocks sends the blocks with events to the driver. Sends are cancellation-aware so a
// stalled or closed consumer (for example a processor that exited on a reorg) cannot strand the
// downloader goroutine forever. It returns false if the context was canceled before all blocks
// could be sent.
func (d *EVMDownloader) reportBlocks(ctx context.Context, downloadedCh chan EVMBlock,
	blocks EVMBlocks, lastFinalizedBlock uint64) bool {
	for _, block := range blocks {
		d.log.Debugf("sending block %d to the driver (with events)", block.Num)
		block.IsFinalizedBlock = block.Num <= lastFinalizedBlock
		select {
		case <-ctx.Done():
			return false
		case downloadedCh <- *block:
		}
	}
	return true
}

// reportEmptyBlock sends to the driver the header of blockNum as a block without events.
// The client can resolve a missing (null) block to a previous existing one (see
// RPCClientConfig.SupportNonEthereumWithMaxGapsInBlocks), so the header returned can have a lower number than blockNum.
// minBlock is the first block that can still be reported (the start of the window, or the one after the last
// block already reported): if the resolved header is before it, nothing is reported.
// Returns true if a block has been sent to the driver.
func (d *EVMDownloader) reportEmptyBlock(ctx context.Context, downloadedCh chan EVMBlock,
	blockNum, minBlock, lastFinalizedBlock uint64) bool {
	// Indicate the last downloaded block if there are not events on it
	d.log.Debugf("sending block %d to the driver (without events)", blockNum)
	header, isCanceled := d.GetBlockHeader(ctx, blockNum)
	if isCanceled {
		return false
	}
	if header.Num != blockNum {
		d.log.Infof("block %d is missing on the node, using previous block %d instead", blockNum, header.Num)
		if header.Num < minBlock {
			d.log.Infof("block %d is before the start of the window (%d), no empty block reported", header.Num, minBlock)
			return false
		}
	}

	select {
	case <-ctx.Done():
		return false
	case downloadedCh <- EVMBlock{
		IsFinalizedBlock: header.Num <= lastFinalizedBlock,
		EVMBlockHeader:   header,
	}:
	}
	return true
}

type EVMDownloaderImplementation struct {
	ethClient              aggkittypes.MultiDownloaderLegacy
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
	logsHook               LogsHookFunc
	logsEnricher           LogsEnricherFunc
}

// SetLogsHook sets an optional hook that is called after fetching logs and can modify them before processing.
// It returns the previous hook, if any.
func (d *EVMDownloaderImplementation) SetLogsHook(hook LogsHookFunc) LogsHookFunc {
	prev := d.logsHook
	d.logsHook = hook
	return prev
}

// SetLogsEnricher sets an optional enricher that is invoked after the logs hook and before the
// appender builds the events. It returns the previous enricher, if any.
func (d *EVMDownloaderImplementation) SetLogsEnricher(enricher LogsEnricherFunc) LogsEnricherFunc {
	prev := d.logsEnricher
	d.logsEnricher = enricher
	return prev
}

// NewEVMDownloaderImplementation creates a new EVMDownloaderImplementation
// finalizedBlockType can be nil, in this case, it means that the reorgs are not happening on the network
func NewEVMDownloaderImplementation(
	syncerID string,
	ethClient aggkittypes.MultiDownloaderLegacy,
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
	return d.ethClient.ChainID(ctx)
}

func (d *EVMDownloaderImplementation) GetLastFinalizedBlock(ctx context.Context) (uint64, error) {
	blockFinality := d.finalizedBlockType
	// if the finalized block type is nil, it means that the reorgs are not happening on the network
	if blockFinality == nil {
		blockFinality = &d.blockFinality
	}
	return d.ethClient.BlockNumber(ctx, *blockFinality)
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
			blockHeader, err := d.ethClient.HeaderByNumber(ctx, &d.blockFinality)
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
			blockNumber := blockHeader.Number
			headerHash := blockHeader.Hash
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
				trackedBlock, err := d.reorgDetector.GetTrackedBlockByBlockNumber(d.reorgDetectorID, blockNumber)
				if err != nil {
					continue
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
	blocks, canceled := d.getEventsByBlockRangeWithRetry(ctx, fromBlock, toBlock)
	if canceled {
		return nil
	}
	return blocks
}

func (d *EVMDownloaderImplementation) GetLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log {
	unfilteredLogs := d.getUnfilteredLogs(ctx, fromBlock, toBlock)
	return d.filterLogs(unfilteredLogs)
}

// enrichLogsOutcome is the result of running the optional logs enricher.
type enrichLogsOutcome int

const (
	enrichLogsOK enrichLogsOutcome = iota
	enrichLogsCanceled
	// enrichLogsRefetchRange signals the logs are stale/reorged and the whole range must be refetched.
	enrichLogsRefetchRange
)

// appendLogsOutcome is the result of building blocks and appending events for a range.
type appendLogsOutcome int

const (
	appendLogsOK appendLogsOutcome = iota
	appendLogsCanceled
	// appendLogsRefetchRange signals a stale/reorged log that requires refetching the whole range.
	appendLogsRefetchRange
	// appendLogsHashMismatch signals the event query and header query disagreed on a block hash.
	appendLogsHashMismatch
)

// getEventsByBlockRangeWithRetry fetches the logs for the range, runs the optional logs hook and
// enricher, and finally builds the blocks by appending events.
//
// It never reports a transient failure as an empty result: a stale inclusion
// (aggkittypes.ErrTransactionInclusionMismatch) or an event/header hash mismatch makes it discard
// the stale logs and refetch the whole range after a backoff, until the range resolves or the
// context is canceled. The previous recursive retry is replaced by this loop to avoid unbounded
// recursion.
//
// It returns (blocks, false) on success and (nil, true) when the context was canceled.
func (d *EVMDownloaderImplementation) getEventsByBlockRangeWithRetry(
	ctx context.Context, fromBlock, toBlock uint64,
) (EVMBlocks, bool) {
	hashMismatchAttempts := 0
	for {
		if ctx.Err() != nil {
			return nil, true
		}

		logs := d.GetLogs(ctx, fromBlock, toBlock)
		if ctx.Err() != nil {
			return nil, true
		}

		if d.logsHook != nil {
			originalCount := len(logs)
			logs = d.logsHook(ctx, fromBlock, toBlock, logs)
			if len(logs) != originalCount {
				d.log.Debugf("logsHook modified logs count from %d to %d for block range [%d, %d]",
					originalCount, len(logs), fromBlock, toBlock)
			}
		}

		// Optional contextual enrichment runs after the existing logs hook and before any
		// event is appended.
		if d.logsEnricher != nil {
			switch d.runLogsEnricher(ctx, fromBlock, toBlock, logs) {
			case enrichLogsOK:
			case enrichLogsCanceled:
				return nil, true
			case enrichLogsRefetchRange:
				// The enriched source is stale (reorged): drop the stale logs and refetch
				// the whole range instead of retrying the same stale log.
				if !d.waitBeforeRangeRefetch(ctx) {
					return nil, true
				}
				continue
			}
		}

		blocks, outcome := d.appendLogsToBlocks(ctx, logs)
		switch outcome {
		case appendLogsOK:
			return blocks, false
		case appendLogsCanceled:
			return nil, true
		case appendLogsRefetchRange:
			// The appender (through its contextual provider) resolved a stale inclusion:
			// refetch the whole range so the stale log is dropped.
			if !d.waitBeforeRangeRefetch(ctx) {
				return nil, true
			}
			continue
		case appendLogsHashMismatch:
			// The event query and the header query disagreed on a block hash, so the range
			// is not consistent and must be refetched. After the configured number of
			// attempts keep refetching (with backoff) instead of returning nil, which the
			// caller would interpret as an empty range and use to advance progress.
			hashMismatchAttempts++
			if hashMismatchAttempts >= MaxRetryCountBlockHashMismatch {
				d.log.Errorf("block hash mismatch persisted for range [%d, %d] after %d attempts, refetching range",
					fromBlock, toBlock, hashMismatchAttempts)
			}
			if !d.waitBeforeRangeRefetch(ctx) {
				return nil, true
			}
			continue
		}
	}
}

// runLogsEnricher invokes the optional enricher, retrying ordinary (retryable) errors with the
// RetryHandler and the download context. A stale inclusion (ErrTransactionInclusionMismatch) is
// reported back as enrichLogsRefetchRange so the caller refetches the whole range, and
// cancellation is reported as enrichLogsCanceled.
func (d *EVMDownloaderImplementation) runLogsEnricher(
	ctx context.Context, fromBlock, toBlock uint64, logs []types.Log,
) enrichLogsOutcome {
	attempts := 0
	for {
		if ctx.Err() != nil {
			return enrichLogsCanceled
		}
		err := d.logsEnricher(ctx, logs)
		if err == nil {
			return enrichLogsOK
		}
		if errors.Is(err, aggkittypes.ErrTransactionInclusionMismatch) {
			d.log.Warnf("logs enricher detected stale inclusion for range [%d, %d]: %v", fromBlock, toBlock, err)
			return enrichLogsRefetchRange
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Only a cancellation of the download context itself means "canceled".
			// A child-scoped cancellation or timeout (for example a per-RPC deadline)
			// can surface as context.Canceled/context.DeadlineExceeded while the
			// caller's context is still active. Reporting that as a cancellation
			// would make GetEventsByBlockRange return a nil (empty) range, which
			// Download would mistake for real progress and use to advance the
			// checkpoint, losing the logs of this range. Treat it as a transient
			// error and retry with the RetryHandler instead.
			if ctx.Err() != nil {
				return enrichLogsCanceled
			}
		}
		attempts++
		d.log.Errorf("error enriching logs for range [%d, %d] (attempt %d): %v", fromBlock, toBlock, attempts, err)
		d.rh.Handle(ctx, "enrichLogs", attempts)
	}
}

// waitBeforeRangeRefetch waits for the configured retry period before refetching a whole range,
// returning false if the context is done first so callers can stop. It prevents a busy refetch
// loop when the node keeps answering with a stale/reorged view.
func (d *EVMDownloaderImplementation) waitBeforeRangeRefetch(ctx context.Context) bool {
	period := d.rh.RetryAfterErrorPeriod
	if period <= 0 {
		period = DefaultWaitPeriodBlockNotFound
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(period):
		return true
	}
}

// appendLogsToBlocks builds the blocks for the given logs and appends the events through the
// appender map. Ordering of the logs is preserved. It returns appendLogsHashMismatch when the
// header resolved for a block does not match the hash carried by its log, and
// appendLogsRefetchRange when an appender returns aggkittypes.ErrTransactionInclusionMismatch.
func (d *EVMDownloaderImplementation) appendLogsToBlocks(
	ctx context.Context, logs []types.Log,
) (EVMBlocks, appendLogsOutcome) {
	blocks := make(EVMBlocks, 0, len(logs))
	var latestBlock *EVMBlock
	for _, l := range logs {
		if ctx.Err() != nil {
			return nil, appendLogsCanceled
		}
		// A log without topics cannot be attributed to an appender function and would make
		// the Topics[0] lookup panic. Skip it defensively.
		if len(l.Topics) == 0 {
			d.log.Warnf("skipping log without topics at block %d", l.BlockNumber)
			continue
		}

		if latestBlock == nil || latestBlock.Num < l.BlockNumber {
			b, canceled := d.GetBlockHeader(ctx, l.BlockNumber)
			if canceled {
				return nil, appendLogsCanceled
			}

			if b.Hash != l.BlockHash {
				d.log.Warnf(
					"there has been a block hash change between the event query and the block query "+
						"for block %d: %s vs (logs)%s",
					l.BlockNumber, b.Hash, l.BlockHash,
				)
				return nil, appendLogsHashMismatch
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
		if appenderFn == nil {
			d.log.Debugf("no appender function found for topic %s, ignoring log at block %d",
				l.Topics[0].Hex(), l.BlockNumber)
			continue
		}

		attempts := 0
		for {
			if ctx.Err() != nil {
				return nil, appendLogsCanceled
			}
			err := appenderFn(latestBlock, l)
			if err == nil {
				break
			}
			if errors.Is(err, aggkittypes.ErrTransactionInclusionMismatch) {
				// The appender's contextual provider resolved a stale inclusion: the caller
				// must refetch the whole range instead of retrying this stale log.
				d.log.Warnf("appender resolved stale inclusion for block %d: %v", l.BlockNumber, err)
				return nil, appendLogsRefetchRange
			}
			attempts++
			d.log.Error("error trying to append log: ", err)
			d.rh.Handle(ctx, "appendLogs", attempts)
		}
	}

	return blocks, appendLogsOK
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
		if len(l.Topics) > 0 && slices.Contains(d.topicsToQuery, l.Topics[0]) {
			filteredLogs = append(filteredLogs, l)
		}
	}
	return filteredLogs
}

func (d *EVMDownloaderImplementation) GetBlockHeader(ctx context.Context, blockNum uint64) (EVMBlockHeader, bool) {
	attempts := 0
	for {
		header, err := d.ethClient.HeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNum))
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
			// A canceled context makes rh.Handle return immediately; without this
			// guard the loop would spin on a client that keeps failing while the
			// context is already done.
			if ctx.Err() != nil {
				return EVMBlockHeader{}, true
			}
			continue
		}
		return EVMBlockHeader{
			Num:        header.Number,
			Hash:       header.Hash,
			ParentHash: *header.ParentHash,
			Timestamp:  header.Time,
		}, false
	}
}
