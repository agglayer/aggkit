package multidownloader

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	dbtypes "github.com/agglayer/aggkit/db/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
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

// blockingFilterLogsStorage overrides GetEthLogs so a test can hold the
// downloader read lock deterministically: every call signals entered and then
// blocks until release is closed.
type blockingFilterLogsStorage struct {
	mdrtypes.Storager
	entered chan struct{}
	release chan struct{}
	logs    []ethtypes.Log
}

func (b *blockingFilterLogsStorage) GetEthLogs(_ dbtypes.Querier, _ mdrtypes.LogQuery) ([]ethtypes.Log, error) {
	b.entered <- struct{}{}
	<-b.release
	return b.logs, nil
}

// TestEVMMultidownloader_FilterLogs_ReadLockExcludesRollback pins that FilterLogs
// performs the availability decision and the storage read under the read lock.
// Two concurrent readers must both reach the storage read before a rollback
// writer can take the write lock, so the rollback cannot interleave and turn a
// covered query into a false-empty result. After the rollback the query is no
// longer covered and must report unavailable (an error), never a false-empty
// success.
func TestEVMMultidownloader_FilterLogs_ReadLockExcludesRollback(t *testing.T) {
	td := newEVMMultidownloaderTestData(t, true)
	td.FakeInitialized(t)

	query := ethereum.FilterQuery{
		Addresses: []common.Address{addr1},
		FromBlock: big.NewInt(100),
		ToBlock:   big.NewInt(100),
	}
	mdQuery := mdrtypes.NewLogQueryFromEthereumFilter(query)
	// Mark the exact query as covered so both readers enter the storage read.
	require.NoError(t, td.mdr.state.OnNewSyncedLogQuery(&mdQuery))

	expectedLogs := []ethtypes.Log{{BlockNumber: 100, Address: addr1}}
	blocking := &blockingFilterLogsStorage{
		Storager: td.usedStorage,
		entered:  make(chan struct{}, 2),
		release:  make(chan struct{}),
		logs:     expectedLogs,
	}
	td.mdr.storage = blocking

	type filterResult struct {
		logs []ethtypes.Log
		err  error
	}
	results := make(chan filterResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			logs, err := td.mdr.FilterLogs(context.Background(), query)
			results <- filterResult{logs: logs, err: err}
		}()
	}
	// Both readers hold the read lock once they are inside GetEthLogs.
	for i := 0; i < 2; i++ {
		select {
		case <-blocking.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("FilterLogs reader never reached the storage read")
		}
	}

	// A rollback writer must not be able to mutate the state while any reader
	// holds the read lock.
	writerAcquired := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		td.mdr.mutex.Lock()
		close(writerAcquired)
		// Rollback: drop all coverage so the query is no longer available.
		td.mdr.state = NewEmptyState()
		td.mdr.mutex.Unlock()
		close(writerDone)
	}()

	// Guard (not a synchronization point): with both readers provably holding
	// the read lock, the writer's write lock cannot be granted yet.
	select {
	case <-writerAcquired:
		t.Fatal("rollback writer acquired the write lock while readers held the read lock")
	case <-time.After(250 * time.Millisecond):
	}

	// Release the readers: they must return the stored logs, not a false-empty
	// result, because the rollback could not interleave between the availability
	// decision and the storage read.
	close(blocking.release)
	for i := 0; i < 2; i++ {
		select {
		case res := <-results:
			require.NoError(t, res.err)
			require.Equal(t, expectedLogs, res.logs)
		case <-time.After(5 * time.Second):
			t.Fatal("FilterLogs reader did not finish")
		}
	}

	// Only after all readers finished can the writer roll back.
	select {
	case <-writerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("rollback writer never acquired the lock after the readers finished")
	}

	// The query is no longer covered: it must report unavailable, never a
	// false-empty success.
	resp, err := td.mdr.LogQuery(context.Background(), mdQuery)
	require.Error(t, err)
	require.Contains(t, err.Error(), "logs not synced for query")
	require.Equal(t, mdrtypes.LogQueryResponse{}, resp)
}
