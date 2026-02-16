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

	t.Run("returns false when block not stored and not in blocks_reorged", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		blockNumber := uint64(100)
		blockHash := common.HexToHash("0x1234")

		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, blockNumber).
			Return(nil, mdrtypes.NotFinalized, nil).Once()
		testData.mockStorage.EXPECT().GetBlockReorgedReorgID(mock.Anything, blockNumber, blockHash).
			Return(uint64(0), false, nil).Once()

		isValid, reorgID, err := testData.mdr.CheckValidBlock(context.Background(), blockNumber, blockHash)

		require.Error(t, err)
		require.Contains(t, err.Error(), "not found in storage or blocks_reorged")
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
