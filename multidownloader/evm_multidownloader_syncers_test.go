package multidownloader

import (
	"context"
	"errors"
	"math/big"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	errStorageExample = errors.New("storage error")
	// hash1             = common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	// hash2             = common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
)

func TestEVMMultidownloader_HeaderByNumber(t *testing.T) {
	t.Run("negative block number returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		// Test
		result, err := testData.mdr.HeaderByNumber(context.Background(), big.NewInt(-1))

		// Assertions
		require.Nil(t, result)
		require.Error(t, err)
		require.Contains(t, err.Error(), "negative block number not supported")
	})

	t.Run("storage error returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, uint64(123)).
			Return(nil, false, errStorageExample)

		// Test
		result, err := testData.mdr.HeaderByNumber(context.Background(), big.NewInt(123))

		// Assertions
		require.Nil(t, result)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get BlockHeader number=123")
		require.ErrorIs(t, err, errStorageExample)
	})

	t.Run("block found in storage returns block", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		expectedBlock := &aggkittypes.BlockHeader{
			Number: 123,
		}
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, expectedBlock.Number).
			Return(expectedBlock, false, nil)

		// Test
		result, err := testData.mdr.HeaderByNumber(context.Background(), big.NewInt(123))

		// Assertions
		require.NoError(t, err)
		require.Equal(t, expectedBlock, result)
	})

	t.Run("block not in storage, eth client error returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, uint64(123)).
			Return(nil, false, nil) // Block not found in storage

		ethClientErr := errors.New("eth client error")
		testData.mockEthClient.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(123)).
			Return(nil, ethClientErr)

		// Test
		result, err := testData.mdr.HeaderByNumber(context.Background(), big.NewInt(123))

		// Assertions
		require.Nil(t, result)
		require.Error(t, err)
		require.Contains(t, err.Error(), "fails ethClient.HeaderByNumber(123)")
		require.ErrorIs(t, err, ethClientErr)
	})

	t.Run("block not in storage, fetched from eth client successfully", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, uint64(123)).
			Return(nil, false, nil) // Block not found in storage

		ethHeader := &types.Header{
			Number: big.NewInt(123),
		}
		testData.mockEthClient.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(123)).
			Return(ethHeader, nil)

		// Test
		result, err := testData.mdr.HeaderByNumber(context.Background(), big.NewInt(123))

		// Assertions
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, uint64(123), result.Number)
	})
}

/*
func TestEVMMultidownloader_FilterLogs(t *testing.T) {
	t.Run("storage error returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		query := ethereum.FilterQuery{
			Addresses: []common.Address{hash1},
			FromBlock: big.NewInt(100),
			ToBlock:   big.NewInt(200),
		}

		// Mock IsAvailable to return true (logs are available)
		testData.mdr.isAvailableFunc = func(logQuery interface{}) bool {
			return true
		}

		testData.mockStorage.EXPECT().GetEthLogs(mock.Anything, mock.Anything).
			Return(nil, storageErr)

		// Test
		result, err := testData.mdr.FilterLogs(t.Context(), query)

		// Assertions
		require.Nil(t, result)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get logs")
		require.ErrorIs(t, err, storageErr)
	})

		t.Run("logs available immediately, returns logs successfully", func(t *testing.T) {
			// Setup
			testData := newEVMMultidownloaderTestData(t, true)

			query := ethereum.FilterQuery{
				FromBlock: big.NewInt(100),
				ToBlock:   big.NewInt(200),
			}

			expectedLogs := []types.Log{
				{
					BlockNumber: 150,
					TxHash:      [32]byte{1, 2, 3},
				},
				{
					BlockNumber: 175,
					TxHash:      [32]byte{4, 5, 6},
				},
			}

			// Mock IsAvailable to return true (logs are available)
			testData.mdr.isAvailableFunc = func(logQuery interface{}) bool {
				return true
			}

			testData.mockStorage.EXPECT().GetEthLogs(mock.Anything, mock.Anything).
				Return(expectedLogs, nil)

			// Test
			result, err := testData.mdr.FilterLogs(context.Background(), query)

			// Assertions
			require.NoError(t, err)
			require.Equal(t, expectedLogs, result)
		})

		t.Run("logs not available initially, waits then returns logs", func(t *testing.T) {
			// Setup
			testData := newEVMMultidownloaderTestData(t, true)

			query := ethereum.FilterQuery{
				FromBlock: big.NewInt(100),
				ToBlock:   big.NewInt(200),
			}

			expectedLogs := []types.Log{
				{
					BlockNumber: 150,
					TxHash:      [32]byte{1, 2, 3},
				},
			}

			// Mock IsAvailable to return false first, then true
			callCount := 0
			testData.mdr.isAvailableFunc = func(logQuery interface{}) bool {
				callCount++
				return callCount > 1
			}

			testData.mockStorage.EXPECT().GetEthLogs(mock.Anything, mock.Anything).
				Return(expectedLogs, nil)

			// Test
			result, err := testData.mdr.FilterLogs(context.Background(), query)

			// Assertions
			require.NoError(t, err)
			require.Equal(t, expectedLogs, result)
			require.Greater(t, callCount, 1, "Should have called IsAvailable multiple times")
		})

		t.Run("empty logs returned successfully", func(t *testing.T) {
			// Setup
			testData := newEVMMultidownloaderTestData(t, true)

			query := ethereum.FilterQuery{
				FromBlock: big.NewInt(100),
				ToBlock:   big.NewInt(200),
			}

			// Mock IsAvailable to return true (logs are available)
			testData.mdr.isAvailableFunc = func(logQuery interface{}) bool {
				return true
			}

			testData.mockStorage.EXPECT().GetEthLogs(mock.Anything, mock.Anything).
				Return([]types.Log{}, nil)

			// Test
			result, err := testData.mdr.FilterLogs(context.Background(), query)

			// Assertions
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, result, 0)
		})

}
*/
