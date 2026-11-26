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

// BlockBatchFunc commits an ordered batch of blocks atomically in a single database transaction.
// It is the optional batch capability the native multidownloader driver can use through
// NewBlockByBlockProcessorWithBatch. It must be bounded to aggkitsync.MaxBatchBlocks and preserve
// the per-block semantics of SingleBlockProcessor.ProcessBlock.
type BlockBatchFunc func(ctx context.Context, blocks []aggkitsync.Block) error

var _ mdrsynctypes.ProcessorInterface = (*BlockByBlockProcessor)(nil)

// BlockByBlockProcessor adapts a SingleBlockProcessor to the processor that the multidownloader
// driver requires (mdrsynctypes.ProcessorInterface). It also notifies each block to the
// subscribers once it has been committed.
type BlockByBlockProcessor struct {
	processor       SingleBlockProcessor
	getBlockHash    BlockHashGetter
	blockSubscriber aggkitcommon.PubSub[aggkitsync.Block]
	// batch, when non-nil, commits an ordered chunk of the downloaded blocks atomically
	// instead of one block per transaction. When nil the processor keeps the legacy
	// one-block-at-a-time path.
	batch BlockBatchFunc
}

// NewBlockByBlockProcessor creates a BlockByBlockProcessor that stores the blocks one by one, each
// one in its own transaction.
func NewBlockByBlockProcessor(processor SingleBlockProcessor, getBlockHash BlockHashGetter) *BlockByBlockProcessor {
	return NewBlockByBlockProcessorWithBatch(processor, getBlockHash, nil)
}

// NewBlockByBlockProcessorWithBatch creates a BlockByBlockProcessor that commits the downloaded
// blocks atomically through batch, in chunks of at most aggkitsync.MaxBatchBlocks. A nil batch
// behaves exactly like NewBlockByBlockProcessor (one block per transaction).
func NewBlockByBlockProcessorWithBatch(processor SingleBlockProcessor, getBlockHash BlockHashGetter,
	batch BlockBatchFunc) *BlockByBlockProcessor {
	return &BlockByBlockProcessor{
		processor:       processor,
		getBlockHash:    getBlockHash,
		blockSubscriber: aggkitcommon.NewGenericSubscriber[aggkitsync.Block](),
		batch:           batch,
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

// ProcessBlocks processes the downloaded blocks, skipping the ones that are already processed. Each
// block is notified to the subscribers only after it has been committed.
//
// Without a batch callback each block is committed (and notified) before processing the next one: if
// it fails the previous blocks stay committed, so the blocks that are already processed are skipped
// and the same batch can be retried.
//
// With a batch callback the remaining blocks are split into chunks of at most
// aggkitsync.MaxBatchBlocks and each chunk is committed atomically before the next one is attempted.
// It never waits to fill a chunk, it only splits the already-downloaded blocks. A chunk that fails is
// not published and a retry of the same DownloadResult skips the chunks already committed through
// the last processed block.
func (p *BlockByBlockProcessor) ProcessBlocks(ctx context.Context, blocks *mdrsynctypes.DownloadResult) error {
	if blocks == nil || len(blocks.Data) == 0 {
		return nil
	}
	lastProcessedBlock, found, err := p.processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return fmt.Errorf("getting last processed block: %w", err)
	}
	pending := make([]aggkitsync.Block, 0, len(blocks.Data))
	for _, b := range blocks.Data {
		if found && b.Num <= lastProcessedBlock {
			continue
		}
		pending = append(pending, aggkitsync.Block{
			Num:    b.Num,
			Hash:   b.Hash,
			Events: b.Events,
		})
	}
	if p.batch == nil {
		for _, block := range pending {
			if err := p.processor.ProcessBlock(ctx, block); err != nil {
				return fmt.Errorf("processing block %d: %w", block.Num, err)
			}
			// Notify subscribers about the new block after successful processing
			p.blockSubscriber.Publish(block)
		}
		return nil
	}
	for start := 0; start < len(pending); start += aggkitsync.MaxBatchBlocks {
		end := min(start+aggkitsync.MaxBatchBlocks, len(pending))
		chunk := pending[start:end]
		if err := p.batch(ctx, chunk); err != nil {
			return fmt.Errorf("processing batch of %d blocks (first %d): %w", len(chunk), chunk[0].Num, err)
		}
		// Notify subscribers about each block only after the chunk has been committed
		for _, block := range chunk {
			p.blockSubscriber.Publish(block)
		}
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
