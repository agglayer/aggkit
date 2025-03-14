package sync

import (
	"context"
	"errors"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/ethereum/go-ethereum/common"
)

var ErrInconsistentState = errors.New("state is inconsistent, try again later once the state is consolidated")

type Block struct {
	Num    uint64
	Events []interface{}
	Hash   common.Hash
}

type downloader interface {
	Download(ctx context.Context, fromBlock uint64, downloadedCh chan EVMBlock)
	// GetRuntimeData returns the runtime data from this downloader
	// this is used to check that DB is compatible with the runtime data
	GetRuntimeData(ctx context.Context) (RuntimeData, error)
}

type EVMDriver struct {
	reorgDetector        ReorgDetector
	reorgSub             *reorgdetector.Subscription
	processor            processorInterface
	downloader           downloader
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
		return fmt.Errorf("addresses len mishmatch: %d != %d", len(r.Addresses), len(other.Addresses))
	}
	for i, addr := range r.Addresses {
		if addr != other.Addresses[i] {
			return fmt.Errorf("addresses[%d] mishmatch: %s != %s", i, addr.String(), other.Addresses[i].String())
		}
	}
	return nil
}

type processorInterface interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
	ProcessBlock(ctx context.Context, block Block) error
	Reorg(ctx context.Context, firstReorgedBlock uint64) error
	// CheckCompatibilityData is the interface to set / retrieve the compatibility data to storage
	compatibility.CompatibilityDataStorager[RuntimeData]
}

type ReorgDetector interface {
	Subscribe(id string) (*reorgdetector.Subscription, error)
	AddBlockToTrack(ctx context.Context, id string, blockNum uint64, blockHash common.Hash) error
	GetFinalizedBlockType() etherman.BlockNumberFinality
	String() string
}

func NewEVMDriver(
	reorgDetector ReorgDetector,
	processor processorInterface,
	downloader downloader,
	reorgDetectorID string,
	downloadBufferSize int,
	rh *RetryHandler,
	requireStorageContentCompatibility bool,
) (*EVMDriver, error) {
	logger := log.WithFields("syncer", reorgDetectorID)
	reorgSub, err := reorgDetector.Subscribe(reorgDetectorID)
	if err != nil {
		return nil, err
	}
	compatibilityChecker := compatibility.NewCompatibilityCheck[RuntimeData](
		requireStorageContentCompatibility,
		downloader.GetRuntimeData,
		processor)

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
		err = d.compatibilityChecker.Check(ctx, nil)
		if err != nil {
			attempts++
			d.log.Error("error checking compatibility data between downloader(runtime) and processor (db): ", err)
			d.rh.Handle("Sync", attempts)
			continue
		}
		break
	}
	for {
		lastProcessedBlock, err = d.processor.GetLastProcessedBlock(ctx)
		if err != nil {
			attempts++
			d.log.Error("error getting last processed block: ", err)
			d.rh.Handle("Sync", attempts)
			continue
		}
		break
	}
	cancellableCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	d.log.Infof("Starting sync... lastProcessedBlock %d", lastProcessedBlock)
	// start downloading
	downloadCh := make(chan EVMBlock, d.downloadBufferSize)
	go d.downloader.Download(cancellableCtx, lastProcessedBlock+1, downloadCh)

	for {
		select {
		case <-ctx.Done():
			d.log.Info("sync stopped due to context done")
			cancel()
			return
		case b, ok := <-downloadCh:
			if ok {
				// when channel is closing, it is sending an empty block with num = 0, and empty hash
				// because it is not passing object by reference, but by value, so do not handle that since it is closing
				d.log.Debugf("handleNewBlock, blockNum: %d, blockHash: %s", b.Num, b.Hash)
				d.handleNewBlock(ctx, cancel, b)
			}
		case firstReorgedBlock := <-d.reorgSub.ReorgedBlock:
			d.log.Debug("handleReorg from block: ", firstReorgedBlock)
			d.handleReorg(ctx, cancel, firstReorgedBlock)
			goto reset
		}
	}
}

func (d *EVMDriver) handleNewBlock(ctx context.Context, cancel context.CancelFunc, b EVMBlock) {
	attempts := 0
	succeed := false
	for {
		select {
		case <-ctx.Done():
			// If the context is canceled, exit the function
			d.log.Warnf("context canceled while adding block %d to tracker", b.Num)
			return
		default:
			if !b.IsFinalizedBlock {
				err := d.reorgDetector.AddBlockToTrack(ctx, d.reorgDetectorID, b.Num, b.Hash)
				if err != nil {
					attempts++
					d.log.Errorf("error adding block %d to tracker: %v", b.Num, err)
					d.rh.Handle("handleNewBlock", attempts)
				} else {
					succeed = true
				}
			} else {
				succeed = true
			}
		}
		if succeed {
			break
		}
	}
	attempts = 0
	succeed = false
	for {
		select {
		case <-ctx.Done():
			// If the context is canceled, exit the function
			d.log.Warnf("context canceled while processing block %d", b.Num)
			return
		default:
			blockToProcess := Block{
				Num:    b.Num,
				Events: b.Events,
				Hash:   b.Hash,
			}
			err := d.processor.ProcessBlock(ctx, blockToProcess)
			if err != nil {
				if errors.Is(err, ErrInconsistentState) {
					d.log.Warn("state got inconsistent after processing this block. Stopping downloader until there is a reorg")
					cancel()
					return
				}
				attempts++
				d.log.Errorf("error processing events for block %d, err: %v", b.Num, err)
				d.rh.Handle("handleNewBlock", attempts)
			} else {
				succeed = true
			}
		}
		if succeed {
			break
		}
	}
}

func (d *EVMDriver) handleReorg(ctx context.Context, cancel context.CancelFunc, firstReorgedBlock uint64) {
	// stop downloader
	cancel()

	// handle reorg
	attempts := 0
	for {
		err := d.processor.Reorg(ctx, firstReorgedBlock)
		if err != nil {
			attempts++
			d.log.Errorf(
				"error processing reorg, last valid Block %d, err: %v",
				firstReorgedBlock, err,
			)
			d.rh.Handle("handleReorg", attempts)
			continue
		}
		break
	}
	d.reorgSub.ReorgProcessed <- true
}
