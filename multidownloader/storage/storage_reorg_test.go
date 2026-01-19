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
