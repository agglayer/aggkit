package multidownloader

import (
	"context"
	"fmt"
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEVMMultidownloader_CheckValidBlock(t *testing.T) {
	t.Run("returns true when block is found and hash matches", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		blockNumber := uint64(100)
		blockHash := common.HexToHash("0x1234")

		storedBlock := &aggkittypes.BlockHeader{
			Number: blockNumber,
			Hash:   blockHash,
		}

		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, blockNumber).
			Return(storedBlock, mdrtypes.Finalized, nil).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(context.Background(), blockNumber, blockHash)

		require.NoError(t, err)
		require.True(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})

	t.Run("returns error when GetBlockHeaderByNumber fails", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		blockNumber := uint64(100)
		blockHash := common.HexToHash("0x1234")

		expectedErr := fmt.Errorf("database error")
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, blockNumber).
			Return(nil, mdrtypes.NotFinalized, expectedErr).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(context.Background(), blockNumber, blockHash)

		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get BlockHeader")
		require.True(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})

	t.Run("returns false with reorgID when block found in blocks_reorged", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		blockNumber := uint64(100)
		blockHash := common.HexToHash("0x1234")
		expectedReorgID := uint64(42)

		storedBlock := &aggkittypes.BlockHeader{
			Number: blockNumber,
			Hash:   common.HexToHash("0x5678"), // Different hash
		}

		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, blockNumber).
			Return(storedBlock, mdrtypes.Finalized, nil).Once()
		testData.mockStorage.EXPECT().GetBlockReorgedReorgID(mock.Anything, blockNumber, blockHash).
			Return(expectedReorgID, true, nil).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(context.Background(), blockNumber, blockHash)

		require.NoError(t, err)
		require.False(t, isValid)
		require.Equal(t, expectedReorgID, reorgID)
	})

	t.Run("returns false when block not stored, not in blocks_reorged and above finalized", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		blockNumber := uint64(100)
		blockHash := common.HexToHash("0x1234")

		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, blockNumber).
			Return(nil, mdrtypes.NotFinalized, nil).Once()
		testData.mockStorage.EXPECT().GetBlockReorgedReorgID(mock.Anything, blockNumber, blockHash).
			Return(uint64(0), false, nil).Once()
		// Block is above finalized -> not stable, cannot validate against L1, stays an error.
		testData.mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, mock.Anything).Return(blockNumber-1, nil).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(context.Background(), blockNumber, blockHash)

		require.Error(t, err)
		require.Contains(t, err.Error(), "not found in storage or blocks_reorged")
		require.False(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})

	t.Run("returns error (no L1 fallback) when a different block is stored and not in blocks_reorged", func(t *testing.T) {
		// A different block is stored at this height and the requested hash is not recorded as
		// reorged: this is a real/undetected reorg or inconsistent DB. It must NOT be validated
		// against L1 (no finalized/RPC lookup) and must surface as an error so it is repaired.
		testData := newEVMMultidownloaderTestData(t, true)
		blockNumber := uint64(100)
		blockHash := common.HexToHash("0x1234")

		storedBlock := &aggkittypes.BlockHeader{
			Number: blockNumber,
			Hash:   common.HexToHash("0x5678"), // Different hash
		}

		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, blockNumber).
			Return(storedBlock, mdrtypes.Finalized, nil).Once()
		testData.mockStorage.EXPECT().GetBlockReorgedReorgID(mock.Anything, blockNumber, blockHash).
			Return(uint64(0), false, nil).Once()
		// No GetCurrentBlockNumber / CustomHeaderByNumber expectations: the L1 fallback must not run.

		isValid, reorgID, err := testData.mdr.CheckValidBlock(context.Background(), blockNumber, blockHash)

		require.Error(t, err)
		require.Contains(t, err.Error(), "a different block is stored at this height")
		require.False(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})

	t.Run("returns false with reorgID when stored block hash does not match", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		blockNumber := uint64(100)
		blockHash := common.HexToHash("0x1234")
		expectedReorgID := uint64(99)

		storedBlock := &aggkittypes.BlockHeader{
			Number: blockNumber,
			Hash:   common.HexToHash("0xabcd"), // Different hash
		}

		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, blockNumber).
			Return(storedBlock, mdrtypes.Finalized, nil).Once()
		testData.mockStorage.EXPECT().GetBlockReorgedReorgID(mock.Anything, blockNumber, blockHash).
			Return(expectedReorgID, true, nil).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(context.Background(), blockNumber, blockHash)

		require.NoError(t, err)
		require.False(t, isValid)
		require.Equal(t, expectedReorgID, reorgID)
	})

	t.Run("returns error when GetBlockReorgedReorgID fails", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		blockNumber := uint64(100)
		blockHash := common.HexToHash("0x1234")

		storedBlock := &aggkittypes.BlockHeader{
			Number: blockNumber,
			Hash:   common.HexToHash("0x5678"), // Different hash
		}

		expectedErr := fmt.Errorf("reorg query error")
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, blockNumber).
			Return(storedBlock, mdrtypes.Finalized, nil).Once()
		testData.mockStorage.EXPECT().GetBlockReorgedReorgID(mock.Anything, blockNumber, blockHash).
			Return(uint64(0), false, expectedErr).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(context.Background(), blockNumber, blockHash)

		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot check blocks_reorged")
		require.True(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})
}

// TestEVMMultidownloader_CheckValidBlock_LegacyCheckpointAfterUpgrade covers issue #1638.
//
// Scenario: before upgrading to the multidownloader-based implementation, the legacy
// l1infotreesync stopped on an "empty" checkpoint block (a block with no events that the
// legacy syncer persisted in its own `block` table just to track progress). After upgrading,
// the multidownloader driver resumes by calling processor.GetLastProcessedBlockHeader, which
// returns that legacy checkpoint block (number + hash). That block was downloaded by the legacy
// syncer and therefore never existed in the multidownloader's own (freshly created, empty)
// storage. checkReorgedBlock -> CheckValidBlock is then asked to validate it.
//
// The block is not in `blocks` and not in `blocks_reorged`. Since it is at or below the finalized
// block, CheckValidBlock asks L1 for the canonical block at that height and compares hashes:
//   - canonical hash matches -> valid (the block is canonical, just never downloaded by us).
//   - canonical hash differs -> error (orphaned finalized block, manual intervention).
//
// issue: https://github.com/agglayer/aggkit/issues/1638
func TestEVMMultidownloader_CheckValidBlock_LegacyCheckpointAfterUpgrade(t *testing.T) {
	// The legacy checkpoint block reported by processor.GetLastProcessedBlockHeader.
	// It is not present in the multidownloader storage (neither in `blocks` nor `blocks_reorged`).
	legacyCheckpointBlock := uint64(10995066)
	legacyCheckpointHash := common.HexToHash(
		"0x132592000000000000000000000000000000000000000000000000000000000")
	finalizedBlock := uint64(11000000) // checkpoint is below finalized -> stable

	t.Run("canonical hash matches -> valid (resume sync)", func(t *testing.T) {
		// Real, empty storage: mimics the multidownloader storage right after the upgrade.
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, mock.Anything).Return(finalizedBlock, nil).Once()
		testData.mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(legacyCheckpointBlock)).
			Return(&aggkittypes.BlockHeader{Number: legacyCheckpointBlock, Hash: legacyCheckpointHash}, nil).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(
			context.Background(), legacyCheckpointBlock, legacyCheckpointHash)

		require.NoError(t, err)
		require.True(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})

	t.Run("canonical hash differs -> error (orphaned finalized block)", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, mock.Anything).Return(finalizedBlock, nil).Once()
		testData.mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(legacyCheckpointBlock)).
			Return(&aggkittypes.BlockHeader{Number: legacyCheckpointBlock, Hash: common.HexToHash("0xdead")}, nil).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(
			context.Background(), legacyCheckpointBlock, legacyCheckpointHash)

		require.Error(t, err)
		require.Contains(t, err.Error(), "does not match the canonical finalized")
		require.False(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})

	t.Run("block above finalized -> error (not stable yet)", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, mock.Anything).Return(legacyCheckpointBlock-1, nil).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(
			context.Background(), legacyCheckpointBlock, legacyCheckpointHash)

		require.Error(t, err)
		require.Contains(t, err.Error(), "above finalized block")
		require.False(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})

	t.Run("finalized block lookup fails -> error (assumed valid for retry)", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, mock.Anything).Return(uint64(0), fmt.Errorf("rpc down")).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(
			context.Background(), legacyCheckpointBlock, legacyCheckpointHash)

		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get finalized block number")
		require.True(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})

	t.Run("canonical header lookup fails -> error (assumed valid for retry)", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, mock.Anything).Return(finalizedBlock, nil).Once()
		testData.mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(legacyCheckpointBlock)).
			Return(nil, fmt.Errorf("rpc down")).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(
			context.Background(), legacyCheckpointBlock, legacyCheckpointHash)

		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get canonical header from L1")
		require.True(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})

	t.Run("canonical header is nil -> error (assumed valid for retry)", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, mock.Anything).Return(finalizedBlock, nil).Once()
		testData.mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(legacyCheckpointBlock)).
			Return(nil, nil).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(
			context.Background(), legacyCheckpointBlock, legacyCheckpointHash)

		require.Error(t, err)
		require.Contains(t, err.Error(), "got nil canonical header from L1")
		require.True(t, isValid)
		require.Equal(t, uint64(0), reorgID)
	})
}

func TestEVMMultidownloader_GetReorgedDataByReorgID(t *testing.T) {
	t.Run("returns reorg data successfully", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		expectedReorgID := uint64(42)
		expectedReorgData := &mdrtypes.ReorgData{
			ReorgID: expectedReorgID,
			BlockRangeAffected: aggkitcommon.BlockRange{
				FromBlock: 100,
				ToBlock:   200,
			},
			DetectedAtBlock:   250,
			DetectedTimestamp: 1234567890,
		}

		testData.mockStorage.EXPECT().GetReorgedDataByReorgID(mock.Anything, expectedReorgID).
			Return(expectedReorgData, nil).Once()

		result, err := testData.mdr.GetReorgedDataByReorgID(context.Background(), expectedReorgID)

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, expectedReorgData.ReorgID, result.ReorgID)
		require.Equal(t, expectedReorgData.BlockRangeAffected, result.BlockRangeAffected)
		require.Equal(t, expectedReorgData.DetectedAtBlock, result.DetectedAtBlock)
		require.Equal(t, expectedReorgData.DetectedTimestamp, result.DetectedTimestamp)
	})

	t.Run("returns error when storage query fails", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		expectedReorgID := uint64(42)
		expectedErr := fmt.Errorf("database error")

		testData.mockStorage.EXPECT().GetReorgedDataByReorgID(mock.Anything, expectedReorgID).
			Return(nil, expectedErr).Once()

		result, err := testData.mdr.GetReorgedDataByReorgID(context.Background(), expectedReorgID)

		require.Error(t, err)
		require.Equal(t, expectedErr, err)
		require.Nil(t, result)
	})

	t.Run("returns nil when reorgID not found", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		expectedReorgID := uint64(999)

		testData.mockStorage.EXPECT().GetReorgedDataByReorgID(mock.Anything, expectedReorgID).
			Return(nil, nil).Once()

		result, err := testData.mdr.GetReorgedDataByReorgID(context.Background(), expectedReorgID)

		require.NoError(t, err)
		require.Nil(t, result)
	})
}
