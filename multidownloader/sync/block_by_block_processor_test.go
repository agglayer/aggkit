package multidownloader

import (
	"context"
	"errors"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// fakeSingleBlockProcessor is an in-memory SingleBlockProcessor
type fakeSingleBlockProcessor struct {
	blocks        []sync.Block
	failOnBlock   uint64
	processErr    error
	lastBlockErr  error
	reorgedBlocks []uint64
}

func (f *fakeSingleBlockProcessor) GetLastProcessedBlock(context.Context) (uint64, bool, error) {
	if f.lastBlockErr != nil {
		return 0, false, f.lastBlockErr
	}
	if len(f.blocks) == 0 {
		return 0, false, nil
	}
	return f.blocks[len(f.blocks)-1].Num, true, nil
}

func (f *fakeSingleBlockProcessor) ProcessBlock(_ context.Context, block sync.Block) error {
	if f.processErr != nil && block.Num == f.failOnBlock {
		return f.processErr
	}
	f.blocks = append(f.blocks, block)
	return nil
}

func (f *fakeSingleBlockProcessor) Reorg(_ context.Context, firstReorgedBlock uint64) error {
	f.reorgedBlocks = append(f.reorgedBlocks, firstReorgedBlock)
	return nil
}

func (f *fakeSingleBlockProcessor) getBlockHash(_ context.Context, blockNum uint64) (common.Hash, bool, error) {
	for _, b := range f.blocks {
		if b.Num == blockNum && b.Hash != (common.Hash{}) {
			return b.Hash, true, nil
		}
	}
	return common.Hash{}, false, nil
}

func newBlocksResultForTest(blockNums ...uint64) *mdrsynctypes.DownloadResult {
	result := &mdrsynctypes.DownloadResult{}
	for _, num := range blockNums {
		result.Data = append(result.Data, &sync.EVMBlock{
			EVMBlockHeader: sync.EVMBlockHeader{Num: num, Hash: common.BigToHash(common.Big1)},
			Events:         []any{num},
		})
	}
	return result
}

func TestBlockByBlockProcessor_GetLastProcessedBlockHeader(t *testing.T) {
	ctx := t.Context()

	t.Run("no processed blocks", func(t *testing.T) {
		fake := &fakeSingleBlockProcessor{}
		header, err := NewBlockByBlockProcessor(fake, fake.getBlockHash).GetLastProcessedBlockHeader(ctx)
		require.NoError(t, err)
		require.Nil(t, header)
	})

	t.Run("block with hash", func(t *testing.T) {
		hash := common.HexToHash("0x1234")
		fake := &fakeSingleBlockProcessor{blocks: []sync.Block{{Num: 9}, {Num: 10, Hash: hash}}}
		header, err := NewBlockByBlockProcessor(fake, fake.getBlockHash).GetLastProcessedBlockHeader(ctx)
		require.NoError(t, err)
		require.Equal(t, &aggkittypes.BlockHeader{Number: 10, Hash: hash}, header)
	})

	t.Run("block without hash can't be checked for reorgs", func(t *testing.T) {
		fake := &fakeSingleBlockProcessor{blocks: []sync.Block{{Num: 10}}}
		header, err := NewBlockByBlockProcessor(fake, fake.getBlockHash).GetLastProcessedBlockHeader(ctx)
		require.NoError(t, err)
		require.Equal(t, &aggkittypes.BlockHeader{Number: 10, Hash: aggkitcommon.ZeroHash}, header)
	})

	t.Run("errors", func(t *testing.T) {
		expectedErr := errors.New("db error")
		fake := &fakeSingleBlockProcessor{lastBlockErr: expectedErr}
		_, err := NewBlockByBlockProcessor(fake, fake.getBlockHash).GetLastProcessedBlockHeader(ctx)
		require.ErrorIs(t, err, expectedErr)

		fake = &fakeSingleBlockProcessor{blocks: []sync.Block{{Num: 10}}}
		_, err = NewBlockByBlockProcessor(fake,
			func(context.Context, uint64) (common.Hash, bool, error) { return common.Hash{}, false, expectedErr },
		).GetLastProcessedBlockHeader(ctx)
		require.ErrorIs(t, err, expectedErr)
	})
}

func TestBlockByBlockProcessor_ProcessBlocks(t *testing.T) {
	ctx := t.Context()

	t.Run("nothing to process", func(t *testing.T) {
		fake := &fakeSingleBlockProcessor{lastBlockErr: errors.New("it must not be called")}
		processor := NewBlockByBlockProcessor(fake, fake.getBlockHash)
		require.NoError(t, processor.ProcessBlocks(ctx, nil))
		require.NoError(t, processor.ProcessBlocks(ctx, &mdrsynctypes.DownloadResult{}))
	})

	t.Run("blocks are processed in order and notified once committed", func(t *testing.T) {
		fake := &fakeSingleBlockProcessor{}
		processor := NewBlockByBlockProcessor(fake, fake.getBlockHash)
		blocksCh := processor.SubscribeToNewBlocks("test")

		require.NoError(t, processor.ProcessBlocks(ctx, newBlocksResultForTest(10, 11)))

		require.Len(t, fake.blocks, 2)
		for _, expectedNum := range []uint64{10, 11} {
			select {
			case block := <-blocksCh:
				require.Equal(t, expectedNum, block.Num)
				require.Equal(t, []any{expectedNum}, block.Events)
			case <-time.After(5 * time.Second):
				require.Fail(t, "block not notified")
			}
		}
	})

	t.Run("a failed batch can be retried: the committed blocks are skipped", func(t *testing.T) {
		expectedErr := errors.New("process error")
		fake := &fakeSingleBlockProcessor{failOnBlock: 11, processErr: expectedErr}
		processor := NewBlockByBlockProcessor(fake, fake.getBlockHash)
		batch := newBlocksResultForTest(10, 11, 12)

		require.ErrorIs(t, processor.ProcessBlocks(ctx, batch), expectedErr)
		require.Len(t, fake.blocks, 1)

		fake.processErr = nil
		require.NoError(t, processor.ProcessBlocks(ctx, batch))
		require.Len(t, fake.blocks, 3)
		require.Equal(t, uint64(10), fake.blocks[0].Num)
		require.Equal(t, uint64(11), fake.blocks[1].Num)
		require.Equal(t, uint64(12), fake.blocks[2].Num)
	})

	t.Run("error getting the last processed block", func(t *testing.T) {
		expectedErr := errors.New("db error")
		fake := &fakeSingleBlockProcessor{lastBlockErr: expectedErr}
		processor := NewBlockByBlockProcessor(fake, fake.getBlockHash)
		require.ErrorIs(t, processor.ProcessBlocks(ctx, newBlocksResultForTest(10)), expectedErr)
	})
}

func TestBlockByBlockProcessor_Reorg(t *testing.T) {
	fake := &fakeSingleBlockProcessor{}
	require.NoError(t, NewBlockByBlockProcessor(fake, fake.getBlockHash).Reorg(t.Context(), 15))
	require.Equal(t, []uint64{15}, fake.reorgedBlocks)
}

type fakeStorageHeaderGetter struct {
	header  *aggkittypes.BlockHeader
	isFinal bool
	err     error
}

func (f *fakeStorageHeaderGetter) StorageHeaderByNumber(context.Context,
	*aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, bool, error) {
	return f.header, f.isFinal, f.err
}

func TestIsBlockFinalized(t *testing.T) {
	hash := common.HexToHash("0x1234")
	otherHash := common.HexToHash("0x5678")
	expectedErr := errors.New("storage error")
	tests := []struct {
		name              string
		storage           *fakeStorageHeaderGetter
		blockHash         common.Hash
		expectedFinalized bool
		expectedFound     bool
		expectedErr       error
	}{
		{name: "no hash given", storage: &fakeStorageHeaderGetter{err: expectedErr}},
		{name: "storage error", storage: &fakeStorageHeaderGetter{err: expectedErr}, blockHash: hash,
			expectedErr: expectedErr},
		{name: "block not stored", storage: &fakeStorageHeaderGetter{}, blockHash: hash},
		{name: "block stored with another hash", blockHash: hash,
			storage: &fakeStorageHeaderGetter{header: &aggkittypes.BlockHeader{Hash: otherHash}, isFinal: true}},
		{name: "block not finalized", blockHash: hash, expectedFound: true,
			storage: &fakeStorageHeaderGetter{header: &aggkittypes.BlockHeader{Hash: hash}}},
		{name: "block finalized", blockHash: hash, expectedFound: true, expectedFinalized: true,
			storage: &fakeStorageHeaderGetter{header: &aggkittypes.BlockHeader{Hash: hash}, isFinal: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			finalized, found, err := IsBlockFinalized(t.Context(), tt.storage, 10, tt.blockHash)
			if tt.expectedErr != nil {
				require.ErrorIs(t, err, tt.expectedErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expectedFinalized, finalized)
			require.Equal(t, tt.expectedFound, found)
		})
	}
}
