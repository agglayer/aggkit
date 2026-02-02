package multidownloader

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/etherman"
	mockethermantypes "github.com/agglayer/aggkit/etherman/types/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdrsync "github.com/agglayer/aggkit/multidownloader/sync"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	mockmdrtypes "github.com/agglayer/aggkit/multidownloader/types/mocks"
	aggkitsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocktypes "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const storagePath = "../tmp/ut/"
const runASyncer = true

type testProcessor struct {
	lastBlock *aggkittypes.BlockHeader
}

func (tp *testProcessor) GetLastProcessedBlockHeader(ctx context.Context) (*aggkittypes.BlockHeader, error) {
	return tp.lastBlock, nil
}
func (tp *testProcessor) ProcessBlock(ctx context.Context, block aggkitsync.Block) error {
	log.Infof("PROCESSOR: Processing block number %d", block.Num)
	tp.lastBlock = &aggkittypes.BlockHeader{
		Number: block.Num,
		Hash:   block.Hash,
	}
	return nil
}
func (tp *testProcessor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	log.Infof("PROCESSOR: Reorg from block number %d", firstReorgedBlock)
	return nil
}

func TestEVMMultidownloader(t *testing.T) {
	t.Skip("code to test/debug not real unittest - requires external dependencies (l1infotreesync causes import cycle)")

	cfgLog := log.Config{
		Environment: "development",
		Level:       "info",
		Outputs:     []string{"stderr"},
	}
	log.Init(cfgLog)
	l1url := os.Getenv("L1URL")
	ethRawClient, err := ethclient.Dial(l1url)
	require.NoError(t, err)
	ethClient := etherman.NewDefaultEthClient(ethRawClient, ethRawClient.Client(), nil)
	ethRPCClient, err := rpc.DialContext(t.Context(), l1url)
	require.NoError(t, err)

	block, err := ethClient.CustomHeaderByNumber(t.Context(), nil) // Test connection
	require.NoError(t, err)
	log.Infof("Connected to Ethereum. Current block: %d", block.Number)

	logger := log.WithFields("test", "test")

	db, err := storage.NewMultidownloaderStorage(logger, storage.MultidownloaderStorageConfig{
		DBPath: storagePath + "mdr_test.sqlite",
	})
	require.NoError(t, err)
	cfg := Config{
		BlockChunkSize:                  5000,
		MaxParallelBlockHeaderRetrieval: 50,
		BlockFinality:                   aggkittypes.FinalizedBlock,
		WaitPeriodToCheckCatchUp:        types.NewDuration(time.Second),
		PeriodToCheckReorgs:             types.NewDuration(time.Second * 10),
	}

	mdr, err := NewEVMMultidownloader(logger,
		cfg, "l1", ethClient, ethRPCClient,
		db, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, mdr)
	err = mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID: "test_syncer",
		ContractAddresses: []common.Address{
			common.HexToAddress("0x2968d6d736178f8fe7393cc33c87f29d9c287e78"), // GERManager
			common.HexToAddress("0xe2ef6215adc132df6913c8dd16487abf118d1764"), // RollupManager
		},
		FromBlock: 5157574,
		ToBlock:   aggkittypes.LatestBlock,
	})
	require.NoError(t, err)

	ctx := context.TODO()

	var syncer *mdrsync.EVMDriver
	if runASyncer == true {
		logger := log.WithFields("syncer", "test")
		rh := &aggkitsync.RetryHandler{
			RetryAfterErrorPeriod:      time.Second,
			MaxRetryAttemptsAfterError: 0,
		}
		downloader := mdrsync.NewDownloader(
			mdr,
			logger,
			rh,
			nil, // appender,
			time.Second,
			time.Second,
		)
		syncerConfig := aggkittypes.SyncerConfig{
			SyncerID: "l1infotree_syncer_test",
			ContractAddresses: []common.Address{
				common.HexToAddress("0x2968d6d736178f8fe7393cc33c87f29d9c287e78"), // GlobalExitRootAddr
				common.HexToAddress("0xe2ef6215adc132df6913c8dd16487abf118d1764"), // RollupManager
			},
			FromBlock: 5157574,
			ToBlock:   aggkittypes.LatestBlock,
		}
		processor := &testProcessor{}
		syncer = mdrsync.NewEVMDriver(logger, processor, downloader, syncerConfig,
			100, rh, nil)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := aggkitcommon.TimeTracker{}
		timer.Start()
		err = mdr.Start(ctx)
		timer.Stop()
		log.Infof("Multidownloader sync finished in %s. err: %w", timer.String(), err)
		require.NoError(t, err)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := aggkitcommon.TimeTracker{}
		timer.Start()
		if syncer != nil {
			syncer.Sync(t.Context())
		}
		timer.Stop()
		log.Infof("L1InfoTree sync finished in %s", timer.String())
	}()
	wg.Wait()
}

func TestEVMMultidownloaderExploratoryBatchRequests(t *testing.T) {
	t.Skip("it's a exploratory test for batch requests - requires external dependencies")
	/* Commented out to avoid import cycles
	l1url := os.Getenv("L1URL")
	ethClient, err := rpc.DialContext(t.Context(), l1url)
	require.NoError(t, err)
	var blockNumber string
	var chainID string

	var latestBlock aggkittypes.BlockHeader
	batch := []rpc.BatchElem{
		{
			Method: "eth_blockNumber",
			Args:   []interface{}{},
			Result: &blockNumber,
		},
		{
			Method: "eth_chainId",
			Args:   []interface{}{},
			Result: &chainID,
		},
		{
			Method: "eth_getBlockByNumber",
			Args: []interface{}{
				"0x37", // número de bloque en formato hex o palabra clave
				false,  // incluir transacciones completas
			},
			Result: &latestBlock,
		},
	}

	err = ethClient.BatchCallContext(t.Context(), batch)
	require.NoError(t, err)

	log.Infof("blockNumber: %s, chainID: %s", blockNumber, chainID)
	log.Infof("latestBlock: %+v", latestBlock)
	*/
}

func TestDownloaderParellelvsBatch(t *testing.T) {
	t.Skip("it's a benchmarking test - requires external dependencies")
	/* Commented out to avoid import cycles
	l1url := os.Getenv("L1URL")
	ethClient, err := ethclient.Dial(l1url)
	require.NoError(t, err)
	ethRPCClient, err := rpc.DialContext(t.Context(), l1url)
	require.NoError(t, err)
	ethClientWrapped := etherman.NewDefaultEthClient(ethClient, ethRPCClient, nil)

	blockNumbersMap := make([]uint64, 0)
	var blockNumbersSlice []uint64
	initialBlock := uint64(1)
	for i := initialBlock; i < initialBlock+923; i++ {
		blockNumbersMap = append(blockNumbersMap, i)
		blockNumbersSlice = append(blockNumbersSlice, i)
	}
	logger := log.WithFields("test", "test")

	start := time.Now()
	headersBatch, err := etherman.RetrieveBlockHeaders(t.Context(), logger, nil, ethRPCClient, blockNumbersMap, 10)
	require.NoError(t, err)
	durationBatch := time.Since(start)
	log.Infof("BatchMode took %s", durationBatch.String())

	start = time.Now()
	headersParallel, err := etherman.RetrieveBlockHeaders(t.Context(), logger, ethClientWrapped, nil, blockNumbersMap, 20)
	require.NoError(t, err)
	durationParallel := time.Since(start)
	log.Infof("Parallel RPC took %s", durationParallel.String())

	require.Equal(t, len(headersParallel), len(headersBatch))
	for _, blockNumber := range blockNumbersSlice {
		headerP := getBlockHeader(blockNumber, headersParallel)
		headerB := getBlockHeader(blockNumber, headersBatch)
		require.NotNil(t, headerP)
		require.NotNil(t, headerB)
		require.Equal(t, headerP.Hash, headerB.Hash)
	}
	*/
}

// getBlockHeader is only used in skipped tests
// func getBlockHeader(bn uint64, headers []*aggkittypes.BlockHeader) *aggkittypes.BlockHeader {
// 	for _, h := range headers {
// 		if h.Number == bn {
// 			return h
// 		}
// 	}
// 	return nil
// }

func TestEVMMultidownloader_NewEVMMultidownloader(t *testing.T) {
	logger := log.WithFields("test", "evm_multidownloader_test")
	cfg := NewConfigDefault("test.sqlite", t.TempDir())
	sut, err := NewEVMMultidownloader(logger, cfg, "test", nil, nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, sut)
	require.NotNil(t, sut.blockNotifierManager)
	require.NotNil(t, sut.storage)
}

func TestEVMMultidownloaderExtractSuggestedBlockRangeFromErrorMsg(t *testing.T) {
	br := extractSuggestedBlockRangeFromErrorMsg("Query returned more than 20000 results. Try with this block range [0x852c16, 0x853273].")
	require.NotNil(t, br)
	require.Equal(t, uint64(8727574), br.FromBlock)
	require.Equal(t, uint64(8729203), br.ToBlock)
}

func TestEVMMultidownloader_RegisterSyncer(t *testing.T) {
	t.Run("check Addresses", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		err := testData.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
			SyncerID: "syncer1",
			ContractAddresses: []common.Address{
				common.HexToAddress("0x1"),
			},
			FromBlock: 100,
			ToBlock:   aggkittypes.LatestBlock,
		})
		require.NoError(t, err)

		require.Equal(t, []common.Address{common.HexToAddress("0x1")}, testData.mdr.syncersConfig.Addresses(
			aggkitcommon.NewBlockRange(100, 200),
		))
	})

	t.Run("try to add after initialize", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil)
		err := testData.mdr.Initialize(t.Context())
		require.NoError(t, err)
		err = testData.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
			SyncerID: "syncer2",
		})
		require.Error(t, err)
	})
}

func TestEVMMultidownloader_GetRPCServices(t *testing.T) {
	t.Run("returns correct RPC service", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)

		services := testData.mdr.GetRPCServices()

		require.Len(t, services, 1)
		require.Equal(t, "multidownloader-test", services[0].Name)
		require.NotNil(t, services[0].Service)

		// Verify the service is of the correct type
		_, ok := services[0].Service.(*EVMMultidownloaderRPC)
		require.True(t, ok, "Service should be of type *EVMMultidownloaderRPC")
	})

	t.Run("service name includes multidownloader name", func(t *testing.T) {
		logger := log.WithFields("test", "evm_multidownloader_test")
		cfg := Config{
			BlockChunkSize:                  5000,
			MaxParallelBlockHeaderRetrieval: 50,
			BlockFinality:                   aggkittypes.FinalizedBlock,
			WaitPeriodToCheckCatchUp:        types.NewDuration(time.Second),
		}
		ethClient := mocktypes.NewBaseEthereumClienter(t)
		db, err := storage.NewMultidownloaderStorage(logger, storage.MultidownloaderStorageConfig{
			DBPath: cfg.StoragePath,
		})
		require.NoError(t, err)

		customName := "custom-name"
		mdr, err := NewEVMMultidownloader(logger, cfg, customName, ethClient, nil, db, nil, nil)
		require.NoError(t, err)

		services := mdr.GetRPCServices()

		require.Len(t, services, 1)
		require.Equal(t, "multidownloader-"+customName, services[0].Name)
	})
}

func TestEVMMultidownloader_Initialize(t *testing.T) {
	t.Run("successful initialization", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil)
		err := testData.mdr.Initialize(t.Context())
		require.NoError(t, err)
	})

	t.Run("failed initialization due to ChainID error", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mockEthClient.EXPECT().ChainID(mock.Anything).Return(nil, fmt.Errorf("chain ID error"))
		err := testData.mdr.Initialize(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "chain ID error")
	})
	t.Run("double initialization", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil)
		err := testData.mdr.Initialize(t.Context())
		require.NoError(t, err)

		// Second initialization should fail
		err = testData.mdr.Initialize(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "already initialized")
	})
}

func TestEVMMultidownloader_StepSafe(t *testing.T) {
	testData := newEVMMultidownloaderTestData(t, false)
	testData.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil)
	err := testData.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID: "syncer1",
		ContractAddresses: []common.Address{
			common.HexToAddress("0x1"),
		},
		FromBlock: 100,
		ToBlock:   aggkittypes.FinalizedBlock,
	})
	require.NoError(t, err)
	testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.FinalizedBlock).
		Return(uint64(150), nil).Maybe()
	testData.mockEthClient.EXPECT().FilterLogs(mock.Anything, mock.Anything).Return([]ethtypes.Log{}, nil).Maybe()
	err = testData.mdr.Initialize(t.Context())
	require.NoError(t, err)

	finished, err := testData.mdr.StepSafe(t.Context())
	require.NoError(t, err)
	require.True(t, finished)

	ctx, cancel := context.WithCancel(context.TODO())
	cancel()
	_, err = testData.mdr.StepSafe(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestEVMMultidownloader_Start(t *testing.T) {
	t.Run("initialization error is returned", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, true)

		// Verify not initialized
		require.False(t, testData.mdr.IsInitialized())

		// Mock ChainID to fail
		expectedErr := fmt.Errorf("chain ID error")
		testData.mockEthClient.EXPECT().ChainID(mock.Anything).Return(nil, expectedErr).Once()

		ctx := context.Background()

		// Start should try to initialize and return the error
		err := testData.mdr.Start(ctx)

		// Should return the initialization error
		require.Error(t, err)
		require.Contains(t, err.Error(), "chain ID error")

		// Verify it was not initialized
		require.False(t, testData.mdr.IsInitialized())
	})

	// Note: Testing the full Start() loop with auto-initialization is complex because Start()
	// has an infinite loop and requires extensive mocking. The key behavior is tested above:
	// - If not initialized, Start() calls Initialize()
	// - If Initialize() fails, Start() returns the error
	// For integration testing of the full Start() flow, see e2e_test.go
}

type testDataEVMMultidownloader struct {
	mockEthClient            *mocktypes.BaseEthereumClienter
	mdr                      *EVMMultidownloader
	realStorage              *storage.MultidownloaderStorage
	mockStorage              *mockmdrtypes.Storager
	usedStorage              mdrtypes.Storager
	mockBlockNotifierManager *mockethermantypes.BlockNotifierManager
}

func (td *testDataEVMMultidownloader) FakeInitialized(t *testing.T) {
	t.Helper()
	td.mdr.state = NewEmptyState()
}

func (td *testDataEVMMultidownloader) MockInitialize(t *testing.T, chainID uint64) {
	t.Helper()
	chainIDBig := big.NewInt(0).SetUint64(chainID)
	td.mockEthClient.EXPECT().ChainID(mock.Anything).Return(chainIDBig, nil).Maybe()
	if td.mockStorage != nil {
		td.mockStorage.EXPECT().GetValue(mock.Anything, mock.Anything, mock.Anything).Return("", db.ErrNotFound).Maybe()
		td.mockStorage.EXPECT().InsertValue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		td.mockStorage.EXPECT().UpsertSyncerConfigs(mock.Anything, mock.Anything).Return(nil).Maybe()
		td.mockStorage.EXPECT().GetSyncedBlockRangePerContract(mock.Anything).Return(mdrtypes.NewSetSyncSegment(), nil).Maybe()
	}
	if td.mockBlockNotifierManager != nil {
		td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).Return(uint64(200), nil).Maybe()
	}
}

func newEVMMultidownloaderTestData(t *testing.T, mockStorage bool) *testDataEVMMultidownloader {
	t.Helper()
	logger := log.WithFields("test", "evm_multidownloader_test")
	cfg := Config{
		BlockChunkSize:                  5000,
		MaxParallelBlockHeaderRetrieval: 50,
		BlockFinality:                   aggkittypes.FinalizedBlock,
		WaitPeriodToCheckCatchUp:        types.NewDuration(time.Second),
	}
	ethClient := mocktypes.NewBaseEthereumClienter(t)
	mockBlockNotifierManager := mockethermantypes.NewBlockNotifierManager(t)
	var mockDB *mockmdrtypes.Storager
	var realDB *storage.MultidownloaderStorage
	var useDB mdrtypes.Storager
	var err error
	if mockStorage {
		mockDB = mockmdrtypes.NewStorager(t)
		useDB = mockDB
	} else {
		realDB, err = storage.NewMultidownloaderStorage(logger, storage.MultidownloaderStorageConfig{
			DBPath: cfg.StoragePath,
		})
		require.NoError(t, err)
		useDB = realDB
	}
	mdr, err := NewEVMMultidownloader(logger, cfg, "test", ethClient, nil, useDB, mockBlockNotifierManager, nil)
	require.NoError(t, err)
	return &testDataEVMMultidownloader{
		mockEthClient:            ethClient,
		mdr:                      mdr,
		realStorage:              realDB,
		mockStorage:              mockDB,
		usedStorage:              useDB,
		mockBlockNotifierManager: mockBlockNotifierManager,
	}
}

func TestEVMMultidownloader_StartStop(t *testing.T) {
	t.Run("Stop without Start returns error", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		ctx := context.Background()
		err := data.mdr.Stop(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not running")
	})

	t.Run("Start and Stop successfully", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)

		// Setup mocks for Start loop
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(100), nil).Maybe()
		data.mockStorage.EXPECT().NewTx(mock.Anything).Return(nil, fmt.Errorf("stop test")).Maybe()
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mock.Anything, mock.Anything).Return(nil, nil).Maybe()

		// Start in background
		ctx := context.Background()
		var startErr error
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			startErr = data.mdr.Start(ctx)
		}()

		// Give it time to start and run a few iterations
		time.Sleep(50 * time.Millisecond)

		// Stop should succeed
		stopCtx := context.Background()
		err := data.mdr.Stop(stopCtx)
		require.NoError(t, err)

		// Wait for Start to finish
		wg.Wait()
		// Start should return context.Canceled (clean shutdown via context cancellation)
		require.ErrorIs(t, startErr, context.Canceled)
	})

	t.Run("Start twice returns error", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)

		// Setup mocks
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(100), nil).Maybe()
		data.mockStorage.EXPECT().NewTx(mock.Anything).Return(nil, fmt.Errorf("stop test")).Maybe()
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mock.Anything, mock.Anything).Return(nil, nil).Maybe()

		// Start first time
		ctx := context.Background()
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = data.mdr.Start(ctx)
		}()

		// Give it time to start
		time.Sleep(50 * time.Millisecond)

		// Try to start again - should fail
		err := data.mdr.Start(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "already running")

		// Cleanup
		_ = data.mdr.Stop(ctx)
		wg.Wait()
	})

	t.Run("Stop waits for Start to complete", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)

		// Setup mocks
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(100), nil).Maybe()
		data.mockStorage.EXPECT().NewTx(mock.Anything).Return(nil, fmt.Errorf("mock error")).Maybe()
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mock.Anything, mock.Anything).Return(nil, nil).Maybe()

		// Start in background
		ctx := context.Background()
		var startCompleted atomic.Bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = data.mdr.Start(ctx)
			startCompleted.Store(true)
		}()

		// Give it time to start
		time.Sleep(50 * time.Millisecond)

		// Stop and verify it waits
		stopStartTime := time.Now()
		stopCtx := context.Background()
		err := data.mdr.Stop(stopCtx)
		stopDuration := time.Since(stopStartTime)

		require.NoError(t, err)
		require.True(t, startCompleted.Load(), "Start should have completed before Stop returns")
		require.Greater(t, stopDuration, time.Duration(0), "Stop should take some time waiting for Start")

		wg.Wait()
	})
}
