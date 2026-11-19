package multidownloader

import (
	"context"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	aggkitsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// SingleBlockProcessor is a processor that stores the blocks one by one, each one in its own
// transaction. It is the interface that the processors of the legacy syncers implement.
type SingleBlockProcessor interface {
	// GetLastProcessedBlock returns the number of the last processed block, found is false if
	// no block has been processed yet
	GetLastProcessedBlock(ctx context.Context) (blockNum uint64, found bool, err error)
	// ProcessBlock stores the block and its events
	ProcessBlock(ctx context.Context, block aggkitsync.Block) error
	// Reorg removes the blocks >= firstReorgedBlock
	Reorg(ctx context.Context, firstReorgedBlock uint64) error
}

// BlockHashGetter returns the hash that the processor stores for the given block.
// found is false if the block is not stored or it has no hash.
type BlockHashGetter func(ctx context.Context, blockNum uint64) (hash common.Hash, found bool, err error)

var _ mdrsynctypes.ProcessorInterface = (*BlockByBlockProcessor)(nil)

// BlockByBlockProcessor adapts a SingleBlockProcessor to the processor that the multidownloader
// driver requires (mdrsynctypes.ProcessorInterface). It also notifies each block to the
// subscribers once it has been committed.
type BlockByBlockProcessor struct {
	processor       SingleBlockProcessor
	getBlockHash    BlockHashGetter
	blockSubscriber aggkitcommon.PubSub[aggkitsync.Block]
}

// NewBlockByBlockProcessor creates a BlockByBlockProcessor
func NewBlockByBlockProcessor(processor SingleBlockProcessor, getBlockHash BlockHashGetter) *BlockByBlockProcessor {
	return &BlockByBlockProcessor{
		processor:       processor,
		getBlockHash:    getBlockHash,
		blockSubscriber: aggkitcommon.NewGenericSubscriber[aggkitsync.Block](),
	}
}

// SubscribeToNewBlocks returns a channel that receives each block after it has been processed
func (p *BlockByBlockProcessor) SubscribeToNewBlocks(subscriberName string) <-chan aggkitsync.Block {
	return p.blockSubscriber.Subscribe(subscriberName)
}

// GetLastProcessedBlockHeader returns the header (number and hash) of the last processed block,
// or nil if no block has been processed yet.
// If the processor doesn't store the hash of that block the returned hash is the zero hash,
// that means that the block can't be checked for reorgs.
func (p *BlockByBlockProcessor) GetLastProcessedBlockHeader(ctx context.Context) (*aggkittypes.BlockHeader, error) {
	blockNum, found, err := p.processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	hash, found, err := p.getBlockHash(ctx, blockNum)
	if err != nil {
		return nil, fmt.Errorf("getting the hash of the last processed block %d: %w", blockNum, err)
	}
	if !found {
		hash = aggkitcommon.ZeroHash
	}
	return &aggkittypes.BlockHeader{Number: blockNum, Hash: hash}, nil
}

// ProcessBlocks processes the blocks one by one, each one is committed (and notified to the
// subscribers) before processing the next one. If it fails the previous blocks of the batch stay
// committed, so the blocks that are already processed are skipped: the same batch can be retried.
func (p *BlockByBlockProcessor) ProcessBlocks(ctx context.Context, blocks *mdrsynctypes.DownloadResult) error {
	if blocks == nil || len(blocks.Data) == 0 {
		return nil
	}
	lastProcessedBlock, found, err := p.processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return fmt.Errorf("getting last processed block: %w", err)
	}
	for _, b := range blocks.Data {
		if found && b.Num <= lastProcessedBlock {
			continue
		}
		block := aggkitsync.Block{
			Num:    b.Num,
			Hash:   b.Hash,
			Events: b.Events,
		}
		if err := p.processor.ProcessBlock(ctx, block); err != nil {
			return fmt.Errorf("processing block %d: %w", b.Num, err)
		}
		// Notify subscribers about the new block after successful processing
		p.blockSubscriber.Publish(block)
	}
	return nil
}

// Reorg removes the blocks >= firstReorgedBlock
func (p *BlockByBlockProcessor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	return p.processor.Reorg(ctx, firstReorgedBlock)
}

// StorageHeaderGetter returns the block header that the multidownloader stores and its finality
type StorageHeaderGetter interface {
	StorageHeaderByNumber(ctx context.Context,
		number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, bool, error)
}

// IsBlockFinalized reports whether the block with the given number and hash, as read from a syncer
// that is synced through the multidownloader, is finalized, i.e. it can no longer be reorged.
// It is the IsFinal flag the multidownloader stores, which is only set once the block was validated;
// the stored header must have the given hash: the multidownloader stores one block per number, so
// without the hash of the block the caller read, the flag could belong to the block that replaced it
// after a reorg.
// found is false when it can't be determined: no hash was given or the block is not stored (or not
// with that hash).
func IsBlockFinalized(ctx context.Context, storage StorageHeaderGetter, blockNum uint64,
	blockHash common.Hash) (finalized, found bool, err error) {
	if blockHash == (common.Hash{}) {
		return false, false, nil
	}
	header, isFinal, err := storage.StorageHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNum))
	if err != nil {
		return false, false, fmt.Errorf("failed to get block %d finality: %w", blockNum, err)
	}
	if header == nil || header.Hash != blockHash {
		return false, false, nil
	}
	return isFinal, true, nil
}
