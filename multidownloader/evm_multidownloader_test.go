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
	dbmocks "github.com/agglayer/aggkit/db/mocks"
	"github.com/agglayer/aggkit/etherman"
	mockethermantypes "github.com/agglayer/aggkit/etherman/types/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdrsync "github.com/agglayer/aggkit/multidownloader/sync"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
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

func (tp *testProcessor) ProcessBlocks(ctx context.Context, blocks *mdrsynctypes.DownloadResult) error {
	if blocks == nil || len(blocks.Data) == 0 {
		return nil
	}
	for _, block := range blocks.Data {
		if err := tp.ProcessBlock(ctx, block); err != nil {
			return err
		}
	}
	return nil
}
func (tp *testProcessor) ProcessBlock(ctx context.Context, block *aggkitsync.EVMBlock) error {
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

func TestEVMMultidownloaderExploratory(t *testing.T) {
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
		downloader := mdrsync.NewEVMDownloader(
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
			fromBlock := uint64(5157574)
			syncer.Sync(t.Context(), &fromBlock)
		}
		timer.Stop()
		log.Infof("L1InfoTree sync finished in %s", timer.String())
	}()
	wg.Wait()
}

func TestPerformanceDownloaderParallelvsBatch(t *testing.T) {
	t.Skip("it's a benchmarking test - requires external dependencies")

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
	require.True(t, headersBatch.Success())
	durationBatch := time.Since(start)
	log.Infof("BatchMode took %s", durationBatch.String())

	start = time.Now()
	headersParallel, err := etherman.RetrieveBlockHeaders(t.Context(), logger, ethClientWrapped, nil, blockNumbersMap, 20)
	require.NoError(t, err)
	require.True(t, headersParallel.Success())
	durationParallel := time.Since(start)
	log.Infof("Parallel RPC took %s", durationParallel.String())

	require.Equal(t, len(headersParallel.Headers), len(headersBatch.Headers))
	for _, blockNumber := range blockNumbersSlice {
		headerP, existsP := headersParallel.Headers[blockNumber]
		headerB, existsB := headersBatch.Headers[blockNumber]
		require.True(t, existsP)
		require.True(t, existsB)
		require.NotNil(t, headerP)
		require.NotNil(t, headerB)
		require.Equal(t, headerP.Hash, headerB.Hash)
	}
}

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

	t.Run("Start() and reorg", func(t *testing.T) {
		testData := newEVMMultidownloaderTestData(t, false)
		testData.mdr.debug = &EVMMultidownloaderDebug{} // Enable debug to test that reorgs are checked even in debug mode
		// Fake initialization
		testData.mdr.state = NewEmptyState()
		ctx := context.Background()
		testData.mdr.debug.ForceReorg(1234)

		testData.mockReorgProcessor.EXPECT().ProcessReorg(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		// It starts, execute 1 loop that do a reorg and then return
		err := testData.mdr.startNumLoops(ctx, 1)
		// Should return no error
		require.NoError(t, err)
	})
}

type testDataEVMMultidownloader struct {
	mockEthClient            *mocktypes.BaseEthereumClienter
	mdr                      *EVMMultidownloader
	realStorage              *storage.MultidownloaderStorage
	mockStorage              *mockmdrtypes.Storager
	usedStorage              mdrtypes.Storager
	mockBlockNotifierManager *mockethermantypes.BlockNotifierManager
	mockReorgProcessor       *mockmdrtypes.ReorgProcessor
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
	mockReorgProcessor := mockmdrtypes.NewReorgProcessor(t)
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
	mdr, err := NewEVMMultidownloader(logger, cfg, "test", ethClient, nil,
		useDB, mockBlockNotifierManager, mockReorgProcessor)
	require.NoError(t, err)
	return &testDataEVMMultidownloader{
		mockEthClient:            ethClient,
		mdr:                      mdr,
		realStorage:              realDB,
		mockStorage:              mockDB,
		usedStorage:              useDB,
		mockBlockNotifierManager: mockBlockNotifierManager,
		mockReorgProcessor:       mockReorgProcessor,
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

func TestEVMMultidownloader_MoveUnsafeToSafeIfPossible(t *testing.T) {
	t.Run("successful move from unsafe to safe", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)
		ctx := context.Background()

		// Mock finalized block number
		finalizedBlockNumber := uint64(200)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, data.mdr.cfg.BlockFinality).
			Return(finalizedBlockNumber, nil).Once()

		// Mock transaction
		mockTx := dbmocks.NewTxer(t)
		mockTx.EXPECT().Rollback().Return(nil).Maybe()
		mockTx.EXPECT().Commit().Return(nil).Once()
		data.mockStorage.EXPECT().NewTx(mock.Anything).Return(mockTx, nil).Once()

		// Create Ethereum headers that will be returned by RPC
		header195 := &ethtypes.Header{
			Number:     big.NewInt(195),
			ParentHash: common.HexToHash("0x194"),
			Time:       1234567890,
		}
		header196 := &ethtypes.Header{
			Number:     big.NewInt(196),
			ParentHash: common.HexToHash("0x195"),
			Time:       1234567891,
		}

		// Mock unsafe blocks with the same hashes that will be calculated from the Ethereum headers
		unsafeBlocks := aggkittypes.ListBlockHeaders{
			&aggkittypes.BlockHeader{Number: 195, Hash: header195.Hash()},
			&aggkittypes.BlockHeader{Number: 196, Hash: header196.Hash()},
		}
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mockTx, &finalizedBlockNumber).
			Return(unsafeBlocks, nil).Once()

		// Mock RPC block headers retrieval for reorg detection
		data.mockEthClient.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(195)).Return(header195, nil).Once()
		data.mockEthClient.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(196)).Return(header196, nil).Once()

		// Mock update to finalized
		data.mockStorage.EXPECT().UpdateBlockToFinalized(mockTx, []uint64{195, 196}).Return(nil).Once()

		err := data.mdr.moveUnsafeToSafeIfPossible(ctx)
		require.NoError(t, err)
	})

	t.Run("no unsafe blocks to move", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)
		ctx := context.Background()

		// Mock finalized block number
		finalizedBlockNumber := uint64(200)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(ctx, data.mdr.cfg.BlockFinality).
			Return(finalizedBlockNumber, nil).Once()

		// Mock transaction
		mockTx := dbmocks.NewTxer(t)
		mockTx.EXPECT().Rollback().Return(nil).Maybe()
		data.mockStorage.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()

		// Mock no unsafe blocks
		emptyBlocks := aggkittypes.ListBlockHeaders{}
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mockTx, &finalizedBlockNumber).
			Return(emptyBlocks, nil).Once()

		err := data.mdr.moveUnsafeToSafeIfPossible(ctx)
		require.NoError(t, err)
	})

	t.Run("error getting finalized block number", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)
		ctx := context.Background()

		// Mock finalized block number error
		expectedErr := fmt.Errorf("finalized block error")
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(ctx, data.mdr.cfg.BlockFinality).
			Return(uint64(0), expectedErr).Once()

		err := data.mdr.moveUnsafeToSafeIfPossible(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get finalized block number")
		require.Contains(t, err.Error(), expectedErr.Error())
	})

	t.Run("error creating transaction", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)
		ctx := context.Background()

		// Mock finalized block number
		finalizedBlockNumber := uint64(200)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(ctx, data.mdr.cfg.BlockFinality).
			Return(finalizedBlockNumber, nil).Once()

		// Mock transaction creation error
		expectedErr := fmt.Errorf("tx creation error")
		data.mockStorage.EXPECT().NewTx(ctx).Return(nil, expectedErr).Once()

		err := data.mdr.moveUnsafeToSafeIfPossible(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot create new tx")
		require.Contains(t, err.Error(), expectedErr.Error())
	})

	t.Run("error getting unsafe blocks", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)
		ctx := context.Background()

		// Mock finalized block number
		finalizedBlockNumber := uint64(200)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(ctx, data.mdr.cfg.BlockFinality).
			Return(finalizedBlockNumber, nil).Once()

		// Mock transaction
		mockTx := dbmocks.NewTxer(t)
		mockTx.EXPECT().Rollback().Return(nil).Once()
		data.mockStorage.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()

		// Mock error getting unsafe blocks
		expectedErr := fmt.Errorf("get blocks error")
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mockTx, &finalizedBlockNumber).
			Return(nil, expectedErr).Once()

		err := data.mdr.moveUnsafeToSafeIfPossible(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get unsafe block bases")
		require.Contains(t, err.Error(), expectedErr.Error())
	})

	t.Run("reorg detected during move", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)
		ctx := context.Background()

		// Mock finalized block number
		finalizedBlockNumber := uint64(200)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, data.mdr.cfg.BlockFinality).
			Return(finalizedBlockNumber, nil).Once()

		// Mock transaction
		mockTx := dbmocks.NewTxer(t)
		mockTx.EXPECT().Rollback().Return(nil).Once()
		data.mockStorage.EXPECT().NewTx(mock.Anything).Return(mockTx, nil).Once()

		// Mock unsafe blocks with a specific hash
		storageHash := common.HexToHash("0x195")
		unsafeBlocks := aggkittypes.ListBlockHeaders{
			&aggkittypes.BlockHeader{Number: 195, Hash: storageHash},
		}
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mockTx, &finalizedBlockNumber).
			Return(unsafeBlocks, nil).Once()

		// Mock RPC returns header with different hash (reorg detected)
		headerDifferent := &ethtypes.Header{
			Number:     big.NewInt(195),
			ParentHash: common.HexToHash("0xDIFFERENT"),
			Time:       9999999,
		}
		data.mockEthClient.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(195)).Return(headerDifferent, nil).Once()

		err := data.mdr.moveUnsafeToSafeIfPossible(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "error detecting reorgs")
		// Check it's a reorg error
		reorgErr := mdrtypes.CastDetectedReorgError(err)
		require.NotNil(t, reorgErr)
	})

	t.Run("error updating blocks to finalized", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)
		ctx := context.Background()

		// Mock finalized block number
		finalizedBlockNumber := uint64(200)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, data.mdr.cfg.BlockFinality).
			Return(finalizedBlockNumber, nil).Once()

		// Mock transaction
		mockTx := dbmocks.NewTxer(t)
		mockTx.EXPECT().Rollback().Return(nil).Once()
		data.mockStorage.EXPECT().NewTx(mock.Anything).Return(mockTx, nil).Once()

		// Create Ethereum header
		header195 := &ethtypes.Header{
			Number:     big.NewInt(195),
			ParentHash: common.HexToHash("0x194"),
			Time:       1234567890,
		}

		// Mock unsafe blocks with matching hash
		unsafeBlocks := aggkittypes.ListBlockHeaders{
			&aggkittypes.BlockHeader{Number: 195, Hash: header195.Hash()},
		}
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mockTx, &finalizedBlockNumber).
			Return(unsafeBlocks, nil).Once()

		// Mock RPC block headers (no reorg)
		data.mockEthClient.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(195)).Return(header195, nil).Once()

		// Mock update error
		expectedErr := fmt.Errorf("update error")
		data.mockStorage.EXPECT().UpdateBlockToFinalized(mockTx, []uint64{195}).Return(expectedErr).Once()

		err := data.mdr.moveUnsafeToSafeIfPossible(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot update is_final for block bases")
		require.Contains(t, err.Error(), expectedErr.Error())
	})

	t.Run("error committing transaction", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)
		ctx := context.Background()

		// Mock finalized block number
		finalizedBlockNumber := uint64(200)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, data.mdr.cfg.BlockFinality).
			Return(finalizedBlockNumber, nil).Once()

		// Mock transaction
		mockTx := dbmocks.NewTxer(t)
		mockTx.EXPECT().Rollback().Return(nil).Maybe()
		expectedErr := fmt.Errorf("commit error")
		mockTx.EXPECT().Commit().Return(expectedErr).Once()
		data.mockStorage.EXPECT().NewTx(mock.Anything).Return(mockTx, nil).Once()

		// Create Ethereum header
		header195 := &ethtypes.Header{
			Number:     big.NewInt(195),
			ParentHash: common.HexToHash("0x194"),
			Time:       1234567890,
		}

		// Mock unsafe blocks with matching hash
		unsafeBlocks := aggkittypes.ListBlockHeaders{
			&aggkittypes.BlockHeader{Number: 195, Hash: header195.Hash()},
		}
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mockTx, &finalizedBlockNumber).
			Return(unsafeBlocks, nil).Once()

		// Mock RPC block headers (no reorg)
		data.mockEthClient.EXPECT().HeaderByNumber(mock.Anything, big.NewInt(195)).Return(header195, nil).Once()

		// Mock update success
		data.mockStorage.EXPECT().UpdateBlockToFinalized(mockTx, []uint64{195}).Return(nil).Once()

		err := data.mdr.moveUnsafeToSafeIfPossible(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot commit tx")
		require.Contains(t, err.Error(), expectedErr.Error())
	})
}

func TestEVMMultidownloader_StartStep(t *testing.T) {
	t.Run("error in MoveUnsafeToSafeIfPossible", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)
		ctx := context.Background()

		// Mock updateTargetBlockNumber success (no pending blocks to update)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(100), nil).Maybe()

		// Mock MoveUnsafeToSafeIfPossible to fail
		expectedErr := fmt.Errorf("move unsafe error")
		data.mockStorage.EXPECT().NewTx(mock.Anything).Return(nil, expectedErr).Once()

		err := data.mdr.StartStep(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot create new tx")
	})

	t.Run("error in checkReorgsUnsafeZone", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)
		ctx := context.Background()

		// Mock updateTargetBlockNumber success
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(100), nil).Maybe()

		// Mock MoveUnsafeToSafeIfPossible success (no unsafe blocks)
		mockTx := dbmocks.NewTxer(t)
		mockTx.EXPECT().Rollback().Return(nil).Maybe()
		data.mockStorage.EXPECT().NewTx(mock.Anything).Return(mockTx, nil).Once()
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mockTx, mock.Anything).
			Return(aggkittypes.ListBlockHeaders{}, nil).Once()

		// Mock checkReorgsUnsafeZone to fail
		expectedErr := fmt.Errorf("check reorgs error")
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mock.Anything, mock.Anything).
			Return(nil, expectedErr).Once()

		err := data.mdr.StartStep(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "check reorgs error")
	})

	t.Run("no pending blocks - waits for new blocks", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		data.FakeInitialized(t)

		// Create a context with cancel to avoid waiting forever
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Mock updateTargetBlockNumber success
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(100), nil).Maybe()

		// Mock MoveUnsafeToSafeIfPossible success
		mockTx := dbmocks.NewTxer(t)
		mockTx.EXPECT().Rollback().Return(nil).Maybe()
		data.mockStorage.EXPECT().NewTx(mock.Anything).Return(mockTx, nil).Once()
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mockTx, mock.Anything).
			Return(aggkittypes.ListBlockHeaders{}, nil).Once()

		// Mock checkReorgsUnsafeZone success (no unsafe blocks)
		data.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mock.Anything, mock.Anything).
			Return(aggkittypes.ListBlockHeaders{}, nil).Once()

		// Mock WaitForNewLatestBlocks - GetBlockHeaderByNumber will fail
		data.mockStorage.EXPECT().GetBlockHeaderByNumber(mock.Anything, mock.Anything).
			Return(nil, mdrtypes.NotFinalized, fmt.Errorf("no blocks yet")).Once()

		err := data.mdr.StartStep(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get block header")
	})
}

func TestGetBlockNumbers(t *testing.T) {
	t.Run("empty logs", func(t *testing.T) {
		logs := []ethtypes.Log{}
		result := getBlockNumbers(logs)
		require.Empty(t, result)
	})

	t.Run("single log", func(t *testing.T) {
		logs := []ethtypes.Log{
			{BlockNumber: 100},
		}
		result := getBlockNumbers(logs)
		require.Len(t, result, 1)
		require.Equal(t, uint64(100), result[0])
	})

	t.Run("multiple logs with unique block numbers", func(t *testing.T) {
		logs := []ethtypes.Log{
			{BlockNumber: 100},
			{BlockNumber: 101},
			{BlockNumber: 102},
		}
		result := getBlockNumbers(logs)
		require.Len(t, result, 3)
		require.Contains(t, result, uint64(100))
		require.Contains(t, result, uint64(101))
		require.Contains(t, result, uint64(102))
	})

	t.Run("multiple logs with duplicate block numbers", func(t *testing.T) {
		logs := []ethtypes.Log{
			{BlockNumber: 100},
			{BlockNumber: 100},
			{BlockNumber: 101},
			{BlockNumber: 101},
			{BlockNumber: 102},
		}
		result := getBlockNumbers(logs)
		require.Len(t, result, 3)
		require.Contains(t, result, uint64(100))
		require.Contains(t, result, uint64(101))
		require.Contains(t, result, uint64(102))
	})
}

func TestGetContracts(t *testing.T) {
	t.Run("empty log queries", func(t *testing.T) {
		queries := []mdrtypes.LogQuery{}
		result := getContracts(queries)
		require.Empty(t, result)
	})

	t.Run("single query with one address", func(t *testing.T) {
		addr1 := common.HexToAddress("0x1")
		queries := []mdrtypes.LogQuery{
			{Addrs: []common.Address{addr1}},
		}
		result := getContracts(queries)
		require.Len(t, result, 1)
		require.Contains(t, result, addr1)
	})

	t.Run("multiple queries with unique addresses", func(t *testing.T) {
		addr1 := common.HexToAddress("0x1")
		addr2 := common.HexToAddress("0x2")
		queries := []mdrtypes.LogQuery{
			{Addrs: []common.Address{addr1}},
			{Addrs: []common.Address{addr2}},
		}
		result := getContracts(queries)
		require.Len(t, result, 2)
		require.Contains(t, result, addr1)
		require.Contains(t, result, addr2)
	})

	t.Run("multiple queries with duplicate addresses", func(t *testing.T) {
		addr1 := common.HexToAddress("0x1")
		addr2 := common.HexToAddress("0x2")
		queries := []mdrtypes.LogQuery{
			{Addrs: []common.Address{addr1, addr2}},
			{Addrs: []common.Address{addr1}},
			{Addrs: []common.Address{addr2}},
		}
		result := getContracts(queries)
		require.Len(t, result, 2)
		require.Contains(t, result, addr1)
		require.Contains(t, result, addr2)
	})
}

func TestEVMMultidownloader_CheckIntegrityNewLogsBlockHeaders(t *testing.T) {
	t.Run("empty logs and headers", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, false)
		logs := []ethtypes.Log{}
		headers := aggkittypes.ListBlockHeaders{}

		err := data.mdr.checkIntegrityNewLogsBlockHeaders(logs, headers)
		require.NoError(t, err)
	})

	t.Run("matching logs and headers", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, false)

		hash100 := common.HexToHash("0x100")
		hash101 := common.HexToHash("0x101")

		logs := []ethtypes.Log{
			{BlockNumber: 100, BlockHash: hash100},
			{BlockNumber: 101, BlockHash: hash101},
		}
		headers := aggkittypes.ListBlockHeaders{
			&aggkittypes.BlockHeader{Number: 100, Hash: hash100},
			&aggkittypes.BlockHeader{Number: 101, Hash: hash101},
		}

		err := data.mdr.checkIntegrityNewLogsBlockHeaders(logs, headers)
		require.NoError(t, err)
	})

	t.Run("log with missing block header", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, false)

		hash100 := common.HexToHash("0x100")

		logs := []ethtypes.Log{
			{BlockNumber: 100, BlockHash: hash100},
			{BlockNumber: 101, BlockHash: common.HexToHash("0x101")},
		}
		headers := aggkittypes.ListBlockHeaders{
			&aggkittypes.BlockHeader{Number: 100, Hash: hash100},
		}

		err := data.mdr.checkIntegrityNewLogsBlockHeaders(logs, headers)
		require.Error(t, err)
		require.Contains(t, err.Error(), "block header for log block number 101 not found")
	})

	t.Run("log with mismatched hash", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, false)

		hash100 := common.HexToHash("0x100")
		differentHash := common.HexToHash("0xDIFFERENT")

		logs := []ethtypes.Log{
			{BlockNumber: 100, BlockHash: hash100},
		}
		headers := aggkittypes.ListBlockHeaders{
			&aggkittypes.BlockHeader{Number: 100, Hash: differentHash},
		}

		err := data.mdr.checkIntegrityNewLogsBlockHeaders(logs, headers)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not match block header hash")
	})
}

func TestEVMMultidownloader_IsPartiallyAvailable(t *testing.T) {
	t.Run("basic functionality", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, false)
		data.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(200), nil).Maybe()

		err := data.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
			SyncerID: "syncer1",
			ContractAddresses: []common.Address{
				common.HexToAddress("0x1"),
			},
			FromBlock: 100,
			ToBlock:   aggkittypes.FinalizedBlock,
		})
		require.NoError(t, err)

		err = data.mdr.Initialize(context.Background())
		require.NoError(t, err)

		// Query for blocks that are not yet synced
		query := mdrtypes.LogQuery{
			BlockRange: aggkitcommon.NewBlockRange(100, 200),
			Addrs:      []common.Address{common.HexToAddress("0x1")},
		}

		// The function should not panic and return valid values
		isPartial, partialQuery := data.mdr.IsPartiallyAvailable(query)
		// Since nothing is synced yet, it might be partially available or not available
		// We just verify it doesn't panic and returns consistent values
		if isPartial {
			require.NotNil(t, partialQuery)
		} else {
			require.Nil(t, partialQuery)
		}
	})
}

func TestEVMMultidownloader_GetLatestBlockNumber(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		ctx := context.Background()

		expectedBlockNumber := uint64(12345)
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(ctx, aggkittypes.LatestBlock).
			Return(expectedBlockNumber, nil).Once()

		blockNumber, err := data.mdr.GetLatestBlockNumber(ctx)
		require.NoError(t, err)
		require.Equal(t, expectedBlockNumber, blockNumber)
	})

	t.Run("error", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		ctx := context.Background()

		expectedErr := fmt.Errorf("block number error")
		data.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(ctx, aggkittypes.LatestBlock).
			Return(uint64(0), expectedErr).Once()

		blockNumber, err := data.mdr.GetLatestBlockNumber(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot get latest block")
		require.Equal(t, uint64(0), blockNumber)
	})
}

func TestEVMMultidownloader_ShowStatistics(t *testing.T) {
	t.Run("show statistics", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, false)
		// This should not panic
		data.mdr.ShowStatistics(1)
		data.mdr.ShowStatistics(10)
	})
}

// mockDataError is a mock implementation of ethrpc.DataError for testing
type mockDataError struct {
	msg  string
	data any
}

func (e *mockDataError) Error() string {
	return e.msg
}

func (e *mockDataError) ErrorCode() int {
	return -32000
}

func (e *mockDataError) ErrorData() any {
	return e.data
}

func Test_ethGetExtendedError(t *testing.T) {
	t.Run("nil error returns empty string", func(t *testing.T) {
		result := ethGetExtendedError(nil)
		require.Equal(t, "", result)
	})

	t.Run("non-DataError returns empty string", func(t *testing.T) {
		err := fmt.Errorf("regular error")
		result := ethGetExtendedError(err)
		require.Equal(t, "", result)
	})

	t.Run("DataError returns formatted error data", func(t *testing.T) {
		dataErr := &mockDataError{
			msg:  "query error",
			data: "Query returned more than 20000 results",
		}
		result := ethGetExtendedError(dataErr)
		require.Equal(t, "json_data: Query returned more than 20000 results", result)
	})
}

func Test_isEthClientErrorTooManyResults(t *testing.T) {
	t.Run("nil error returns false", func(t *testing.T) {
		result := isEthClientErrorTooManyResults(nil)
		require.False(t, result)
	})

	t.Run("regular error returns false", func(t *testing.T) {
		err := fmt.Errorf("regular error")
		result := isEthClientErrorTooManyResults(err)
		require.False(t, result)
	})

	t.Run("error with 'Response size exceeded' returns true", func(t *testing.T) {
		dataErr := &mockDataError{
			msg:  "query error",
			data: "Response size exceeded maximum limit",
		}
		result := isEthClientErrorTooManyResults(dataErr)
		require.True(t, result)
	})

	t.Run("error with 'Query returned more than' returns true", func(t *testing.T) {
		dataErr := &mockDataError{
			msg:  "query error",
			data: "Query returned more than 20000 results. Try with this block range [0x852c16, 0x853273].",
		}
		result := isEthClientErrorTooManyResults(dataErr)
		require.True(t, result)
	})
}

func Test_extractSuggestedBlockRangeFromError(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		result := extractSuggestedBlockRangeFromError(nil)
		require.Nil(t, result)
	})

	t.Run("non-too-many-results error returns nil", func(t *testing.T) {
		err := fmt.Errorf("regular error")
		result := extractSuggestedBlockRangeFromError(err)
		require.Nil(t, result)
	})

	t.Run("error with valid block range returns BlockRange", func(t *testing.T) {
		dataErr := &mockDataError{
			msg:  "query error",
			data: "Query returned more than 20000 results. Try with this block range [0x852c16, 0x853273].",
		}
		result := extractSuggestedBlockRangeFromError(dataErr)
		require.NotNil(t, result)
		require.Equal(t, uint64(0x852c16), result.FromBlock)
		require.Equal(t, uint64(0x853273), result.ToBlock)
	})

	t.Run("error with invalid block range returns nil", func(t *testing.T) {
		dataErr := &mockDataError{
			msg:  "query error",
			data: "Query returned more than 20000 results. Try with different range.",
		}
		result := extractSuggestedBlockRangeFromError(dataErr)
		require.Nil(t, result)
	})
}

func TestEVMMultidownloader_storeData(t *testing.T) {
	t.Run("successful store", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		ctx := context.Background()

		logs := []ethtypes.Log{{Address: common.HexToAddress("0x123")}}
		blocks := aggkittypes.ListBlockHeaders{{Number: 100, Hash: common.HexToHash("0xabc")}}
		updatedSegments := []mdrtypes.SyncSegment{
			mdrtypes.NewSyncSegment(
				common.HexToAddress("0x123"),
				aggkitcommon.NewBlockRange(100, 200),
				aggkittypes.BlockNumberFinality{},
				false,
			),
		}

		mockTx := dbmocks.NewTxer(t)
		data.mockStorage.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		data.mockStorage.EXPECT().SaveEthLogsWithHeaders(mockTx, blocks, logs, true).Return(nil).Once()
		data.mockStorage.EXPECT().UpdateSyncedStatus(mockTx, updatedSegments).Return(nil).Once()
		mockTx.EXPECT().Commit().Return(nil).Once()

		err := data.mdr.storeData(ctx, logs, blocks, updatedSegments, true)
		require.NoError(t, err)
	})

	t.Run("error creating transaction", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		ctx := context.Background()

		expectedErr := fmt.Errorf("tx creation error")
		data.mockStorage.EXPECT().NewTx(ctx).Return(nil, expectedErr).Once()

		err := data.mdr.storeData(ctx, nil, nil, nil, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot create new tx")
	})

	t.Run("error saving logs and headers", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		ctx := context.Background()

		logs := []ethtypes.Log{{Address: common.HexToAddress("0x123")}}
		blocks := aggkittypes.ListBlockHeaders{{Number: 100}}

		mockTx := dbmocks.NewTxer(t)
		expectedErr := fmt.Errorf("save error")
		data.mockStorage.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		data.mockStorage.EXPECT().SaveEthLogsWithHeaders(mockTx, blocks, logs, true).Return(expectedErr).Once()
		mockTx.EXPECT().Rollback().Return(nil).Once()

		err := data.mdr.storeData(ctx, logs, blocks, nil, true)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot save eth logs")
	})

	t.Run("error updating synced status", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		ctx := context.Background()

		updatedSegments := []mdrtypes.SyncSegment{
			mdrtypes.NewSyncSegment(
				common.HexToAddress("0x123"),
				aggkitcommon.NewBlockRange(100, 200),
				aggkittypes.BlockNumberFinality{},
				false,
			),
		}

		mockTx := dbmocks.NewTxer(t)
		expectedErr := fmt.Errorf("update error")
		data.mockStorage.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		data.mockStorage.EXPECT().SaveEthLogsWithHeaders(mockTx, mock.Anything, mock.Anything, false).Return(nil).Once()
		data.mockStorage.EXPECT().UpdateSyncedStatus(mockTx, updatedSegments).Return(expectedErr).Once()
		mockTx.EXPECT().Rollback().Return(nil).Once()

		err := data.mdr.storeData(ctx, nil, nil, updatedSegments, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot update synced segments")
	})

	t.Run("error committing transaction", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)
		ctx := context.Background()

		mockTx := dbmocks.NewTxer(t)
		expectedErr := fmt.Errorf("commit error")
		data.mockStorage.EXPECT().NewTx(ctx).Return(mockTx, nil).Once()
		data.mockStorage.EXPECT().SaveEthLogsWithHeaders(mockTx, mock.Anything, mock.Anything, false).Return(nil).Once()
		data.mockStorage.EXPECT().UpdateSyncedStatus(mockTx, mock.Anything).Return(nil).Once()
		mockTx.EXPECT().Commit().Return(expectedErr).Once()

		err := data.mdr.storeData(ctx, nil, nil, nil, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot commit tx")
	})
}

func TestEVMMultidownloader_newStateFromStorage(t *testing.T) {
	t.Run("successful state creation", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)

		// Mock GetCurrentBlockNumber for UpdateTargetBlockToNumber
		data.mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(1000), nil).Maybe()

		// Mock storage response
		storageSyncSegments := mdrtypes.NewSetSyncSegment()
		storageSyncSegments.Add(mdrtypes.NewSyncSegment(
			common.HexToAddress("0x123"),
			aggkitcommon.NewBlockRange(0, 100),
			aggkittypes.BlockNumberFinality{},
			false,
		))
		data.mockStorage.EXPECT().GetSyncedBlockRangePerContract(mock.Anything).
			Return(storageSyncSegments, nil).Once()

		state, err := data.mdr.newStateFromStorage(t.Context())
		require.NoError(t, err)
		require.NotNil(t, state)
	})

	t.Run("error getting synced block ranges from storage", func(t *testing.T) {
		data := newEVMMultidownloaderTestData(t, true)

		// Mock GetCurrentBlockNumber for UpdateTargetBlockToNumber
		data.mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(1000), nil).Maybe()

		// Mock storage to return error
		expectedErr := fmt.Errorf("storage error")
		emptySegments := mdrtypes.NewSetSyncSegment()
		data.mockStorage.EXPECT().GetSyncedBlockRangePerContract(mock.Anything).
			Return(emptySegments, expectedErr).Once()

		state, err := data.mdr.newStateFromStorage(t.Context())
		require.Error(t, err)
		require.Nil(t, state)
		require.Contains(t, err.Error(), "cannot get synced block ranges from storage")
	})
}

// setupWaitForNewBlocksTest creates common test fixtures
func setupWaitForNewBlocksTest(t *testing.T) (*EVMMultidownloader, *aggkittypes.BlockHeader, *mocktypes.BaseEthereumClienter, *mockethermantypes.BlockNotifierManager) {
	t.Helper()
	mockEthClient := mocktypes.NewBaseEthereumClienter(t)
	mockBlockNotifierManager := mockethermantypes.NewBlockNotifierManager(t)
	logger := log.WithFields("test", "waitForNewBlocks")

	mdr := &EVMMultidownloader{
		log:                  logger,
		ethClient:            mockEthClient,
		blockNotifierManager: mockBlockNotifierManager,
		cfg: Config{
			PeriodToCheckReorgs: types.Duration{Duration: 10 * time.Millisecond},
		},
	}

	lastBlockHeader := &aggkittypes.BlockHeader{
		Number: 100,
		Hash:   common.HexToHash("0x1234"),
	}

	return mdr, lastBlockHeader, mockEthClient, mockBlockNotifierManager
}

func TestEVMMultidownloader_waitForNewBlocks(t *testing.T) {
	t.Run("context cancelled", func(t *testing.T) {
		mdr, lastBlockHeader, _, _ := setupWaitForNewBlocksTest(t)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		// Execute
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.LatestBlock, lastBlockHeader, mdrtypes.NotFinalized)

		// Assert
		require.Error(t, err)
		require.Equal(t, context.Canceled, err)
		require.Equal(t, lastBlockHeader.Number, blockNumber)
	})

	t.Run("finalized - new block arrives", func(t *testing.T) {
		mdr, lastBlockHeader, _, mockBlockNotifierManager := setupWaitForNewBlocksTest(t)

		// Mock: first call returns same block, second call returns new block
		callCount := 0
		mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, aggkittypes.FinalizedBlock).
			RunAndReturn(func(ctx context.Context, blockTag aggkittypes.BlockNumberFinality) (uint64, error) {
				callCount++
				if callCount == 1 {
					return 100, nil // Same block
				}
				return 101, nil // New block
			})

		// Execute
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.FinalizedBlock, lastBlockHeader, mdrtypes.Finalized)

		// Assert
		require.NoError(t, err)
		require.Equal(t, uint64(101), blockNumber)
	})

	t.Run("finalized - error getting current block number", func(t *testing.T) {
		mdr, lastBlockHeader, _, mockBlockNotifierManager := setupWaitForNewBlocksTest(t)

		expectedErr := fmt.Errorf("RPC error")
		mockBlockNotifierManager.EXPECT().
			GetCurrentBlockNumber(mock.Anything, aggkittypes.FinalizedBlock).
			Return(uint64(0), expectedErr).Once()

		// Execute
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.FinalizedBlock, lastBlockHeader, mdrtypes.Finalized)

		// Assert
		require.Error(t, err)
		require.Contains(t, err.Error(), "WaitForNewBlocks: cannot get current block number")
		require.Contains(t, err.Error(), "RPC error")
		require.Equal(t, lastBlockHeader.Number, blockNumber)
	})

	t.Run("not finalized - new block arrives", func(t *testing.T) {
		mdr, lastBlockHeader, mockEthClient, _ := setupWaitForNewBlocksTest(t)

		// Mock: return new block immediately
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(&aggkittypes.BlockHeader{
				Number: 101,
				Hash:   common.HexToHash("0x5678"),
			}, nil).Once()

		// Execute
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.LatestBlock, lastBlockHeader, mdrtypes.NotFinalized)

		// Assert
		require.NoError(t, err)
		require.Equal(t, uint64(101), blockNumber)
	})

	t.Run("not finalized - error getting current header", func(t *testing.T) {
		mdr, lastBlockHeader, mockEthClient, _ := setupWaitForNewBlocksTest(t)

		expectedErr := fmt.Errorf("RPC error")
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(nil, expectedErr).Once()

		// Execute
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.LatestBlock, lastBlockHeader, mdrtypes.NotFinalized)

		// Assert
		require.Error(t, err)
		require.Contains(t, err.Error(), "WaitForNewBlocks: cannot get current block header")
		require.Contains(t, err.Error(), "RPC error")
		require.Equal(t, lastBlockHeader.Number, blockNumber)
	})

	t.Run("not finalized - reorg detected - block hash mismatch at same block", func(t *testing.T) {
		mdr, lastBlockHeader, mockEthClient, _ := setupWaitForNewBlocksTest(t)

		// Mock: return same block number but different hash
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(&aggkittypes.BlockHeader{
				Number: 100,
				Hash:   common.HexToHash("0x5678"), // Different hash!
			}, nil).Once()

		// Execute
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.LatestBlock, lastBlockHeader, mdrtypes.NotFinalized)

		// Assert
		require.Error(t, err)
		var reorgErr *mdrtypes.DetectedReorgError
		require.True(t, mdrtypes.IsDetectedReorgError(err))
		require.ErrorAs(t, err, &reorgErr)
		require.Equal(t, mdrtypes.ReorgDetectionReason_BlockHashMismatch, reorgErr.ReorgDetectionReason)
		require.Equal(t, lastBlockHeader.Number, reorgErr.OffendingBlockNumber)
		require.Equal(t, lastBlockHeader.Hash, reorgErr.OldHash)
		require.Equal(t, common.HexToHash("0x5678"), reorgErr.NewHash)
		require.Equal(t, lastBlockHeader.Number, blockNumber)
	})

	t.Run("not finalized - reorg detected - parent hash mismatch at next block", func(t *testing.T) {
		mdr, lastBlockHeader, mockEthClient, _ := setupWaitForNewBlocksTest(t)

		wrongParentHash := common.HexToHash("0x9999")
		// Mock: return next block (101) with wrong parent hash
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(&aggkittypes.BlockHeader{
				Number:     101,
				Hash:       common.HexToHash("0x5678"),
				ParentHash: &wrongParentHash, // Wrong parent hash!
			}, nil).Once()

		// Execute
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.LatestBlock, lastBlockHeader, mdrtypes.NotFinalized)

		// Assert
		require.Error(t, err)
		var reorgErr *mdrtypes.DetectedReorgError
		require.True(t, mdrtypes.IsDetectedReorgError(err))
		require.ErrorAs(t, err, &reorgErr)
		require.Equal(t, mdrtypes.ReorgDetectionReason_ParentHashMismatch, reorgErr.ReorgDetectionReason)
		require.Equal(t, lastBlockHeader.Number, reorgErr.OffendingBlockNumber)
		require.Equal(t, lastBlockHeader.Hash, reorgErr.OldHash)
		require.Equal(t, wrongParentHash, reorgErr.NewHash)
		require.Equal(t, lastBlockHeader.Number, blockNumber)
	})

	t.Run("not finalized - reorg detected - current block less than last block", func(t *testing.T) {
		mdr, lastBlockHeader, mockEthClient, _ := setupWaitForNewBlocksTest(t)

		// Mock: return lower block number (reorg happened)
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(&aggkittypes.BlockHeader{
				Number: 95, // Lower than last synced block!
				Hash:   common.HexToHash("0x5678"),
			}, nil).Once()

		// Execute
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.LatestBlock, lastBlockHeader, mdrtypes.NotFinalized)

		// Assert
		require.Error(t, err)
		var reorgErr *mdrtypes.DetectedReorgError
		require.True(t, mdrtypes.IsDetectedReorgError(err))
		require.ErrorAs(t, err, &reorgErr)
		require.Equal(t, mdrtypes.ReorgDetectionReason_MissingBlock, reorgErr.ReorgDetectionReason)
		require.Equal(t, lastBlockHeader.Number, reorgErr.OffendingBlockNumber)
		require.Equal(t, lastBlockHeader.Number, blockNumber)
	})

	t.Run("not finalized - same block number with same hash - no reorg", func(t *testing.T) {
		mdr, lastBlockHeader, mockEthClient, _ := setupWaitForNewBlocksTest(t)

		// Mock: first returns same block with same hash, second returns new block
		callCount := 0
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, mock.Anything).
			RunAndReturn(func(ctx context.Context, blockTag *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
				callCount++
				if callCount == 1 {
					return &aggkittypes.BlockHeader{
						Number: 100,
						Hash:   common.HexToHash("0x1234"), // Same hash - no reorg
					}, nil
				}
				return &aggkittypes.BlockHeader{
					Number: 101,
					Hash:   common.HexToHash("0x5678"),
				}, nil
			})

		// Execute
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.LatestBlock, lastBlockHeader, mdrtypes.NotFinalized)

		// Assert
		require.NoError(t, err)
		require.Equal(t, uint64(101), blockNumber)
	})

	t.Run("not finalized - next block with correct parent hash - no reorg", func(t *testing.T) {
		mdr, lastBlockHeader, mockEthClient, _ := setupWaitForNewBlocksTest(t)

		correctParentHash := common.HexToHash("0x1234")
		// Mock: return next block with correct parent hash
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(&aggkittypes.BlockHeader{
				Number:     101,
				Hash:       common.HexToHash("0x5678"),
				ParentHash: &correctParentHash, // Correct parent hash
			}, nil).Once()

		// Execute
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.LatestBlock, lastBlockHeader, mdrtypes.NotFinalized)

		// Assert
		require.NoError(t, err)
		require.Equal(t, uint64(101), blockNumber)
	})

	t.Run("not finalized - next block without parent hash - no parent check", func(t *testing.T) {
		mdr, lastBlockHeader, mockEthClient, _ := setupWaitForNewBlocksTest(t)

		// Mock: return next block without parent hash
		mockEthClient.EXPECT().
			CustomHeaderByNumber(mock.Anything, mock.Anything).
			Return(&aggkittypes.BlockHeader{
				Number:     101,
				Hash:       common.HexToHash("0x5678"),
				ParentHash: nil, // No parent hash to check
			}, nil).Once()

		// Execute
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		blockNumber, err := mdr.waitForNewBlocks(ctx, aggkittypes.LatestBlock, lastBlockHeader, mdrtypes.NotFinalized)

		// Assert
		require.NoError(t, err)
		require.Equal(t, uint64(101), blockNumber)
	})
}
