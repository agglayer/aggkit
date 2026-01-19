package multidownloader

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"sync"
	"testing"
	"time"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/etherman"
	mockethermantypes "github.com/agglayer/aggkit/etherman/types/mocks"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	mockmdrtypes "github.com/agglayer/aggkit/multidownloader/types/mocks"
	"github.com/agglayer/aggkit/reorgdetector"
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

const runL1InfoTree = true
const l1InfoTreeUseMultidownloader = true
const storagePath = "../tmp/ut/"

func TestEVMMultidownloader(t *testing.T) {
	// t.Skip("code to test/debug not real unittest")
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
	var rpcServices []jRPC.Service
	mdr, err := NewEVMMultidownloader(logger,
		cfg, "l1", ethClient, ethRPCClient,
		db, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, mdr)
	err = mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID: "test_syncer",
		ContractsAddr: []common.Address{
			common.HexToAddress("0x2968d6d736178f8fe7393cc33c87f29d9c287e78"), // GERManager
			common.HexToAddress("0xe2ef6215adc132df6913c8dd16487abf118d1764"), // RollupManager
		},
		FromBlock: 5157574,
		ToBlock:   aggkittypes.LatestBlock,
	})
	require.NoError(t, err)
	rpcServices = append(rpcServices, mdr.GetRPCServices()...)
	ctx := context.TODO()
	var l1infotree *l1infotreesync.L1InfoTreeSync
	if runL1InfoTree == true {
		var multidownloader aggkittypes.MultiDownloader
		var dbPath string
		if l1InfoTreeUseMultidownloader {
			multidownloader = mdr
			dbPath = storagePath + "l1infotree_md.sqlite"
		} else {
			multidownloader = aggkitsync.NewAdapterEthClientToMultidownloader(ethClient)
			dbPath = storagePath + "l1infotree_eth.sqlite"
		}
		reorgDetector, err := reorgdetector.New(ethClient, reorgdetector.Config{
			DBPath:              storagePath + "l1_reorgdetector.sqlite",
			CheckReorgsInterval: types.NewDuration(time.Second * 10),
			FinalizedBlock:      aggkittypes.FinalizedBlock,
		}, reorgdetector.L1)
		require.NoError(t, err)

		l1infotree, err = l1infotreesync.New(
			ctx,
			l1infotreesync.Config{
				DBPath:             dbPath,
				InitialBlock:       5157574,
				GlobalExitRootAddr: common.HexToAddress("0x2968d6d736178f8fe7393cc33c87f29d9c287e78"),
				RollupManagerAddr:  common.HexToAddress("0xe2ef6215adc132df6913c8dd16487abf118d1764"),
				SyncBlockChunkSize: 6500,
				WaitForNewBlocksPeriod: types.Duration{
					Duration: 5 * time.Second,
				},
				BlockFinality: aggkittypes.FinalizedBlock,
			},
			multidownloader,
			reorgDetector,
			// l1infotreesync.FlagStopOnFinalizedBlockReached,
			l1infotreesync.FlagNone,
		)
		require.NoError(t, err)
		rpcServices = append(rpcServices, l1infotree.GetRPCServices()...)
	}
	if len(rpcServices) > 0 {
		log.Infof("Registering %d RPC services", len(rpcServices))
		logger := log.WithFields("module", "RPC")
		jRPCServer := jRPC.NewServer(
			jRPC.Config{
				Host:                      "127.0.0.1",
				Port:                      5576,
				MaxRequestsPerIPAndSecond: 10000.0,
			},
			rpcServices,
			jRPC.WithLogger(logger.GetSugaredLogger()),
		)
		go func() {
			if err := jRPCServer.Start(); err != nil {
				log.Fatal(err)
			}
		}()
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
		if l1infotree != nil {
			l1infotree.Start(t.Context())
		}
		timer.Stop()
		log.Infof("L1InfoTree sync finished in %s", timer.String())
	}()
	wg.Wait()
}

func TestEVMMultidownloaderExploratoryBatchRequests(t *testing.T) {
	t.Skip("it's a exploratory test for batch requests")
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
}

func TestDownloaderParellelvsBatch(t *testing.T) {
	t.Skip("it's a benchmarking test")
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
}

func getBlockHeader(bn uint64, headers []*aggkittypes.BlockHeader) *aggkittypes.BlockHeader {
	for _, h := range headers {
		if h.Number == bn {
			return h
		}
	}
	return nil
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
			ContractsAddr: []common.Address{
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
		ContractsAddr: []common.Address{
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

	err = testData.mdr.sync(t.Context(), testData.mdr.StepSafe, "safe")
	require.NoError(t, err)
	require.True(t, finished)

	ctx, cancel := context.WithCancel(context.TODO())
	cancel()
	_, err = testData.mdr.StepSafe(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestEVMMultidownloader_sync(t *testing.T) {
	testData := newEVMMultidownloaderTestData(t, false)
	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		cancel()
		err := testData.mdr.sync(ctx, func(ctx context.Context) (bool, error) {
			return false, nil
		}, "test_sync")
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("sync func returns an error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())
		cancel()
		returnedErr := fmt.Errorf("sync function error")
		err := testData.mdr.sync(ctx, func(ctx context.Context) (bool, error) {
			return false, returnedErr
		}, "test_sync")
		require.ErrorIs(t, err, returnedErr)
	})

	t.Run("sync func finished no errors", func(t *testing.T) {
		err := testData.mdr.sync(t.Context(), func(ctx context.Context) (bool, error) {
			return true, nil
		}, "test_sync")
		require.NoError(t, err)
	})
}

/*
func TestEVMMultidownloader_Start(t *testing.T) {
	testData := newEVMMultidownloaderTestData(t)
	testData.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil).Maybe()
	err := testData.mdr.Initialize(t.Context())
	require.NoError(t, err)

	start := time.Now()
	err = testData.mdr.Start(t.Context())
	duration := time.Since(start)
	log.Infof("Multidownloader Start took %s", duration.String())
	require.NoError(t, err)
}
*/

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
