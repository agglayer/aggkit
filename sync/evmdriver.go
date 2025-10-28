package sync

import (
	"context"
	"errors"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

var ErrInconsistentState = errors.New("state is inconsistent, try again later once the state is consolidated")

type Block struct {
	Num    uint64
	Events []any
	Hash   common.Hash
}

type Downloader interface {
	Download(ctx context.Context, fromBlock uint64, downloadedCh chan EVMBlock)
	// RuntimeData returns the runtime data from this downloader
	// this is used to check that DB is compatible with the runtime data
	RuntimeData(ctx context.Context) (RuntimeData, error)
}

type EVMDriver struct {
	reorgDetector        ReorgDetector
	reorgSub             *reorgdetector.Subscription
	processor            processorInterface
	downloader           Downloader
	reorgDetectorID      string
	downloadBufferSize   int
	rh                   *RetryHandler
	log                  aggkitcommon.Logger
	compatibilityChecker compatibility.CompatibilityChecker
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

func (r RuntimeData) IsCompatible(other RuntimeData) error {
	if r.ChainID != other.ChainID {
		return fmt.Errorf("chain ID mismatch: %d != %d", r.ChainID, other.ChainID)
	}
	if len(r.Addresses) != len(other.Addresses) {
		return fmt.Errorf("addresses len mismatch: %d != %d", len(r.Addresses), len(other.Addresses))
	}
	for i, addr := range r.Addresses {
		if addr != other.Addresses[i] {
			return fmt.Errorf("addresses[%d] mismatch: %s != %s", i, addr.String(), other.Addresses[i].String())
		}
	}
	return nil
}

type processorInterface interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
	ProcessBlock(ctx context.Context, block Block) error
	Reorg(ctx context.Context, firstReorgedBlock uint64) error
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

	return &EVMDriver{
		reorgDetector:        reorgDetector,
		reorgSub:             reorgSub,
		processor:            processor,
		downloader:           downloader,
		reorgDetectorID:      reorgDetectorID,
		downloadBufferSize:   downloadBufferSize,
		rh:                   rh,
		log:                  logger,
		compatibilityChecker: compatibilityChecker,
	}, nil
}

func (d *EVMDriver) Sync(ctx context.Context) {
reset:
	var (
		lastProcessedBlock uint64
		attempts           int
		err                error
	)
	for {
		if err = d.compatibilityChecker.Check(ctx, nil); err != nil {
			attempts++
			d.log.Error("error checking compatibility data between downloader (runtime) and processor (db): ", err)
			d.rh.Handle(ctx, "CompatibilityChecker", attempts)
			continue
		}
		break
	}

	for {
		lastProcessedBlock, err = d.processor.GetLastProcessedBlock(ctx)
		if err != nil {
			attempts++
			d.log.Error("error getting last processed block: ", err)
			d.rh.Handle(ctx, "Sync", attempts)
			continue
		}
		break
	}

	// setup context to cancel downloader and/or block processor
	cancellableCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	d.log.Infof("Starting sync... lastProcessedBlock %d", lastProcessedBlock)
	// start downloading
	downloadCh := make(chan EVMBlock, d.downloadBufferSize)
	go d.downloader.Download(cancellableCtx, lastProcessedBlock+1, downloadCh)

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

				d.log.Debugf("handleNewBlock, blockNum: %d, blockHash: %s", b.Num, b.Hash)
				if err := d.handleNewBlock(cancellableCtx, b); err != nil {
					cancel()
				}
			}
		}
	}()

	// Wait for reorg and then interrupt
	select {
	case <-ctx.Done():
		d.log.Info("sync stopped due to context done")
		cancel()
		<-blockProcessingDone
		return
	case firstReorgedBlock := <-d.reorgSub.ReorgedBlock:
		d.log.Warnf("Reorg detected from block %d", firstReorgedBlock)
		cancel()
		<-blockProcessingDone // wait for block processing to exit cleanly
		if err := d.handleReorg(ctx, firstReorgedBlock); err != nil {
			d.log.Errorf("failed to process reorg at block %d: %w", firstReorgedBlock, err)
		}
		goto reset
	}
}

func (d *EVMDriver) handleNewBlock(ctx context.Context, b EVMBlock) error {
	if err := d.trackNonFinalizedBlock(ctx, b); err != nil {
		return nil
	}

	return d.processBlock(ctx, b)
}

func (d *EVMDriver) trackNonFinalizedBlock(ctx context.Context, b EVMBlock) error {
	return d.withRetry(ctx, "trackBlock", func() error {
		return d.reorgDetector.AddBlockToTrack(ctx, d.reorgDetectorID, b.Num, b.Hash)
	})
}

func (d *EVMDriver) processBlock(ctx context.Context, b EVMBlock) error {
	return d.withRetry(ctx, "processBlock", func() error {
		err := d.processor.ProcessBlock(ctx, Block{
			Num:    b.Num,
			Hash:   b.Hash,
			Events: b.Events,
		})
		if errors.Is(err, ErrInconsistentState) {
			d.log.Warn("state got inconsistent after processing this block; halting until reorg")
			return newStopRetryError(err)
		}
		return err
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
