package multidownloader

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	errStorageExample = errors.New("storage error")
	addr1             = common.HexToAddress("0x1111111111111111111111111111111111111111")
)

func TestEVMMultidownloader_ChainID(t *testing.T) {
	t.Run("ChainID returns chain ID from eth client", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)
		expectedChainID := big.NewInt(137)
		testData.mockEthClient.EXPECT().ChainID(mock.Anything).
			Return(expectedChainID, nil)

		// Test
		result, err := testData.mdr.ChainID(context.Background())

		// Assertions
		require.NoError(t, err)
		require.Equal(t, expectedChainID.Uint64(), result)
	})
	t.Run("ChainID returns error from eth client", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)
		testData.mockEthClient.EXPECT().ChainID(mock.Anything).
			Return(nil, errors.New("eth client error"))

		// Test
		result, err := testData.mdr.ChainID(context.Background())

		// Assertions
		require.Error(t, err)
		require.Equal(t, uint64(0), result)
	})
}

func TestEVMMultidownloader_BlockNumber(t *testing.T) {
	testData := newEVMMultidownloaderTestData(t, true)
	testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(123456), nil)
	num, err := testData.mdr.BlockNumber(t.Context(), aggkittypes.LatestBlock)
	require.NoError(t, err)
	require.Equal(t, uint64(123456), num)
}
func TestEVMMultidownloader_BlockHeader(t *testing.T) {
	testData := newEVMMultidownloaderTestData(t, true)
	testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(123456), nil)
	testData.mockEthClient.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(123456)).
		Return(&types.Header{
			Number: big.NewInt(123456),
		}, nil)
	header, err := testData.mdr.BlockHeader(t.Context(), aggkittypes.LatestBlock)
	require.NoError(t, err)
	require.Equal(t, uint64(123456), header.Number)
}

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

func TestEVMMultidownloader_FilterLogs(t *testing.T) {
	t.Run("FilterLogs context canceled waiting to catch up", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		query := ethereum.FilterQuery{
			Addresses: []common.Address{addr1},
			FromBlock: big.NewInt(100),
			ToBlock:   big.NewInt(200),
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		result, err := testData.mdr.FilterLogs(ctx, query)

		// Assertions
		require.Nil(t, result)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("FilterLogs storage GetEthLogs error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		err := testData.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
			SyncerID:      "test_syncer",
			ContractsAddr: []common.Address{addr1},
			FromBlock:     100,
			ToBlock:       aggkittypes.LatestBlock,
		})
		require.NoError(t, err)

		query := ethereum.FilterQuery{
			Addresses: []common.Address{addr1},
			FromBlock: big.NewInt(100),
			ToBlock:   big.NewInt(200),
		}
		mdQuery := mdrtypes.NewLogQueryFromEthereumFilter(query)
		// It updated the syncedSegments with the new one to be available
		err = testData.mdr.syncedSegments.ExtendSegments(&mdQuery)
		require.NoError(t, err)
		testData.mockStorage.EXPECT().GetEthLogs(mock.Anything, mock.Anything).
			Return(nil, errStorageExample)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		result, err := testData.mdr.FilterLogs(ctx, query)

		// Assertions
		require.Nil(t, result)
		require.Error(t, err)
		require.ErrorIs(t, err, errStorageExample)
	})
}

func TestEVMMultidownloader_EthClient(t *testing.T) {
	testData := newEVMMultidownloaderTestData(t, true)
	require.Equal(t, testData.mockEthClient, testData.mdr.EthClient())
}
