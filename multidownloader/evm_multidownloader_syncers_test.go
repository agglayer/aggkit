package multidownloader

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
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
	testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, uint64(123456)).
		Return(nil, false, nil) // Block not found in storage, will fetch from ethClient
	testData.mockEthClient.EXPECT().CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(123456)).
		Return(&aggkittypes.BlockHeader{
			Number: 123456,
		}, nil)
	header, err := testData.mdr.HeaderByNumber(t.Context(), &aggkittypes.LatestBlock)
	require.NoError(t, err)
	require.Equal(t, uint64(123456), header.Number)
}

func TestEVMMultidownloader_HeaderByNumber(t *testing.T) {
	t.Run("negative block number returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)
		// FinalizedBlock is not a numeric finality, so GetCurrentBlockNumber will fail
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.FinalizedBlock).
			Return(uint64(0), errors.New("only numeric block finalities are supported"))

		// Test
		result, err := testData.mdr.HeaderByNumber(context.Background(), &aggkittypes.FinalizedBlock)

		// Assertions
		require.Nil(t, result)
		require.Error(t, err)
		require.Contains(t, err.Error(), "only numeric")
	})

	t.Run("storage error returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(123), nil)
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, uint64(123)).
			Return(nil, false, errStorageExample)

		// Test
		result, err := testData.mdr.HeaderByNumber(context.Background(), aggkittypes.NewBlockNumber(123))

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
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(expectedBlock.Number, nil)
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, expectedBlock.Number).
			Return(expectedBlock, false, nil)

		// Test
		result, err := testData.mdr.HeaderByNumber(context.Background(), aggkittypes.NewBlockNumber(123))

		// Assertions
		require.NoError(t, err)
		require.Equal(t, expectedBlock, result)
	})

	t.Run("block not in storage, eth client error returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(123), nil)
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, uint64(123)).
			Return(nil, false, nil) // Block not found in storage

		ethClientErr := errors.New("eth client error")
		testData.mockEthClient.EXPECT().CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(123)).
			Return(nil, ethClientErr)

		// Test
		result, err := testData.mdr.HeaderByNumber(context.Background(), aggkittypes.NewBlockNumber(123))

		// Assertions
		require.Nil(t, result)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ethClient.HeaderByNumber(123) failed")
		require.ErrorIs(t, err, ethClientErr)
	})

	t.Run("block not in storage, fetched from eth client successfully", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(123), nil)
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, uint64(123)).
			Return(nil, false, nil) // Block not found in storage

		ethHeader := &aggkittypes.BlockHeader{
			Number: 123,
		}
		testData.mockEthClient.EXPECT().CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(123)).
			Return(ethHeader, nil)

		// Test
		result, err := testData.mdr.HeaderByNumber(context.Background(), aggkittypes.NewBlockNumber(123))

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
		testData.FakeInitialized(t)
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
			SyncerID:          "test_syncer",
			ContractAddresses: []common.Address{addr1},
			FromBlock:         100,
			ToBlock:           aggkittypes.LatestBlock,
		})
		require.NoError(t, err)
		testData.MockInitialize(t, 1)
		err = testData.mdr.Initialize(t.Context())
		require.NoError(t, err)

		query := ethereum.FilterQuery{
			Addresses: []common.Address{addr1},
			FromBlock: big.NewInt(100),
			ToBlock:   big.NewInt(200),
		}
		mdQuery := mdrtypes.NewLogQueryFromEthereumFilter(query)
		// It updated the syncedSegments with the new one to be available
		err = testData.mdr.state.OnNewSyncedLogQuery(&mdQuery)
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

func TestEVMMultidownloader_LogQuery(t *testing.T) {
	t.Run("success case with unsafe range calculation", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)
		testData.FakeInitialized(t)

		// Create a log query
		query := mdrtypes.NewLogQuery(100, 200, []common.Address{addr1})

		// Mark the query as synced in state
		err := testData.mdr.state.OnNewSyncedLogQuery(&query)
		require.NoError(t, err)

		// Mock GetFinalizedBlockNumber (via GetCurrentBlockNumber)
		finalizedBlock := uint64(150)
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, testData.mdr.cfg.BlockFinality).
			Return(finalizedBlock, nil)

		// Mock storage.LogQuery to return a response
		expectedResponse := mdrtypes.LogQueryResponse{
			ResponseRange: aggkitcommon.NewBlockRange(100, 200),
		}
		testData.mockStorage.EXPECT().LogQuery(mock.Anything, query).
			Return(expectedResponse, nil)

		// Test
		result, err := testData.mdr.LogQuery(context.Background(), query)

		// Assertions
		require.NoError(t, err)
		require.Equal(t, aggkitcommon.NewBlockRange(100, 200), result.ResponseRange)
		// UnsafeRange should be the range after finalized block
		require.Equal(t, aggkitcommon.NewBlockRange(151, 200), result.UnsafeRange)
	})

	t.Run("logs not synced returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)
		testData.FakeInitialized(t)

		// Create a query that is NOT synced
		query := mdrtypes.NewLogQuery(100, 200, []common.Address{addr1})

		// Test - state.IsPartiallyAvailable will return false because we didn't call OnNewSyncedLogQuery
		result, err := testData.mdr.LogQuery(context.Background(), query)

		// Assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "logs not synced for query")
		require.Equal(t, mdrtypes.LogQueryResponse{}, result)
	})

	t.Run("GetFinalizedBlockNumber error returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)
		testData.FakeInitialized(t)

		// Create and sync a query
		query := mdrtypes.NewLogQuery(100, 200, []common.Address{addr1})
		err := testData.mdr.state.OnNewSyncedLogQuery(&query)
		require.NoError(t, err)

		// Mock GetFinalizedBlockNumber to fail
		expectedErr := errors.New("finalized block error")
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, testData.mdr.cfg.BlockFinality).
			Return(uint64(0), expectedErr)

		// Test
		result, err := testData.mdr.LogQuery(context.Background(), query)

		// Assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get finalized block number")
		require.ErrorIs(t, err, expectedErr)
		require.Equal(t, mdrtypes.LogQueryResponse{}, result)
	})

	t.Run("storage.LogQuery error returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)
		testData.FakeInitialized(t)

		// Create and sync a query
		query := mdrtypes.NewLogQuery(100, 200, []common.Address{addr1})
		err := testData.mdr.state.OnNewSyncedLogQuery(&query)
		require.NoError(t, err)

		// Mock GetFinalizedBlockNumber
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, testData.mdr.cfg.BlockFinality).
			Return(uint64(150), nil)

		// Mock storage.LogQuery to fail
		testData.mockStorage.EXPECT().LogQuery(mock.Anything, query).
			Return(mdrtypes.LogQueryResponse{}, errStorageExample)

		// Test
		result, err := testData.mdr.LogQuery(context.Background(), query)

		// Assertions
		require.Error(t, err)
		require.Contains(t, err.Error(), "error executing log query")
		require.ErrorIs(t, err, errStorageExample)
		require.Equal(t, mdrtypes.LogQueryResponse{}, result)
	})

	t.Run("empty unsafe range when all blocks are finalized", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)
		testData.FakeInitialized(t)

		// Create a log query
		query := mdrtypes.NewLogQuery(100, 200, []common.Address{addr1})
		err := testData.mdr.state.OnNewSyncedLogQuery(&query)
		require.NoError(t, err)

		// Mock GetFinalizedBlockNumber - finalized is beyond the query range
		finalizedBlock := uint64(250)
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, testData.mdr.cfg.BlockFinality).
			Return(finalizedBlock, nil)

		// Mock storage.LogQuery
		expectedResponse := mdrtypes.LogQueryResponse{
			ResponseRange: aggkitcommon.NewBlockRange(100, 200),
		}
		testData.mockStorage.EXPECT().LogQuery(mock.Anything, query).
			Return(expectedResponse, nil)

		// Test
		result, err := testData.mdr.LogQuery(context.Background(), query)

		// Assertions
		require.NoError(t, err)
		require.Equal(t, aggkitcommon.NewBlockRange(100, 200), result.ResponseRange)
		// UnsafeRange should be empty since all blocks are finalized
		require.True(t, result.UnsafeRange.IsEmpty())
	})
}

func TestEVMMultidownloader_StorageHeaderByNumber(t *testing.T) {
	t.Run("block found in storage with finalized=true", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		expectedBlock := &aggkittypes.BlockHeader{
			Number: 123,
		}
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(expectedBlock.Number, nil)
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, expectedBlock.Number).
			Return(expectedBlock, true, nil)

		// Test
		result, finalized, err := testData.mdr.StorageHeaderByNumber(context.Background(), aggkittypes.NewBlockNumber(123))

		// Assertions
		require.NoError(t, err)
		require.Equal(t, expectedBlock, result)
		require.True(t, finalized)
	})

	t.Run("block found in storage with finalized=false", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		expectedBlock := &aggkittypes.BlockHeader{
			Number: 456,
		}
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(expectedBlock.Number, nil)
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, expectedBlock.Number).
			Return(expectedBlock, false, nil)

		// Test
		result, finalized, err := testData.mdr.StorageHeaderByNumber(context.Background(), aggkittypes.NewBlockNumber(456))

		// Assertions
		require.NoError(t, err)
		require.Equal(t, expectedBlock, result)
		require.False(t, finalized)
	})

	t.Run("nil block number defaults to LatestBlock", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		expectedBlock := &aggkittypes.BlockHeader{
			Number: 999,
		}
		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
			Return(expectedBlock.Number, nil)
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, expectedBlock.Number).
			Return(expectedBlock, true, nil)

		// Test
		result, finalized, err := testData.mdr.StorageHeaderByNumber(context.Background(), nil)

		// Assertions
		require.NoError(t, err)
		require.Equal(t, expectedBlock, result)
		require.True(t, finalized)
	})

	t.Run("block not found in storage returns nil", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(789), nil)
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, uint64(789)).
			Return(nil, false, nil)

		// Test
		result, finalized, err := testData.mdr.StorageHeaderByNumber(context.Background(), aggkittypes.NewBlockNumber(789))

		// Assertions
		require.NoError(t, err)
		require.Nil(t, result)
		require.False(t, finalized)
	})

	t.Run("GetCurrentBlockNumber error returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)
		expectedErr := errors.New("block number resolution error")

		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.FinalizedBlock).
			Return(uint64(0), expectedErr)

		// Test
		result, finalized, err := testData.mdr.StorageHeaderByNumber(context.Background(), &aggkittypes.FinalizedBlock)

		// Assertions
		require.Nil(t, result)
		require.False(t, finalized)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get block number for finality")
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("GetBlockHeaderByNumber error returns error", func(t *testing.T) {
		// Setup
		testData := newEVMMultidownloaderTestData(t, true)

		testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(555), nil)
		testData.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, uint64(555)).
			Return(nil, false, errStorageExample)

		// Test
		result, finalized, err := testData.mdr.StorageHeaderByNumber(context.Background(), aggkittypes.NewBlockNumber(555))

		// Assertions
		require.Nil(t, result)
		require.False(t, finalized)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get BlockHeader number=555")
		require.ErrorIs(t, err, errStorageExample)
	})
}
