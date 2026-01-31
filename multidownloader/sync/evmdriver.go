package multidownloader

import (
	"context"
	"errors"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type EVMDriver struct {
	processor    mdrsynctypes.ProcessorInterface
	downloader   mdrsynctypes.DownloaderInterface
	syncerConfig aggkittypes.SyncerConfig
	rh           *sync.RetryHandler
	logger       aggkitcommon.Logger

	syncBlockChunkSize uint64
}

func NewEVMDriver(processor mdrsynctypes.ProcessorInterface,
	downloader mdrsynctypes.DownloaderInterface,
	syncerConfig aggkittypes.SyncerConfig,
	syncBlockChunkSize uint64,
	rh *sync.RetryHandler,
	logger aggkitcommon.Logger) *EVMDriver {
	return &EVMDriver{
		processor:          processor,
		downloader:         downloader,
		syncerConfig:       syncerConfig,
		syncBlockChunkSize: syncBlockChunkSize,
		rh:                 rh,
		logger:             logger,
	}
}

func (d *EVMDriver) Sync(ctx context.Context) {
reset:
	// TODO: Add if err = d.compatibilityChecker.Check(ctx, nil); err != nil {
	for {
		if ctx.Err() != nil {
			d.logger.Info("context cancelled")
			return
		}
		lastBlockHeader := d.getLastProcessedBlock(ctx)
		if lastBlockHeader == nil {
			d.logger.Info("no last processed block found, starting from beginning")
		} else {
			d.logger.Infof("EVMDriver.Sync: starting sync from last processed block: %d", lastBlockHeader.Number)
		}

		blocks, err := d.downloader.DownloadNextBlocks(ctx,
			lastBlockHeader,
			d.syncBlockChunkSize,
			d.syncerConfig)
		if err != nil {
			d.logger.Error("error downloading next blocks: ", err)
		}
		if err != nil && mdrtypes.IsReorgedError(err) {
			err := d.handleReorg(ctx, mdrtypes.CastReorgedError(err))
			if err != nil {
				d.logger.Error("error handling reorg: ", err)
				d.rh.Handle(ctx, "Sync", 0)
				continue
			}
			goto reset
		}

		if err != nil && !errors.Is(err, ErrLogsNotAvailable) {
			d.logger.Error("error downloading next blocks: ", err)
			d.rh.Handle(ctx, "Sync", 0)
			continue
		}
		if errors.Is(err, ErrLogsNotAvailable) {
			// No logs available yet, wait and retry
			d.logger.Debugf("no logs available yet, waiting to retry")
			d.rh.Handle(ctx, "Sync", 0)
			continue
		}
		err = d.ProcessBlocks(ctx, blocks)
		if err != nil {
			d.logger.Error("error processing blocks: ", err)
			d.rh.Handle(ctx, "Sync", 0)
			continue
		}
	}
}

func (d *EVMDriver) ProcessBlocks(ctx context.Context, b *mdrsynctypes.DownloadResult) error {
	if b == nil || len(b.Data) == 0 {
		return nil
	}
	for _, block := range b.Data {
		err := d.processBlock(ctx, block)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *EVMDriver) processBlock(ctx context.Context, b *sync.EVMBlock) error {
	return d.withRetry(ctx, "processBlock", func() error {
		block := sync.Block{
			Num:    b.Num,
			Hash:   b.Hash,
			Events: b.Events,
		}
		return d.processor.ProcessBlock(ctx, block)
	})
}

func (d *EVMDriver) handleReorg(ctx context.Context, err *mdrtypes.ReorgedError) error {
	d.logger.Warnf("reorg detected: %s", err.Error())
	return d.withRetry(ctx, "handleReorg", func() error {
		return d.processor.Reorg(ctx, err.BlockRangeReorged.FromBlock)
	})
}

func (d *EVMDriver) getLastProcessedBlock(ctx context.Context) *aggkittypes.BlockHeader {
	attempts := 0
	for {
		// TODO: Case header == nil -> ?
		header, err := d.processor.GetLastProcessedBlockHeader(ctx)
		if err != nil {
			attempts++
			d.logger.Error("error getting last processed block: ", err)
			d.rh.Handle(ctx, "Sync", attempts)
			continue
		}
		return header
	}
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
