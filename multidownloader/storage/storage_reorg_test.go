package storage

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func TestStorage_InsertNewReorg(t *testing.T) {
	storage := newStorageForTest(t, nil)
	reorgData := mdrtypes.ReorgData{
		ChainID:                   1,
		BlockRangeAffected:        aggkitcommon.NewBlockRange(5000, 5010),
		DetectedAtBlock:           5020,
		DetectedTimestamp:         1630003000,
		NetworkLatestBlock:        6000,
		NetworkFinalizedBlock:     5990,
		NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
	}
	tx, err := storage.NewTx(t.Context())
	require.NoError(t, err, "cannot start new transaction")
	chainID, err := storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
	require.NoError(t, err, "cannot insert new reorg")
	require.Equal(t, uint64(1), chainID, "first chain ID must be 1")
	err = tx.Commit()
	require.NoError(t, err, "cannot commit transaction")

	tx, err = storage.NewTx(t.Context())
	require.NoError(t, err, "cannot start new transaction")
	chainID, err = storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
	require.NoError(t, err, "cannot insert new reorg")
	require.Equal(t, uint64(2), chainID, "second chain ID must be 2")
	err = tx.Commit()
	require.NoError(t, err, "cannot commit transaction")
}

func TestStorage_InsertNewReorgAndMoveBlocks(t *testing.T) {
	storage := newStorageForTest(t, nil)
	populateLogsAndBlocksForTest(t, storage,
		5000, 20, 5)

	reorgData := mdrtypes.ReorgData{
		ChainID:                   0, // will be set by InsertNewReorg
		BlockRangeAffected:        aggkitcommon.NewBlockRange(5005, 5015),
		DetectedAtBlock:           5020,
		DetectedTimestamp:         1630003000,
		NetworkLatestBlock:        6000,
		NetworkFinalizedBlock:     5990,
		NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
	}
	tx, err := storage.NewTx(t.Context())
	require.NoError(t, err, "cannot start new transaction")
	chainID, err := storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
	require.NoError(t, err, "cannot insert new reorg")
	require.Equal(t, uint64(1), chainID, "first chain ID must be 1")
	err = tx.Commit()
	require.NoError(t, err, "cannot commit transaction")
	// Now check that blocks from 5005 to 5015 are in block_reorged
	for i := uint64(5005); i <= 5015; i++ {
		hdr, _, err := storage.GetBlockHeaderByNumber(nil, i)
		require.NoError(t, err)
		require.Nil(t, hdr, "block header should not be in blocks table anymore")
	}
}

func TestStorage_GetBlockReorgedChainID_MultipleChains(t *testing.T) {
	t.Run("returns chain_id with lowest reorged_from_block when block exists in multiple chains", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		// First, populate some blocks that will be reorged
		populateLogsAndBlocksForTest(t, storage, 1000, 50, 2)

		// Create first reorg with reorged_from_block=1010
		reorgData1 := mdrtypes.ReorgData{
			BlockRangeAffected:        aggkitcommon.NewBlockRange(1010, 1020),
			DetectedAtBlock:           1025,
			DetectedTimestamp:         1630003000,
			NetworkLatestBlock:        2000,
			NetworkFinalizedBlock:     1990,
			NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
			Description:               "First reorg",
		}

		tx1, err := storage.NewTx(t.Context())
		require.NoError(t, err)
		chainID1, err := storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx1, reorgData1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), chainID1)
		err = tx1.Commit()
		require.NoError(t, err)

		// Create second reorg with reorged_from_block=1005 (lower than first)
		reorgData2 := mdrtypes.ReorgData{
			BlockRangeAffected:        aggkitcommon.NewBlockRange(1005, 1009),
			DetectedAtBlock:           1030,
			DetectedTimestamp:         1630004000,
			NetworkLatestBlock:        2100,
			NetworkFinalizedBlock:     2090,
			NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
			Description:               "Second reorg",
		}

		tx2, err := storage.NewTx(t.Context())
		require.NoError(t, err)
		chainID2, err := storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx2, reorgData2)
		require.NoError(t, err)
		require.Equal(t, uint64(2), chainID2)
		err = tx2.Commit()
		require.NoError(t, err)

		// The key test: insert the SAME block_number and block_hash into MULTIPLE chains
		// This is the scenario the user wants to test - when a block exists in multiple reorg chains,
		// the function should return the chain_id with the lowest reorged_from_block
		testBlockNumber := uint64(2000) // Use a block number outside the reorg ranges
		testBlockHash := exampleTestHash[7]

		tx3, err := storage.NewTx(t.Context())
		require.NoError(t, err)

		// Insert the SAME block into chain 1 (reorged_from_block=1010)
		_, err = tx3.Exec(`INSERT INTO blocks_reorged (chain_id, block_number, block_hash, block_parent_hash, block_timestamp)
			VALUES (?, ?, ?, ?, ?)`, chainID1, testBlockNumber, testBlockHash.Hex(), exampleTestHash[4].Hex(), 1630000000)
		require.NoError(t, err)

		// Insert the SAME block into chain 2 (reorged_from_block=1005, lower!)
		_, err = tx3.Exec(`INSERT INTO blocks_reorged (chain_id, block_number, block_hash, block_parent_hash, block_timestamp)
			VALUES (?, ?, ?, ?, ?)`, chainID2, testBlockNumber, testBlockHash.Hex(), exampleTestHash[4].Hex(), 1630000000)
		require.NoError(t, err)

		err = tx3.Commit()
		require.NoError(t, err)

		// Query for the block - should return chainID2 since it has the lowest reorged_from_block (1005 < 1010)
		returnedChainID, found, err := storage.GetBlockReorgedChainID(nil, testBlockNumber, testBlockHash)
		require.NoError(t, err)
		require.True(t, found, "block should be found")
		require.Equal(t, chainID2, returnedChainID, "should return chain_id with lowest reorged_from_block (chain 2 with reorged_from_block=1005)")

		// Verify the reorged_from_block values to confirm our expectation
		reorgData1Retrieved, err := storage.GetReorgedDataByChainID(nil, chainID1)
		require.NoError(t, err)
		require.Equal(t, uint64(1010), reorgData1Retrieved.BlockRangeAffected.FromBlock)

		reorgData2Retrieved, err := storage.GetReorgedDataByChainID(nil, chainID2)
		require.NoError(t, err)
		require.Equal(t, uint64(1005), reorgData2Retrieved.BlockRangeAffected.FromBlock)
	})

	t.Run("returns false when block not found in any chain", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		// Query for non-existent block
		chainID, found, err := storage.GetBlockReorgedChainID(nil, 9999, exampleTestHash[0])
		require.NoError(t, err)
		require.False(t, found, "block should not be found")
		require.Equal(t, uint64(0), chainID)
	})
}

func TestStorage_GetReorgedDataByChainID(t *testing.T) {
	t.Run("returns reorg data when found", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		// Insert a reorg
		expectedReorgData := mdrtypes.ReorgData{
			ChainID:                   0, // will be set by InsertNewReorg
			BlockRangeAffected:        aggkitcommon.NewBlockRange(1000, 1010),
			DetectedAtBlock:           1020,
			DetectedTimestamp:         1630003000,
			NetworkLatestBlock:        2000,
			NetworkFinalizedBlock:     1990,
			NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
		}

		tx, err := storage.NewTx(t.Context())
		require.NoError(t, err)
		chainID, err := storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, expectedReorgData)
		require.NoError(t, err)
		require.Equal(t, uint64(1), chainID)
		err = tx.Commit()
		require.NoError(t, err)

		// Retrieve the reorg data
		reorgData, err := storage.GetReorgedDataByChainID(nil, chainID)
		require.NoError(t, err)
		require.NotNil(t, reorgData, "reorg data should not be nil when found")
		require.Equal(t, chainID, reorgData.ChainID)
		require.Equal(t, expectedReorgData.BlockRangeAffected, reorgData.BlockRangeAffected)
		require.Equal(t, expectedReorgData.DetectedAtBlock, reorgData.DetectedAtBlock)
		require.Equal(t, expectedReorgData.DetectedTimestamp, reorgData.DetectedTimestamp)
		require.Equal(t, expectedReorgData.NetworkLatestBlock, reorgData.NetworkLatestBlock)
		require.Equal(t, expectedReorgData.NetworkFinalizedBlock, reorgData.NetworkFinalizedBlock)
		require.Equal(t, expectedReorgData.NetworkFinalizedBlockName, reorgData.NetworkFinalizedBlockName)
	})

	t.Run("returns nil when chainID not found", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		// Try to retrieve a non-existent chainID
		reorgData, err := storage.GetReorgedDataByChainID(nil, 999)
		require.NoError(t, err, "should not return error when chainID not found")
		require.Nil(t, reorgData, "reorg data should be nil when not found")
	})

	t.Run("returns correct data for multiple reorgs", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		// Insert multiple reorgs
		reorgData1 := mdrtypes.ReorgData{
			BlockRangeAffected:        aggkitcommon.NewBlockRange(1000, 1010),
			DetectedAtBlock:           1020,
			DetectedTimestamp:         1630003000,
			NetworkLatestBlock:        2000,
			NetworkFinalizedBlock:     1990,
			NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
		}

		reorgData2 := mdrtypes.ReorgData{
			BlockRangeAffected:        aggkitcommon.NewBlockRange(2000, 2020),
			DetectedAtBlock:           2030,
			DetectedTimestamp:         1630004000,
			NetworkLatestBlock:        3000,
			NetworkFinalizedBlock:     2990,
			NetworkFinalizedBlockName: aggkittypes.SafeBlock,
		}

		tx1, err := storage.NewTx(t.Context())
		require.NoError(t, err)
		chainID1, err := storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx1, reorgData1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), chainID1)
		err = tx1.Commit()
		require.NoError(t, err)

		tx2, err := storage.NewTx(t.Context())
		require.NoError(t, err)
		chainID2, err := storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx2, reorgData2)
		require.NoError(t, err)
		require.Equal(t, uint64(2), chainID2)
		err = tx2.Commit()
		require.NoError(t, err)

		// Retrieve first reorg
		retrieved1, err := storage.GetReorgedDataByChainID(nil, chainID1)
		require.NoError(t, err)
		require.NotNil(t, retrieved1)
		require.Equal(t, chainID1, retrieved1.ChainID)
		require.Equal(t, reorgData1.BlockRangeAffected, retrieved1.BlockRangeAffected)
		require.Equal(t, reorgData1.NetworkFinalizedBlockName, retrieved1.NetworkFinalizedBlockName)

		// Retrieve second reorg
		retrieved2, err := storage.GetReorgedDataByChainID(nil, chainID2)
		require.NoError(t, err)
		require.NotNil(t, retrieved2)
		require.Equal(t, chainID2, retrieved2.ChainID)
		require.Equal(t, reorgData2.BlockRangeAffected, retrieved2.BlockRangeAffected)
		require.Equal(t, reorgData2.NetworkFinalizedBlockName, retrieved2.NetworkFinalizedBlockName)
	})
}
