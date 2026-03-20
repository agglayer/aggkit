package multidownloader

import (
	"context"
	"errors"
	"fmt"
	"sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkitsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type EVMDriver struct {
	processor            mdrsynctypes.ProcessorInterface
	downloader           mdrsynctypes.DownloaderInterface
	syncerConfig         aggkittypes.SyncerConfig
	rh                   *aggkitsync.RetryHandler
	logger               aggkitcommon.Logger
	compatibilityChecker compatibility.CompatibilityChecker
	// This mutext protect to:
	// - syncBlockChunkSize, because it can be updated dynamically by the user and read by the sync loop
	// - completionPercentage, because it can be updated by the downloader and read by the sync loop and by the API server
	mutex              sync.Mutex
	syncBlockChunkSize uint64
	// It's the percentage of completion of the download, it can be used to estimate the progress of the sync
	// can be nil is there are no information yet
	// 0 -> 0%, 100, -> 100%
	completionPercentage *float64
}

func NewEVMDriver(
	logger aggkitcommon.Logger,
	processor mdrsynctypes.ProcessorInterface,
	downloader mdrsynctypes.DownloaderInterface,
	syncerConfig aggkittypes.SyncerConfig,
	syncBlockChunkSize uint64,
	rh *aggkitsync.RetryHandler,
	compatibilityChecker compatibility.CompatibilityChecker,
) *EVMDriver {
	return &EVMDriver{
		processor:            processor,
		downloader:           downloader,
		syncerConfig:         syncerConfig,
		syncBlockChunkSize:   syncBlockChunkSize,
		rh:                   rh,
		logger:               logger,
		compatibilityChecker: compatibilityChecker,
	}
}
func (d *EVMDriver) checkFirstBlockNumberParams(firstBlockNumber *uint64) error {
	if firstBlockNumber == nil {
		return fmt.Errorf("multidownloader doesnt support firstBlockNumber==nil")
	}
	if *firstBlockNumber != d.syncerConfig.FromBlock {
		return fmt.Errorf("multidownloader doesnt support firstBlockNumber different than FromBlock, got %d, expected %d",
			*firstBlockNumber, d.syncerConfig.FromBlock)
	}
	return nil
}
func (d *EVMDriver) Sync(ctx context.Context, firstBlockNumber *uint64) {
	// firstBlockNumber is unused and not support in the current implementation
	// it just check that is equal to syncerConfig.InitialBlockNum
	if err := d.checkFirstBlockNumberParams(firstBlockNumber); err != nil {
		d.logger.Fatalf("error in firstBlockNumber parameter: %v", err)
	}
	attempts := 0
	for {
		if ctx.Err() != nil {
			d.logger.Info("context cancelled")
			return
		}
		if err := d.syncStep(ctx); err != nil {
			attempts++
			d.logger.Error("error during syncing ", err)
			d.rh.Handle(ctx, "Sync", attempts)
			continue
		}
	}
}

func (d *EVMDriver) syncStep(ctx context.Context) error {
	if d.compatibilityChecker != nil {
		if err := d.compatibilityChecker.Check(ctx, nil); err != nil {
			err := fmt.Errorf("Multidownloader_EVMDriver: error checking compatibility data between downloader (runtime)"+
				" and processor (db): %w", err)
			return err
		}
		d.compatibilityChecker = nil // only check once
	}

	lastBlockHeader, err := d.processor.GetLastProcessedBlockHeader(ctx)
	if err != nil {
		return fmt.Errorf("Multidownloader_EVMDriver: error getting last processed block from processor: %w", err)
	}
	d.logger.Debugf("Multidownloader_EVMDriver: starting sync from last processed block: %s", lastBlockHeader.Brief())
	blocks, err := d.downloader.DownloadNextBlocks(ctx,
		lastBlockHeader,
		d.syncBlockChunkSize,
		d.syncerConfig)

	if err != nil {
		switch {
		case mdrtypes.IsReorgedError(err):
			if reorgErr := d.handleReorg(ctx, mdrtypes.CastReorgedError(err)); reorgErr != nil {
				return fmt.Errorf("Multidownloader_EVMDriver: error handling reorg: %w", reorgErr)
			}
			// Reorg processed
			return nil
		case errors.Is(err, ErrLogsNotAvailable):
			d.logger.Debug("Multidownloader_EVMDriver: no logs available yet, waiting to retry")
			return nil
		default:
			return fmt.Errorf("Multidownloader_EVMDriver: error downloading blocks: %w", err)
		}
	}
	if err = d.processBlocks(ctx, blocks); err != nil {
		return fmt.Errorf("Multidownloader_EVMDriver: error processing blocks: %w", err)
	}
	if blocks != nil {
		LastProcessedBlock := blocks.Data.LastBlock()
		d.logger.Debugf("Multidownloader_EVMDriver: processed %d blocks, percent %.2f%% complete. LastBlock: %s",
			len(blocks.Data), blocks.CompletionPercentage, LastProcessedBlock.Brief())
	}
	return nil
}

func (d *EVMDriver) processBlocks(ctx context.Context, data *mdrsynctypes.DownloadResult) error {
	if data == nil || len(data.Data) == 0 {
		return nil
	}

	err := d.withRetry(ctx, "processBlocks", func() error {
		return d.processor.ProcessBlocks(ctx, data)
	})
	// If no error update percentage
	if err == nil {
		d.setCompletionPercentage(data.CompletionPercentage)
	}
	return err
}

func (d *EVMDriver) handleReorg(ctx context.Context, err *mdrtypes.ReorgedError) error {
	d.logger.Warnf("reorg detected: %s", err.Error())
	return d.withRetry(ctx, "handleReorg", func() error {
		return d.processor.Reorg(ctx, err.BlockRangeReorged.FromBlock)
	})
}

// withRetry is a helper wrapper function that invokes the fn callback on failed attempts
func (d *EVMDriver) withRetry(ctx context.Context, opName string, fn func() error) error {
	attempts := 0
	for {
		select {
		case <-ctx.Done():
			d.logger.Warnf("context canceled during %s", opName)
			return nil
		default:
			err := fn()
			if err != nil {
				attempts++
				d.logger.Errorf("error during %s (attempt %d): %v", opName, attempts, err)
				d.rh.Handle(ctx, opName, attempts)
			} else {
				return nil
			}
		}
	}
}

func (d *EVMDriver) GetCompletionPercentage() *float64 {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	// This is done to copy the value avoid passing internal
	// pointer.
	if d.completionPercentage == nil {
		return nil
	}
	percent := *d.completionPercentage
	return &percent
}

func (d *EVMDriver) setCompletionPercentage(percent float64) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.completionPercentage = &percent
}
