package types

import (
	"context"

	aggkittypes "github.com/agglayer/aggkit/types"
)

type ProcessorInterface interface {
	// GetLastProcessedBlockHeader it must return the last processed block header.
	// or nil if no block has been processed yet.
	// It is used to determine from which block number the downloader should start.
	GetLastProcessedBlockHeader(ctx context.Context) (*aggkittypes.BlockHeader, error)
	// ProcessBlocks processes the block. It is called for all blocks that are downloaded and
	// must be processed.
	// NOTE: legacy syncer use ProcessBlock for each block but it's slower because
	// can't take advantage of batch processing. ProcessBlocks is called with batches of blocks
	// and it is more efficient.
	//  It is the responsibility of the syncer to process them in batch or one by one.
	ProcessBlocks(ctx context.Context, blocks *DownloadResult) error
	// Reorg is called when a reorg is detected. Must execute a syncer reorg if apply
	// it's possible that the reorged blocks doesn't affect to this syncer
	Reorg(ctx context.Context, firstReorgedBlock uint64) error
}
