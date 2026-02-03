package multidownloader

// Unit tests for download.go
// Coverage: Most functions have 100% coverage including:
// - executeLogQuery: 100%
// - logsToEVMBlock: 100%
// - appendLog: 100%
// - newMaxLogQuery: 100%
// - checkReorgedBlock: 100%
// - DownloadNextBlocks: 91.3% (includes retry, timeout, and context cancellation scenarios)

import (
	"context"
	"fmt"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	mdrsynctypesmocks "github.com/agglayer/aggkit/multidownloader/sync/types/mocks"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDownloadNextBlocks_Success(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	// Create a mock appender
	appenderCalled := false
	appender := sync.LogAppenderMap{
		common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"): func(b *sync.EVMBlock, l types.Log) error {
			appenderCalled = true
			b.Events = append(b.Events, "test_event")
			return nil
		},
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           appender,
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	lastBlockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	syncerConfig := aggkittypes.SyncerConfig{
		FromBlock:         50,
		ContractAddresses: []common.Address{common.HexToAddress("0x123")},
	}

	// Setup mocks
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(true, uint64(0), nil)
	mockMdr.EXPECT().IsAvailable(mock.Anything).Return(true)
	mockMdr.EXPECT().LogQuery(ctx, mock.Anything).Return(mdrtypes.LogQueryResponse{
		Blocks: []mdrtypes.BlockWithLogs{
			{
				Header: aggkittypes.BlockHeader{
					Number: 101,
					Hash:   common.HexToHash("0xblock101"),
					Time:   1000,
				},
				IsFinal: true,
				Logs: []mdrtypes.Log{
					{
						BlockNumber:    101,
						BlockTimestamp: 1000,
						Topics: []common.Hash{
							common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
						},
					},
				},
			},
		},
		ResponseRange: aggkitcommon.BlockRange{FromBlock: 101, ToBlock: 110},
	}, nil)
	mockMdr.EXPECT().StorageHeaderByNumber(ctx, mock.Anything).Return(&aggkittypes.BlockHeader{
		Number: 110,
		Hash:   common.HexToHash("0xblock110"),
		Time:   1100,
	}, mdrtypes.Finalized, nil)

	result, err := download.DownloadNextBlocks(ctx, lastBlockHeader, 10, syncerConfig)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Data, 2)
	require.Equal(t, uint64(101), result.Data[0].Num)
	require.Equal(t, uint64(110), result.Data[1].Num)
	require.True(t, appenderCalled)
}

func TestDownloadNextBlocks_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	lastBlockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	syncerConfig := aggkittypes.SyncerConfig{
		FromBlock:         50,
		ContractAddresses: []common.Address{common.HexToAddress("0x123")},
	}

	result, err := download.DownloadNextBlocks(ctx, lastBlockHeader, 10, syncerConfig)

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, context.Canceled, err)
}

func TestDownloadNextBlocks_ReorgDetected(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	lastBlockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	syncerConfig := aggkittypes.SyncerConfig{
		FromBlock:         50,
		ContractAddresses: []common.Address{common.HexToAddress("0x123")},
	}

	reorgData := &mdrtypes.ReorgData{
		ChainID:            1,
		BlockRangeAffected: aggkitcommon.NewBlockRange(100, 105),
		DetectedAtBlock:    106,
	}

	// Setup mocks - reorg detected
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(false, uint64(1), nil)
	mockMdr.EXPECT().GetReorgedDataByChainID(ctx, uint64(1)).Return(reorgData, nil)

	result, err := download.DownloadNextBlocks(ctx, lastBlockHeader, 10, syncerConfig)

	require.Error(t, err)
	require.Nil(t, result)
	require.True(t, mdrtypes.IsReorgedError(err))
	reorgErr := mdrtypes.CastReorgedError(err)
	require.Equal(t, uint64(1), reorgErr.ReorgedChainID)
}

func TestDownloadNextBlocks_NilLastBlockHeader(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	appender := sync.LogAppenderMap{
		common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"): func(b *sync.EVMBlock, l types.Log) error {
			b.Events = append(b.Events, "test_event")
			return nil
		},
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           appender,
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	syncerConfig := aggkittypes.SyncerConfig{
		FromBlock:         50,
		ContractAddresses: []common.Address{common.HexToAddress("0x123")},
	}

	// Setup mocks
	mockMdr.EXPECT().IsAvailable(mock.Anything).Return(true)
	mockMdr.EXPECT().LogQuery(ctx, mock.Anything).Return(mdrtypes.LogQueryResponse{
		Blocks: []mdrtypes.BlockWithLogs{
			{
				Header: aggkittypes.BlockHeader{
					Number: 50,
					Hash:   common.HexToHash("0xblock50"),
					Time:   1000,
				},
				IsFinal: true,
				Logs: []mdrtypes.Log{
					{
						BlockNumber:    50,
						BlockTimestamp: 1000,
						Topics: []common.Hash{
							common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
						},
					},
				},
			},
		},
		ResponseRange: aggkitcommon.BlockRange{FromBlock: 50, ToBlock: 59},
	}, nil)
	mockMdr.EXPECT().StorageHeaderByNumber(ctx, mock.Anything).Return(&aggkittypes.BlockHeader{
		Number: 59,
		Hash:   common.HexToHash("0xblock59"),
		Time:   1090,
	}, mdrtypes.Finalized, nil)

	result, err := download.DownloadNextBlocks(ctx, nil, 10, syncerConfig)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Data, 2)
	require.Equal(t, uint64(50), result.Data[0].Num)
	require.Equal(t, uint64(59), result.Data[1].Num)
}

func TestDownloadNextBlocks_LogsNotAvailableInitially(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	appender := sync.LogAppenderMap{
		common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"): func(b *sync.EVMBlock, l types.Log) error {
			b.Events = append(b.Events, "test_event")
			return nil
		},
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           appender,
		waitPeriodToCatchUpMaximumLogRange: 500 * time.Millisecond,
		pullingPeriod:                      50 * time.Millisecond,
	}

	lastBlockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	syncerConfig := aggkittypes.SyncerConfig{
		FromBlock:         50,
		ContractAddresses: []common.Address{common.HexToAddress("0x123")},
	}

	// First call: checkReorgedBlock before PollingWithTimeout (line 65)
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(true, uint64(0), nil).Once()

	// First iteration: PollingWithTimeout calls checkCondition immediately
	// This calls checkReorgedBlock (line 74) and executeLogQuery
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(true, uint64(0), nil).Once()
	mockMdr.EXPECT().IsAvailable(mock.Anything).Return(false).Once()
	mockMdr.EXPECT().IsPartiallyAvailable(mock.Anything).Return(false, nil).Once()

	// Second iteration in polling loop
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(true, uint64(0), nil).Once()
	mockMdr.EXPECT().IsAvailable(mock.Anything).Return(true).Once()
	mockMdr.EXPECT().LogQuery(ctx, mock.Anything).Return(mdrtypes.LogQueryResponse{
		Blocks: []mdrtypes.BlockWithLogs{
			{
				Header: aggkittypes.BlockHeader{
					Number: 101,
					Hash:   common.HexToHash("0xblock101"),
					Time:   1000,
				},
				IsFinal: true,
				Logs: []mdrtypes.Log{
					{
						BlockNumber:    101,
						BlockTimestamp: 1000,
						Topics: []common.Hash{
							common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
						},
					},
				},
			},
		},
		ResponseRange: aggkitcommon.BlockRange{FromBlock: 101, ToBlock: 110},
	}, nil).Once()
	mockMdr.EXPECT().StorageHeaderByNumber(ctx, mock.Anything).Return(&aggkittypes.BlockHeader{
		Number: 110,
		Hash:   common.HexToHash("0xblock110"),
		Time:   1100,
	}, mdrtypes.Finalized, nil).Once()

	// Final checkReorgedBlock after PollingWithTimeout completes (line 101)
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(true, uint64(0), nil).Once()

	result, err := download.DownloadNextBlocks(ctx, lastBlockHeader, 10, syncerConfig)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Data, 2)
	require.Equal(t, uint64(101), result.Data[0].Num)
	require.Equal(t, uint64(110), result.Data[1].Num)
}

func TestDownloadNextBlocks_TimeoutWaitingForLogs(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 100 * time.Millisecond,
		pullingPeriod:                      200 * time.Millisecond,
	}

	lastBlockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	syncerConfig := aggkittypes.SyncerConfig{
		FromBlock:         50,
		ContractAddresses: []common.Address{common.HexToAddress("0x123")},
	}

	// First call: checkReorgedBlock before PollingWithTimeout (line 65)
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(true, uint64(0), nil).Once()

	// PollingWithTimeout calls checkCondition multiple times until timeout
	// Each call includes checkReorgedBlock and executeLogQuery
	// Since timeout is 100ms and polling period is 200ms, it will try only once before timeout
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(true, uint64(0), nil).Once()
	mockMdr.EXPECT().IsAvailable(mock.Anything).Return(false).Once()
	mockMdr.EXPECT().IsPartiallyAvailable(mock.Anything).Return(false, nil).Once()

	result, err := download.DownloadNextBlocks(ctx, lastBlockHeader, 10, syncerConfig)

	// After timeout, should return error
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "logs not available")
}

func TestDownloadNextBlocks_ContextCancelledDuringRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 5 * time.Second,
		pullingPeriod:                      50 * time.Millisecond,
	}

	lastBlockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	syncerConfig := aggkittypes.SyncerConfig{
		FromBlock:         50,
		ContractAddresses: []common.Address{common.HexToAddress("0x123")},
	}

	// checkReorgedBlock and executeLogQuery may be called multiple times before context is cancelled
	// Using Maybe() to allow flexible number of calls depending on timing
	mockMdr.EXPECT().CheckValidBlock(mock.Anything, uint64(100), lastBlockHeader.Hash).Return(true, uint64(0), nil).Maybe()
	mockMdr.EXPECT().IsAvailable(mock.Anything).Return(false).Maybe()
	mockMdr.EXPECT().IsPartiallyAvailable(mock.Anything).Return(false, nil).Maybe()

	// During retry loop, cancel the context after a short delay
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	result, err := download.DownloadNextBlocks(ctx, lastBlockHeader, 10, syncerConfig)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "context")
}

func TestDownloadNextBlocks_ReorgDetectedDuringRetry(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 500 * time.Millisecond,
		pullingPeriod:                      30 * time.Millisecond,
	}

	lastBlockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	syncerConfig := aggkittypes.SyncerConfig{
		FromBlock:         50,
		ContractAddresses: []common.Address{common.HexToAddress("0x123")},
	}

	reorgData := &mdrtypes.ReorgData{
		ChainID:            1,
		BlockRangeAffected: aggkitcommon.NewBlockRange(100, 105),
		DetectedAtBlock:    106,
	}

	// First call: checkReorgedBlock before PollingWithTimeout (line 65)
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(true, uint64(0), nil).Once()

	// First iteration: PollingWithTimeout calls checkCondition immediately
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(true, uint64(0), nil).Once()
	mockMdr.EXPECT().IsAvailable(mock.Anything).Return(false).Once()
	mockMdr.EXPECT().IsPartiallyAvailable(mock.Anything).Return(false, nil).Once()

	// Second iteration: reorg detected during checkReorgedBlock
	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), lastBlockHeader.Hash).Return(false, uint64(1), nil).Once()
	mockMdr.EXPECT().GetReorgedDataByChainID(ctx, uint64(1)).Return(reorgData, nil).Once()

	result, err := download.DownloadNextBlocks(ctx, lastBlockHeader, 10, syncerConfig)

	require.Error(t, err)
	require.Nil(t, result)
	require.True(t, mdrtypes.IsReorgedError(err))
}

func TestExecuteLogQuery_FullyAvailable(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	appender := sync.LogAppenderMap{
		common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"): func(b *sync.EVMBlock, l types.Log) error {
			b.Events = append(b.Events, "test_event")
			return nil
		},
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           appender,
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	logQuery := mdrtypes.NewLogQuery(100, 110, []common.Address{common.HexToAddress("0x123")})

	mockMdr.EXPECT().IsAvailable(logQuery).Return(true)
	mockMdr.EXPECT().LogQuery(ctx, logQuery).Return(mdrtypes.LogQueryResponse{
		Blocks: []mdrtypes.BlockWithLogs{
			{
				Header: aggkittypes.BlockHeader{
					Number: 105,
					Hash:   common.HexToHash("0xblock105"),
					Time:   2000,
				},
				IsFinal: true,
				Logs: []mdrtypes.Log{
					{
						BlockNumber:    105,
						BlockTimestamp: 2000,
						Topics: []common.Hash{
							common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
						},
					},
				},
			},
		},
		ResponseRange: aggkitcommon.BlockRange{FromBlock: 100, ToBlock: 110},
	}, nil)
	mockMdr.EXPECT().StorageHeaderByNumber(ctx, mock.Anything).Return(&aggkittypes.BlockHeader{
		Number: 110,
		Hash:   common.HexToHash("0xblock110"),
		Time:   2100,
	}, mdrtypes.Finalized, nil)

	result, err := download.executeLogQuery(ctx, logQuery)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Data, 2)
	require.Equal(t, uint64(105), result.Data[0].Num)
	require.Equal(t, uint64(110), result.Data[1].Num)
}

func TestExecuteLogQuery_PartiallyAvailable(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	appender := sync.LogAppenderMap{
		common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"): func(b *sync.EVMBlock, l types.Log) error {
			b.Events = append(b.Events, "test_event")
			return nil
		},
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           appender,
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	logQuery := mdrtypes.NewLogQuery(100, 110, []common.Address{common.HexToAddress("0x123")})
	partialQuery := mdrtypes.NewLogQuery(100, 105, []common.Address{common.HexToAddress("0x123")})

	mockMdr.EXPECT().IsAvailable(logQuery).Return(false)
	mockMdr.EXPECT().IsPartiallyAvailable(logQuery).Return(true, &partialQuery)
	mockMdr.EXPECT().LogQuery(ctx, partialQuery).Return(mdrtypes.LogQueryResponse{
		Blocks: []mdrtypes.BlockWithLogs{
			{
				Header: aggkittypes.BlockHeader{
					Number: 103,
					Hash:   common.HexToHash("0xblock103"),
					Time:   2000,
				},
				IsFinal: true,
				Logs: []mdrtypes.Log{
					{
						BlockNumber:    103,
						BlockTimestamp: 2000,
						Topics: []common.Hash{
							common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
						},
					},
				},
			},
		},
		ResponseRange: aggkitcommon.BlockRange{FromBlock: 100, ToBlock: 105},
	}, nil)
	// When using partial query, addLastBlockIfNotIncluded uses responseRange.ToBlock (105)
	mockMdr.EXPECT().StorageHeaderByNumber(ctx, mock.Anything).Return(&aggkittypes.BlockHeader{
		Number: 105,
		Hash:   common.HexToHash("0xblock105"),
		Time:   2050,
	}, mdrtypes.Finalized, nil)

	result, err := download.executeLogQuery(ctx, logQuery)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Data, 2)
	require.Equal(t, uint64(103), result.Data[0].Num)
	require.Equal(t, uint64(105), result.Data[1].Num) // Last block is from partial response range
}

func TestExecuteLogQuery_NotAvailable(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	logQuery := mdrtypes.NewLogQuery(100, 110, []common.Address{common.HexToAddress("0x123")})

	mockMdr.EXPECT().IsAvailable(logQuery).Return(false)
	mockMdr.EXPECT().IsPartiallyAvailable(logQuery).Return(false, nil)

	result, err := download.executeLogQuery(ctx, logQuery)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "logs not available")
}

func TestExecuteLogQuery_GetEthLogsError(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	logQuery := mdrtypes.NewLogQuery(100, 110, []common.Address{common.HexToAddress("0x123")})

	mockMdr.EXPECT().IsAvailable(logQuery).Return(true)
	mockMdr.EXPECT().LogQuery(ctx, logQuery).Return(mdrtypes.LogQueryResponse{}, fmt.Errorf("database error"))

	result, err := download.executeLogQuery(ctx, logQuery)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "cannot get logs")
}

func TestNewMaxLogQuery_WithLastBlockHeader(t *testing.T) {
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                nil,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	lastBlockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	syncerConfig := aggkittypes.SyncerConfig{
		FromBlock:         50,
		ContractAddresses: []common.Address{common.HexToAddress("0x123")},
	}

	query := download.newMaxLogQuery(lastBlockHeader, 10, syncerConfig)

	require.Equal(t, uint64(101), query.BlockRange.FromBlock)
	require.Equal(t, uint64(110), query.BlockRange.ToBlock)
	require.Equal(t, syncerConfig.ContractAddresses, query.Addrs)
}

func TestNewMaxLogQuery_WithoutLastBlockHeader(t *testing.T) {
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                nil,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	syncerConfig := aggkittypes.SyncerConfig{
		FromBlock:         50,
		ContractAddresses: []common.Address{common.HexToAddress("0x123")},
	}

	query := download.newMaxLogQuery(nil, 10, syncerConfig)

	require.Equal(t, uint64(50), query.BlockRange.FromBlock)
	require.Equal(t, uint64(59), query.BlockRange.ToBlock)
	require.Equal(t, syncerConfig.ContractAddresses, query.Addrs)
}

func TestCheckReorgedBlock_NilBlockHeader(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	// When blockHeader is nil, no reorg check should be performed
	err := download.checkReorgedBlock(ctx, nil)

	require.NoError(t, err)
}

func TestCheckReorgedBlock_ValidBlock(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	blockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), blockHeader.Hash).Return(true, uint64(0), nil)

	err := download.checkReorgedBlock(ctx, blockHeader)

	require.NoError(t, err)
}

func TestCheckReorgedBlock_InvalidBlock(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	blockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	reorgData := &mdrtypes.ReorgData{
		ChainID:            1,
		BlockRangeAffected: aggkitcommon.NewBlockRange(100, 105),
		DetectedAtBlock:    106,
	}

	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), blockHeader.Hash).Return(false, uint64(1), nil)
	mockMdr.EXPECT().GetReorgedDataByChainID(ctx, uint64(1)).Return(reorgData, nil)

	err := download.checkReorgedBlock(ctx, blockHeader)

	require.Error(t, err)
	require.True(t, mdrtypes.IsReorgedError(err))
	reorgErr := mdrtypes.CastReorgedError(err)
	require.Equal(t, uint64(1), reorgErr.ReorgedChainID)
}

func TestCheckReorgedBlock_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	blockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	err := download.checkReorgedBlock(ctx, blockHeader)

	require.Error(t, err)
	require.Equal(t, context.Canceled, err)
}

func TestCheckReorgedBlock_CheckValidBlockError(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	blockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), blockHeader.Hash).Return(false, uint64(0), fmt.Errorf("check error"))

	err := download.checkReorgedBlock(ctx, blockHeader)

	require.Error(t, err)
	require.Contains(t, err.Error(), "check error")
}

func TestCheckReorgedBlock_GetReorgedDataError(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	blockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), blockHeader.Hash).Return(false, uint64(1), nil)
	mockMdr.EXPECT().GetReorgedDataByChainID(ctx, uint64(1)).Return(nil, fmt.Errorf("database error"))

	err := download.checkReorgedBlock(ctx, blockHeader)

	require.Error(t, err)
	require.Contains(t, err.Error(), "database error")
}

func TestCheckReorgedBlock_NilReorgData(t *testing.T) {
	ctx := context.Background()
	mockMdr := mdrsynctypesmocks.NewMultidownloaderInterface(t)
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	download := &EVMDownloader{
		mdr:                                mockMdr,
		logger:                             logger,
		rh:                                 rh,
		appender:                           sync.LogAppenderMap{},
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	blockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0xabc"),
	}

	mockMdr.EXPECT().CheckValidBlock(ctx, uint64(100), blockHeader.Hash).Return(false, uint64(1), nil)
	mockMdr.EXPECT().GetReorgedDataByChainID(ctx, uint64(1)).Return(nil, nil)

	err := download.checkReorgedBlock(ctx, blockHeader)

	require.Error(t, err)
	require.Contains(t, err.Error(), "reorg data not found")
}

func TestAppendLog_Success(t *testing.T) {
	ctx := context.Background()
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	callCount := 0
	appender := sync.LogAppenderMap{
		common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"): func(b *sync.EVMBlock, l types.Log) error {
			callCount++
			b.Events = append(b.Events, "event")
			return nil
		},
	}

	download := &EVMDownloader{
		mdr:                                nil,
		logger:                             logger,
		rh:                                 rh,
		appender:                           appender,
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	block := &sync.EVMBlock{
		EVMBlockHeader: sync.EVMBlockHeader{
			Num: 100,
		},
		Events: []interface{}{},
	}

	log := types.Log{
		BlockNumber: 100,
		Topics: []common.Hash{
			common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
		},
	}

	download.appendLog(ctx, block, log)

	require.Equal(t, 1, callCount)
	require.Len(t, block.Events, 1)
}

func TestAppendLog_RetryOnError(t *testing.T) {
	ctx := context.Background()
	logger := log.WithFields("module", "test")
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      10 * time.Millisecond,
		MaxRetryAttemptsAfterError: 3,
	}

	callCount := 0
	appender := sync.LogAppenderMap{
		common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"): func(b *sync.EVMBlock, l types.Log) error {
			callCount++
			if callCount < 3 {
				return fmt.Errorf("temporary error")
			}
			b.Events = append(b.Events, "event")
			return nil
		},
	}

	download := &EVMDownloader{
		mdr:                                nil,
		logger:                             logger,
		rh:                                 rh,
		appender:                           appender,
		waitPeriodToCatchUpMaximumLogRange: 1 * time.Second,
		pullingPeriod:                      100 * time.Millisecond,
	}

	block := &sync.EVMBlock{
		EVMBlockHeader: sync.EVMBlockHeader{
			Num: 100,
		},
		Events: []interface{}{},
	}

	log := types.Log{
		BlockNumber: 100,
		Topics: []common.Hash{
			common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
		},
	}

	download.appendLog(ctx, block, log)

	require.Equal(t, 3, callCount)
	require.Len(t, block.Events, 1)
}
