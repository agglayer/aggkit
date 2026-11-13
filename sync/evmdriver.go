package sync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrInconsistentState    = errors.New("state is inconsistent, try again later once the state is consolidated")
	ErrNoLastProcessedBlock = errors.New("no block has been processed yet")
	ErrAlreadyBootstrapped  = errors.New("SyncNextBlock: already bootstrapped, a processed block already exists")
)

var _ fmt.Stringer = (*Block)(nil)

type Block struct {
	Num    uint64
	Events []any
	Hash   common.Hash
}

func (b Block) String() string {
	return fmt.Sprintf("SyncBlock{Num: %d, Hash: %s, EventsCount: %d}",
		b.Num, b.Hash.String(), len(b.Events))
}

type Downloader interface {
	// Download downloads blocks starting from fromBlock, sending them to downloadedCh.
	// If lastBlockNum is not nil, it stops after processing that block.
	// If includeEmptyFirstBlock is true, fromBlock is always sent even if it has no events.
	// If fromBlock can't be sent (e.g. it is missing on the node) the channel is closed without sending it.
	Download(ctx context.Context, fromBlock uint64, downloadedCh chan EVMBlock,
		lastBlockNum *uint64, includeEmptyFirstBlock bool)
	// RuntimeData returns the runtime data from this downloader
	// this is used to check that DB is compatible with the runtime data
	RuntimeData(ctx context.Context) (RuntimeData, error)
}

type EVMDriver struct {
	reorgDetector ReorgDetector
	reorgSub      *reorgdetector.Subscription
	processor     processorInterface
	// batchProcessor is non-nil only when the processor was explicitly wrapped for
	// batching (see EnableBlockBatching). When nil the driver keeps the legacy
	// one-block-at-a-time path.
	batchProcessor       BlockBatchProcessor
	downloader           Downloader
	reorgDetectorID      string
	downloadBufferSize   int
	rh                   *RetryHandler
	log                  aggkitcommon.Logger
	compatibilityChecker compatibility.CompatibilityChecker
	blockSubscriber      aggkitcommon.PubSub[Block]

	// bootstrapMu serializes processor mutations performed while the driver has
	// no processed block yet: the bootstrap block handling of SyncNextBlock and
	// the reorg rollback that Sync performs while waiting for a checkpoint (see
	// Sync). Without it an external bootstrap and an incoming reorg could mutate
	// the processor concurrently.
	bootstrapMu sync.Mutex
}

// RuntimeData is the data that is used to check that the DB is compatible with the runtime data
// basically it contains the relevant data from runtime environment
type RuntimeData struct {
	ChainID   uint64
	Addresses []common.Address
}

func (r RuntimeData) String() string {
	res := fmt.Sprintf("ChainID: %d, Addresses: ", r.ChainID)
	for _, addr := range r.Addresses {
		res += addr.String() + ", "
	}
	return res
}

func (r RuntimeData) IsCompatible(other RuntimeData) (*RuntimeData, error) {
	if r.ChainID != other.ChainID {
		return nil, fmt.Errorf("chain ID mismatch: %d != %d", r.ChainID, other.ChainID)
	}
	if len(r.Addresses) != len(other.Addresses) {
		return nil, fmt.Errorf("addresses len mismatch: %d != %d", len(r.Addresses), len(other.Addresses))
	}
	for i, addr := range r.Addresses {
		if addr != other.Addresses[i] {
			return nil, fmt.Errorf("addresses[%d] mismatch: %s != %s", i, addr.String(), other.Addresses[i].String())
		}
	}
	return nil, nil
}

type processorInterface interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, bool, error)
	ProcessBlock(ctx context.Context, block Block) error
	Reorg(ctx context.Context, firstReorgedBlock uint64) error
}

// MaxBatchBlocks is the hard upper bound on how many blocks the driver hands to a
// batching processor in one ProcessBlocks call. It bounds both the in-flight
// memory and the size of the single processing transaction.
const MaxBatchBlocks = 100

// BlockBatchProcessor is an OPTIONAL processor capability. A processor that
// implements it can process an ordered batch of blocks in a SINGLE database
// transaction (all-or-nothing). It is deliberately not part of the mandatory
// processorInterface: the EVMDriver uses it only when the processor was
// explicitly wrapped for batching through EnableBlockBatching, so every existing
// caller (and every legacy/L2 syncer) keeps the exact one-block-at-a-time path.
type BlockBatchProcessor interface {
	// ProcessBlocks applies the given ordered blocks atomically. On error
	// (including context cancellation) the whole batch must be rolled back so a
	// retried batch can never duplicate committed history.
	ProcessBlocks(ctx context.Context, blocks []Block) error
}

// Processor is the exported form of the mandatory processor contract. It is only
// needed so other packages can wrap a processor with the optional batching
// capability via EnableBlockBatching; the driver keeps using processorInterface
// internally so the generated mock and existing references stay unchanged.
type Processor interface {
	processorInterface
}

// BatchProcessFunc is the atomic batch implementation wrapped by
// EnableBlockBatching.
type BatchProcessFunc func(ctx context.Context, blocks []Block) error

// batchingProcessor adapts a Processor into the optional BlockBatchProcessor
// capability. It delegates the mandatory methods unchanged and only adds the
// batch method, so the wrapped processor keeps behaving exactly as before for
// every non-batch caller.
type batchingProcessor struct {
	Processor
	batch BatchProcessFunc
}

func (p *batchingProcessor) ProcessBlocks(ctx context.Context, blocks []Block) error {
	return p.batch(ctx, blocks)
}

// EnableBlockBatching wraps a processor so the EVMDriver may process blocks in
// bounded, ordered batches through the OPTIONAL BlockBatchProcessor capability.
// It is strictly opt-in: callers that do not wrap their processor keep the legacy
// one-block path. The batch function MUST be atomic (single transaction) and MUST
// preserve the per-block semantics of ProcessBlock.
//
// A nil batch function is a no-op: the original processor is returned unwrapped,
// so the driver keeps the legacy one-block path instead of exposing a batch
// capability that would panic when used.
func EnableBlockBatching(processor Processor, batch BatchProcessFunc) Processor {
	if batch == nil {
		return processor
	}
	return &batchingProcessor{Processor: processor, batch: batch}
}

type ReorgDetector interface {
	Subscribe(id string) (*reorgdetector.Subscription, error)
	AddBlockToTrack(ctx context.Context, id string, blockNum uint64, blockHash common.Hash) error
	GetFinalizedBlockType() aggkittypes.BlockNumberFinality
	GetTrackedBlockByBlockNumber(id string, blockNumber uint64) (*reorgdetector.Header, error)
	String() string
}

func NewEVMDriver(
	reorgDetector ReorgDetector,
	processor processorInterface,
	downloader Downloader,
	reorgDetectorID string,
	downloadBufferSize int,
	rh *RetryHandler,
	compatibilityChecker compatibility.CompatibilityChecker,
) (*EVMDriver, error) {
	logger := log.WithFields("syncer", reorgDetectorID)
	reorgSub, err := reorgDetector.Subscribe(reorgDetectorID)
	if err != nil {
		return nil, err
	}

	// The batch capability is optional and opt-in: only a processor explicitly
	// wrapped with EnableBlockBatching exposes it, so unwrapped processors (all
	// legacy/L2 callers) never batch accidentally.
	var batchProcessor BlockBatchProcessor
	if bp, ok := processor.(BlockBatchProcessor); ok {
		batchProcessor = bp
	}

	return &EVMDriver{
		reorgDetector:        reorgDetector,
		reorgSub:             reorgSub,
		processor:            processor,
		batchProcessor:       batchProcessor,
		downloader:           downloader,
		reorgDetectorID:      reorgDetectorID,
		downloadBufferSize:   downloadBufferSize,
		rh:                   rh,
		log:                  logger,
		compatibilityChecker: compatibilityChecker,
		blockSubscriber:      aggkitcommon.NewGenericSubscriber[Block](),
	}, nil
}

func (d *EVMDriver) SubscribeToNewBlocks(subscriberName string) <-chan Block {
	return d.blockSubscriber.Subscribe(subscriberName)
}

// Legacy syncer doesn't support completion percentage, so we return nil here.
func (d *EVMDriver) GetCompletionPercentage() *float64 {
	return nil
}

// SyncNextBlock downloads and processes the given blockNum as a bootstrap step.
// It requires that no block has been processed yet (GetLastProcessedBlock returns found=false).
// Returns ErrAlreadyBootstrapped if a processed block already exists — callers may safely ignore this.
func (d *EVMDriver) SyncNextBlock(ctx context.Context, blockNum uint64) error {
	_, found, err := d.processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return fmt.Errorf("SyncNextBlock: getting last processed block: %w", err)
	}
	if found {
		return ErrAlreadyBootstrapped
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	downloadCh := make(chan EVMBlock, 1)
	// downloaderDone is closed as soon as the bootstrap downloader goroutine
	// returns. The downloader may hold per-generation resources (e.g.
	// multidownloader transaction enrichment), so the generation must be fully
	// joined on every exit path before returning or processing, otherwise it
	// could leak or race the next generation.
	downloaderDone := make(chan struct{})
	go func() {
		defer close(downloaderDone)
		d.downloader.Download(cancelCtx, blockNum, downloadCh, &blockNum, true)
	}()
	// Always cancel and join the bootstrap downloader before returning. The join
	// is intentionally unbounded: a bounded wait would hide a leaked goroutine
	// instead of surfacing it.
	defer func() {
		cancel()
		<-downloaderDone
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case b, ok := <-downloadCh:
		if !ok {
			return fmt.Errorf("SyncNextBlock: download channel closed unexpectedly")
		}
		// Stop and join the downloader before processing the block so it cannot
		// mutate shared state while the block is handled. Serialize the
		// processing with the reorg rollback that Sync may run while waiting for
		// a checkpoint (see bootstrapMu).
		cancel()
		<-downloaderDone
		d.bootstrapMu.Lock()
		defer d.bootstrapMu.Unlock()
		return d.handleNewBlock(ctx, b)
	}
}

func (d *EVMDriver) Sync(ctx context.Context, firstBlockNumber *uint64) {
reset:
	var (
		lastProcessedBlock uint64
		attempts           int
		err                error
		found              bool
	)
	for {
		if ctx.Err() != nil {
			d.log.Info("sync stopped due to context done while checking compatibility")
			return
		}
		if err = d.compatibilityChecker.Check(ctx, nil); err != nil {
			attempts++
			d.log.Error("error checking compatibility data between downloader (runtime) and processor (db): ", err)
			d.rh.Handle(ctx, "CompatibilityChecker", attempts)
			continue
		}
		break
	}
	var nextBlock uint64
	for {
		// A canceled context makes rh.Handle return immediately; without this
		// guard the retry loops below would spin instead of exiting.
		if ctx.Err() != nil {
			d.log.Info("sync stopped due to context done while waiting for processed blocks")
			return
		}
		// Now we let to have no processed block and wait until appears
		lastProcessedBlock, found, err = d.processor.GetLastProcessedBlock(ctx)
		if err == nil && found {
			nextBlock = lastProcessedBlock + 1
			break
		}
		if err != nil {
			attempts++
			d.log.Error("error getting last processed block: ", err)
			d.rh.Handle(ctx, "Sync", attempts)
			continue
		}
		if !found {
			if firstBlockNumber != nil {
				d.log.Infof("no processed blocks found, starting from configured initial block number %d", *firstBlockNumber)
				nextBlock = *firstBlockNumber // we will start syncing from the initial block number
				break
			}
			d.log.Infof("no processed blocks found, waiting %s", d.rh.RetryAfterErrorPeriod)
			select {
			case <-ctx.Done():
				d.log.Info("context done while waiting for processed blocks, exiting sync")
				return
			case firstReorgedBlock := <-d.reorgSub.ReorgedBlock:
				// The reorg detector blocks until the subscriber acknowledges the
				// reorg. While there is no checkpoint yet (for example after a
				// restart, before an external bootstrap) a reorg may still be
				// notified for a block tracked by the detector but not present in
				// the processor. Acknowledge it here so the detector is not
				// stalled. There is nothing to roll back yet, but the rollback is
				// run anyway to keep the same code path; it is serialized with any
				// in-flight bootstrap (SyncNextBlock) through bootstrapMu so the
				// processor is not mutated concurrently.
				d.log.Warnf("reorg detected from block %d while waiting for the initial block, acknowledging it",
					firstReorgedBlock)
				d.bootstrapMu.Lock()
				reorgErr := d.handleReorg(ctx, firstReorgedBlock)
				d.bootstrapMu.Unlock()
				if reorgErr != nil {
					d.log.Errorf("failed to process reorg at block %d: %w", firstReorgedBlock, reorgErr)
				}
				continue
			case <-time.After(d.rh.RetryAfterErrorPeriod):
				continue
			}
		}
		break
	}
	// setup context to cancel downloader and/or block processor
	cancellableCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	d.log.Infof("Starting sync... lastProcessedBlock %d NextBlock: %d",
		lastProcessedBlock, nextBlock)
	// start downloading
	downloadCh := make(chan EVMBlock, d.downloadBufferSize)
	// downloaderDone is closed as soon as the downloader goroutine returns.
	// The downloader may hold per-generation resources (e.g. multidownloader
	// transaction enrichment), so a generation must always be fully joined
	// before another one is started or a rollback is performed.
	downloaderDone := make(chan struct{})
	go func() {
		defer close(downloaderDone)
		d.downloader.Download(cancellableCtx, nextBlock, downloadCh, nil, false)
		log.Warnf("downloader.Download exited, cancelling context")
		cancel()
	}()

	// Block processing goroutine
	blockProcessingDone := make(chan struct{})
	go func() {
		defer close(blockProcessingDone)

		for {
			select {
			case <-cancellableCtx.Done():
				d.log.Info("cancellableCtx done, exiting block handler goroutine")
				return
			case b, ok := <-downloadCh:
				if !ok {
					d.log.Info("downloadCh closed, stopping block processing")
					return
				}

				// When the processor exposes the OPTIONAL batch capability, drain the
				// blocks that are already available (bounded by MaxBatchBlocks, never
				// waiting for more) and process them in one transaction. Otherwise keep
				// the exact legacy one-block path.
				batch := []EVMBlock{b}
				closed := false
				if d.batchProcessor != nil {
					batch, closed = d.drainBatch(cancellableCtx, downloadCh, b)
				}

				d.log.Debugf("handleNewBlocks, batchSize: %d, firstBlock: %d", len(batch), b.Num)
				if err := d.handleNewBlocks(cancellableCtx, batch); err != nil {
					// Permanent processing error (e.g. inconsistent state):
					// tear down this generation. The main loop below joins it
					// and waits for a reorg instead of restarting on its own.
					cancel()
					return
				}
				if closed {
					d.log.Info("downloadCh closed, stopping block processing")
					return
				}
			}
		}
	}()

	// Wait for reorg and then interrupt
	for {
		select {
		case <-ctx.Done():
			d.log.Info("sync stopped due to context done")
			cancel()
			d.joinGeneration(downloaderDone, blockProcessingDone)
			return
		case firstReorgedBlock := <-d.reorgSub.ReorgedBlock:
			d.log.Warnf("Reorg detected from block %d, stopping block processing...", firstReorgedBlock)
			cancel()
			// Wait for the whole generation (downloader + block processing) to
			// exit cleanly before rolling back and restarting.
			d.joinGeneration(downloaderDone, blockProcessingDone)
			if err := d.handleReorg(ctx, firstReorgedBlock); err != nil {
				d.log.Errorf("failed to process reorg at block %d: %w", firstReorgedBlock, err)
			}
			goto reset
		case <-blockProcessingDone:
			// Block processing stopped on its own (permanent processing error or
			// the download channel was closed) and it already cancelled the
			// generation context. Join the generation before waiting for a reorg
			// so the previous downloader is not leaked/raced against the next one.
			d.log.Warn("block processing stopped, cancelling generation and waiting for reorg")
			cancel()
			d.joinGeneration(downloaderDone, blockProcessingDone)
			firstReorgedBlock, ok := d.waitNextReorg(ctx)
			if !ok {
				d.log.Info("sync stopped due to context done")
				return
			}
			d.log.Warnf("Reorg detected from block %d, stopping block processing...", firstReorgedBlock)
			if err := d.handleReorg(ctx, firstReorgedBlock); err != nil {
				d.log.Errorf("failed to process reorg at block %d: %w", firstReorgedBlock, err)
			}
			goto reset
		}
	}
}

// joinGeneration waits until both goroutines of the current generation, the
// downloader and the block processor, have finished. It must be called before
// restarting a generation (e.g. after a reorg) or performing a rollback, since
// the downloader may still be using per-generation resources.
func (d *EVMDriver) joinGeneration(downloaderDone, blockProcessingDone <-chan struct{}) {
	<-downloaderDone
	<-blockProcessingDone
}

// waitNextReorg blocks until a reorg is notified or ctx is done. It returns the
// first reorged block and true when a reorg arrived, or false when ctx was done.
func (d *EVMDriver) waitNextReorg(ctx context.Context) (uint64, bool) {
	select {
	case <-ctx.Done():
		return 0, false
	case firstReorgedBlock := <-d.reorgSub.ReorgedBlock:
		return firstReorgedBlock, true
	}
}

func (d *EVMDriver) handleNewBlock(ctx context.Context, b EVMBlock) error {
	if err := d.trackNonFinalizedBlock(ctx, b); err != nil {
		return nil
	}

	return d.processBlock(ctx, b)
}

// drainBatch collects the blocks that are already available on downloadCh,
// starting with first, up to MaxBatchBlocks. It never blocks waiting for more
// blocks: it only drains what the downloader has already produced, so it cannot
// hold a partially-filled batch while the downloader stalls. The second return
// value reports whether downloadCh was closed while draining, so the caller stops
// after processing the collected batch. On context cancellation it returns the
// blocks gathered so far (the processor will roll the batch back).
func (d *EVMDriver) drainBatch(ctx context.Context, downloadCh <-chan EVMBlock, first EVMBlock) ([]EVMBlock, bool) {
	batch := make([]EVMBlock, 0, MaxBatchBlocks)
	batch = append(batch, first)
	for len(batch) < MaxBatchBlocks {
		if ctx.Err() != nil {
			return batch, false
		}
		select {
		case b, ok := <-downloadCh:
			if !ok {
				return batch, true
			}
			batch = append(batch, b)
		default:
			return batch, false
		}
	}
	return batch, false
}

// handleNewBlocks processes an ordered batch of blocks. It tracks every
// non-finalized block for reorgs first (exactly like the one-block path), and
// only then processes the batch through the OPTIONAL batch capability. It falls
// back to the legacy per-block path when the capability is absent, keeping the
// behaviour of every non-batching caller unchanged.
func (d *EVMDriver) handleNewBlocks(ctx context.Context, blocks []EVMBlock) error {
	if d.batchProcessor == nil {
		for _, b := range blocks {
			if err := d.handleNewBlock(ctx, b); err != nil {
				return err
			}
		}
		return nil
	}

	for _, b := range blocks {
		if err := d.trackNonFinalizedBlock(ctx, b); err != nil {
			return nil
		}
	}

	return d.processBlocks(ctx, blocks)
}

// processBlocks converts the downloaded EVM blocks into sync blocks and hands the
// whole ordered batch to the processor in a single call. Subscribers are notified
// for every block, in the original download order, only AFTER the batch committed:
// if the batch fails (and is retried) nothing is published, so a failure can never
// produce a partial notification.
func (d *EVMDriver) processBlocks(ctx context.Context, evmBlocks []EVMBlock) error {
	blocks := make([]Block, len(evmBlocks))
	for i := range evmBlocks {
		blocks[i] = Block{
			Num:    evmBlocks[i].Num,
			Hash:   evmBlocks[i].Hash,
			Events: evmBlocks[i].Events,
		}
	}

	return d.withRetry(ctx, "processBlocks", func() error {
		err := d.batchProcessor.ProcessBlocks(ctx, blocks)
		if errors.Is(err, ErrInconsistentState) {
			d.log.Warn("state got inconsistent after processing this batch; halting until reorg")
			return newStopRetryError(err)
		}
		if err != nil {
			return err
		}

		// Notify subscribers about every block, in the original order, after the
		// whole batch committed. A retried batch publishes nothing until it commits.
		for _, block := range blocks {
			d.blockSubscriber.Publish(block)
		}

		return nil
	})
}

func (d *EVMDriver) trackNonFinalizedBlock(ctx context.Context, b EVMBlock) error {
	if b.IsFinalizedBlock {
		return nil
	}

	return d.withRetry(ctx, "trackBlock", func() error {
		return d.reorgDetector.AddBlockToTrack(ctx, d.reorgDetectorID, b.Num, b.Hash)
	})
}

func (d *EVMDriver) processBlock(ctx context.Context, b EVMBlock) error {
	return d.withRetry(ctx, "processBlock", func() error {
		block := Block{
			Num:    b.Num,
			Hash:   b.Hash,
			Events: b.Events,
		}
		err := d.processor.ProcessBlock(ctx, block)
		if errors.Is(err, ErrInconsistentState) {
			d.log.Warn("state got inconsistent after processing this block; halting until reorg")
			return newStopRetryError(err)
		}
		if err != nil {
			return err
		}

		// Notify subscribers about the new block after successful processing
		d.blockSubscriber.Publish(block)

		return nil
	})
}

func (d *EVMDriver) handleReorg(ctx context.Context, firstReorgedBlock uint64) error {
	defer func() {
		d.reorgSub.ReorgProcessed <- true
	}()

	return d.withRetry(ctx, "handleReorg", func() error {
		return d.processor.Reorg(ctx, firstReorgedBlock)
	})
}

// withRetry is a helper wrapper function that invokes the fn callback on failed attempts
func (d *EVMDriver) withRetry(ctx context.Context, opName string, fn func() error) error {
	attempts := 0
	for {
		select {
		case <-ctx.Done():
			d.log.Warnf("context canceled during %s", opName)
			return nil
		default:
			err := fn()
			if err != nil {
				attempts++
				d.log.Errorf("error during %s (attempt %d): %v", opName, attempts, err)

				// Stop retrying on permanent errors
				if isStopRetryError(err) {
					return err
				}

				d.rh.Handle(ctx, opName, attempts)
			} else {
				return nil
			}
		}
	}
}

type stopRetryError struct {
	err error
}

func (e *stopRetryError) Error() string { return e.err.Error() }
func (e *stopRetryError) Unwrap() error { return e.err }

func newStopRetryError(err error) error {
	return &stopRetryError{err: err}
}

func isStopRetryError(err error) bool {
	var perr *stopRetryError
	return errors.As(err, &perr)
}
