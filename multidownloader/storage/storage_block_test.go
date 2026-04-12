package storage

import (
	"context"
	"testing"

	mdtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func TestStorage_GetBlock(t *testing.T) {
	storage := newStorageForTest(t, nil)
	// BlockBase not present
	blockHeader, _, err := storage.GetBlockHeaderByNumber(nil, 1234)
	require.NoError(t, err, "cannot get BlockHeader")
	require.Nil(t, blockHeader, "expected nil BlockHeader")
	block := aggkittypes.NewBlockHeader(1234, exampleTestHash[0], 5678, &exampleTestHash[1])
	err = storage.saveAggkitBlock(nil, block, true)
	require.NoError(t, err, "cannot insert BlockHeader")
	// Get and verify block
	readBlock, isFinal, err := storage.GetBlockHeaderByNumber(nil, 1234)
	require.NoError(t, err, "cannot get BlockHeader")
	require.NotNil(t, readBlock, "expected non-nil BlockHeader")
	require.Equal(t, block, readBlock, "BlockHeader mismatch")
	require.True(t, isFinal, "expected block to be final")

	blockNilParentHash := aggkittypes.NewBlockHeader(1235, exampleTestHash[0], 5678, nil)
	err = storage.saveAggkitBlock(nil, blockNilParentHash, true)
	require.NoError(t, err, "cannot get BlockHeader")
	readBlock, _, err = storage.GetBlockHeaderByNumber(nil, blockNilParentHash.Number)
	require.NoError(t, err, "cannot get BlockHeader")
	require.Equal(t, blockNilParentHash, readBlock, "BlockHeader mismatch")
}

func TestStorage_GetRangeBlockHeader(t *testing.T) {
	t.Run("returns same block when only one block exists", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		block := aggkittypes.NewBlockHeader(4000, exampleTestHash[5], 1630002000, nil)
		err := storage.saveAggkitBlock(nil, block, mdtypes.NotFinalized)
		require.NoError(t, err, "cannot insert BlockHeader")

		lowest, highest, err := storage.GetRangeBlockHeader(nil, mdtypes.NotFinalized)
		require.NoError(t, err, "cannot get range BlockHeader")
		require.Equal(t, block, lowest, "lowest BlockHeader mismatch")
		require.Equal(t, block, highest, "highest BlockHeader mismatch")
	})

	t.Run("returns nil when no blocks exist", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		lowest, highest, err := storage.GetRangeBlockHeader(nil, mdtypes.Finalized)
		require.NoError(t, err, "cannot get range BlockHeader")
		require.Nil(t, lowest, "expected nil lowest BlockHeader")
		require.Nil(t, highest, "expected nil highest BlockHeader")
	})

	t.Run("returns correct lowest and highest when multiple blocks exist", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		// Insert multiple non-finalized blocks in non-sequential order
		block1 := aggkittypes.NewBlockHeader(2000, exampleTestHash[0], 1630001000, nil)
		err := storage.saveAggkitBlock(nil, block1, mdtypes.NotFinalized)
		require.NoError(t, err)

		block2 := aggkittypes.NewBlockHeader(1000, exampleTestHash[1], 1630000000, nil)
		err = storage.saveAggkitBlock(nil, block2, mdtypes.NotFinalized)
		require.NoError(t, err)

		block3 := aggkittypes.NewBlockHeader(3000, exampleTestHash[2], 1630002000, nil)
		err = storage.saveAggkitBlock(nil, block3, mdtypes.NotFinalized)
		require.NoError(t, err)

		lowest, highest, err := storage.GetRangeBlockHeader(nil, mdtypes.NotFinalized)
		require.NoError(t, err, "cannot get range BlockHeader")
		require.NotNil(t, lowest)
		require.NotNil(t, highest)
		require.Equal(t, uint64(1000), lowest.Number, "lowest should be block 1000")
		require.Equal(t, uint64(3000), highest.Number, "highest should be block 3000")
		require.Equal(t, block2, lowest, "lowest BlockHeader mismatch")
		require.Equal(t, block3, highest, "highest BlockHeader mismatch")
	})

	t.Run("filters by finality type correctly", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		// Insert finalized blocks
		finalizedBlock1 := aggkittypes.NewBlockHeader(500, exampleTestHash[3], 1629999000, nil)
		err := storage.saveAggkitBlock(nil, finalizedBlock1, mdtypes.Finalized)
		require.NoError(t, err)

		finalizedBlock2 := aggkittypes.NewBlockHeader(1500, exampleTestHash[4], 1630000500, nil)
		err = storage.saveAggkitBlock(nil, finalizedBlock2, mdtypes.Finalized)
		require.NoError(t, err)

		// Insert non-finalized blocks
		notFinalizedBlock := aggkittypes.NewBlockHeader(2500, exampleTestHash[5], 1630001500, nil)
		err = storage.saveAggkitBlock(nil, notFinalizedBlock, mdtypes.NotFinalized)
		require.NoError(t, err)

		// Get finalized range
		lowest, highest, err := storage.GetRangeBlockHeader(nil, mdtypes.Finalized)
		require.NoError(t, err)
		require.NotNil(t, lowest)
		require.NotNil(t, highest)
		require.Equal(t, uint64(500), lowest.Number, "lowest finalized should be block 500")
		require.Equal(t, uint64(1500), highest.Number, "highest finalized should be block 1500")

		// Get non-finalized range
		lowest, highest, err = storage.GetRangeBlockHeader(nil, mdtypes.NotFinalized)
		require.NoError(t, err)
		require.NotNil(t, lowest)
		require.NotNil(t, highest)
		require.Equal(t, uint64(2500), lowest.Number, "should only return non-finalized block")
		require.Equal(t, uint64(2500), highest.Number, "should only return non-finalized block")
	})
}

func TestStorage_GetHighestBlockNumber(t *testing.T) {
	t.Run("returns 0 when no blocks exist", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		highestBlockNum, err := storage.GetHighestBlockNumber(nil)

		require.NoError(t, err)
		require.Equal(t, uint64(0), highestBlockNum)
	})

	t.Run("returns highest block number when blocks exist", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		// Insert multiple blocks
		block1 := aggkittypes.NewBlockHeader(1000, exampleTestHash[0], 1630000000, nil)
		err := storage.saveAggkitBlock(nil, block1, true)
		require.NoError(t, err)

		block2 := aggkittypes.NewBlockHeader(2000, exampleTestHash[1], 1630001000, nil)
		err = storage.saveAggkitBlock(nil, block2, false)
		require.NoError(t, err)

		block3 := aggkittypes.NewBlockHeader(1500, exampleTestHash[2], 1630000500, nil)
		err = storage.saveAggkitBlock(nil, block3, true)
		require.NoError(t, err)

		highestBlockNum, err := storage.GetHighestBlockNumber(nil)

		require.NoError(t, err)
		require.Equal(t, uint64(2000), highestBlockNum, "expected highest block number to be 2000")
	})
}

func TestStorage_GetBlockHeadersNotFinalized(t *testing.T) {
	t.Run("returns empty list when no non-finalized blocks exist", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		blocks, err := storage.GetBlockHeadersNotFinalized(nil, nil)

		require.NoError(t, err)
		require.Empty(t, blocks)
	})

	t.Run("returns all non-finalized blocks when maxBlock is nil", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		// Insert finalized blocks
		block1 := aggkittypes.NewBlockHeader(1000, exampleTestHash[0], 1630000000, nil)
		err := storage.saveAggkitBlock(nil, block1, true)
		require.NoError(t, err)

		// Insert non-finalized blocks
		block2 := aggkittypes.NewBlockHeader(2000, exampleTestHash[1], 1630001000, nil)
		err = storage.saveAggkitBlock(nil, block2, false)
		require.NoError(t, err)

		block3 := aggkittypes.NewBlockHeader(3000, exampleTestHash[2], 1630002000, nil)
		err = storage.saveAggkitBlock(nil, block3, false)
		require.NoError(t, err)

		blocks, err := storage.GetBlockHeadersNotFinalized(nil, nil)

		require.NoError(t, err)
		require.Len(t, blocks, 2, "expected 2 non-finalized blocks")
	})

	t.Run("returns non-finalized blocks up to maxBlock", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		ctx := context.TODO()
		tx, err := storage.NewTx(ctx)
		require.NoError(t, err)
		defer func() {
			_ = tx.Rollback()
		}()
		// Insert non-finalized blocks
		block1 := aggkittypes.NewBlockHeader(1000, exampleTestHash[0], 1630000000, nil)
		err = storage.saveAggkitBlock(tx, block1, false)
		require.NoError(t, err)

		block2 := aggkittypes.NewBlockHeader(2000, exampleTestHash[1], 1630001000, nil)
		err = storage.saveAggkitBlock(tx, block2, false)
		require.NoError(t, err)

		block3 := aggkittypes.NewBlockHeader(3000, exampleTestHash[2], 1630002000, nil)
		err = storage.saveAggkitBlock(tx, block3, false)
		require.NoError(t, err)

		maxBlock := uint64(2500)
		blocks, err := storage.GetBlockHeadersNotFinalized(tx, &maxBlock)

		require.NoError(t, err)
		require.Len(t, blocks, 2, "expected 2 non-finalized blocks <= 2500")
		// Verify that block 3000 is not included
		for _, block := range blocks {
			require.LessOrEqual(t, block.Number, maxBlock, "block number should be <= maxBlock")
		}
	})
}

func TestBlocks_Add(t *testing.T) {
	blocks := NewBlocks()
	require.True(t, blocks.IsEmpty())

	header := aggkittypes.NewBlockHeader(1000, exampleTestHash[0], 1630000000, nil)
	blocks.Add(header, true)

	require.False(t, blocks.IsEmpty())
	require.Equal(t, 1, blocks.Len())
	require.Contains(t, blocks.Headers, header.Number)
	require.True(t, blocks.AreFinal[header.Number])
}

func TestBlocks_Get(t *testing.T) {
	t.Run("returns header and finality when exists", func(t *testing.T) {
		blocks := NewBlocks()
		header := aggkittypes.NewBlockHeader(1000, exampleTestHash[0], 1630000000, nil)
		blocks.Add(header, true)

		retrievedHeader, isFinal, err := blocks.Get(1000)

		require.NoError(t, err)
		require.Equal(t, header, retrievedHeader)
		require.True(t, isFinal)
	})

	t.Run("returns error when header not found", func(t *testing.T) {
		blocks := NewBlocks()

		retrievedHeader, isFinal, err := blocks.Get(9999)

		require.Error(t, err)
		require.Contains(t, err.Error(), "block header not found")
		require.Nil(t, retrievedHeader)
		require.False(t, isFinal)
	})
}

func TestBlocks_ListHeaders(t *testing.T) {
	blocks := NewBlocks()

	header1 := aggkittypes.NewBlockHeader(1000, exampleTestHash[0], 1630000000, nil)
	header2 := aggkittypes.NewBlockHeader(2000, exampleTestHash[1], 1630001000, nil)
	header3 := aggkittypes.NewBlockHeader(3000, exampleTestHash[2], 1630002000, nil)

	blocks.Add(header1, true)
	blocks.Add(header2, false)
	blocks.Add(header3, true)

	headers := blocks.ListHeaders()

	require.Len(t, headers, 3)
	// Verify all headers are present (order may vary since it's from a map)
	headerNumbers := make(map[uint64]bool)
	for _, h := range headers {
		headerNumbers[h.Number] = true
	}
	require.True(t, headerNumbers[1000])
	require.True(t, headerNumbers[2000])
	require.True(t, headerNumbers[3000])
}

func TestBlocks_IsEmpty(t *testing.T) {
	blocks := NewBlocks()
	require.True(t, blocks.IsEmpty())

	header := aggkittypes.NewBlockHeader(1000, exampleTestHash[0], 1630000000, nil)
	blocks.Add(header, true)
	require.False(t, blocks.IsEmpty())
}

func TestBlocks_Len(t *testing.T) {
	blocks := NewBlocks()
	require.Equal(t, 0, blocks.Len())

	blocks.Add(aggkittypes.NewBlockHeader(1000, exampleTestHash[0], 1630000000, nil), true)
	require.Equal(t, 1, blocks.Len())

	blocks.Add(aggkittypes.NewBlockHeader(2000, exampleTestHash[1], 1630001000, nil), false)
	require.Equal(t, 2, blocks.Len())
}
