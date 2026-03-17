package bridgesync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"path"
	"testing"
	"time"

	mocksbridgesync "github.com/agglayer/aggkit/bridgesync/mocks"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testSyncFromInBridges = true

func TestNewLx(t *testing.T) {
	const (
		syncBlockChunkSize         = uint64(100)
		initialBlock               = uint64(0)
		waitForNewBlocksPeriod     = time.Second * 10
		retryAfterErrorPeriod      = time.Second * 5
		maxRetryAttemptsAfterError = 3
		originNetwork              = uint32(1)
	)

	var (
		blockFinalityType = aggkittypes.SafeBlock
		ctx               = context.Background()
		dbPath            = path.Join(t.TempDir(), "TestNewLx.sqlite")
		bridge            = common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	)

	mockEthClient := mocksethclient.NewEthClienter(t)
	// bridgeManager function call (bridge contract)
	// once for each syncer (l1 and l2)
	mockEthClient.EXPECT().CallContract(mock.Anything,
		ethereum.CallMsg{
			To:   &bridge,
			Data: common.Hex2Bytes("14cc01a0"),
		}, mock.Anything).
		Return(common.LeftPadBytes(bridge.Bytes(), common.HashLength), nil).
		Twice()

	// lastUpdatedDepositCount function call (bridge contract)
	// once for each syncer (l1 and l2)
	mockEthClient.EXPECT().
		CallContract(mock.Anything,
			ethereum.CallMsg{
				To:   &bridge,
				Data: common.Hex2Bytes("be5831c7")},
			mock.Anything).
		Return(common.LeftPadBytes(common.Hex2Bytes("2a"), common.HashLength), nil).
		Twice()

	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)
	mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(blockFinalityType)
	mockReorgDetector.EXPECT().String().Return("mockReorgDetector")
	// CustomHeaderByNumber is called once (for L1 on fresh DB; L2 reuses the same DB)
	mockEthClient.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(aggkittypes.NewBlockHeader(0, common.Hash{}, 0, nil), nil).Once()

	dbQueryTimeout := 30 * time.Second

	syncFromInBridgesResolved := testSyncFromInBridges
	bridgeSyncL1Cfg := Config{
		DBPath:                             dbPath,
		BridgeAddr:                         bridge,
		BlockFinality:                      aggkittypes.LatestBlock,
		SyncBlockChunkSize:                 syncBlockChunkSize,
		InitialBlockNum:                    initialBlock,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(waitForNewBlocksPeriod),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(retryAfterErrorPeriod),
		MaxRetryAttemptsAfterError:         maxRetryAttemptsAfterError,
		RequireStorageContentCompatibility: true,
		DBQueryTimeout:                     cfgtypes.NewDuration(dbQueryTimeout),
	}
	bridgeSyncL1Cfg.SyncFromInBridges.Resolved = &syncFromInBridgesResolved

	l1BridgeSync, err := NewL1(
		ctx,
		bridgeSyncL1Cfg,
		mockReorgDetector,
		mockEthClient,
		originNetwork,
	)

	require.NoError(t, err)
	require.NotNil(t, l1BridgeSync)
	require.Equal(t, originNetwork, l1BridgeSync.OriginNetwork())

	bridgeSyncL2Cfg := Config{
		DBPath:                             dbPath,
		BridgeAddr:                         bridge,
		BlockFinality:                      aggkittypes.SafeBlock,
		SyncBlockChunkSize:                 syncBlockChunkSize,
		InitialBlockNum:                    initialBlock,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(waitForNewBlocksPeriod),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(retryAfterErrorPeriod),
		MaxRetryAttemptsAfterError:         maxRetryAttemptsAfterError,
		RequireStorageContentCompatibility: true,
		DBQueryTimeout:                     cfgtypes.NewDuration(dbQueryTimeout),
	}
	bridgeSyncL2Cfg.SyncFromInBridges.Resolved = &syncFromInBridgesResolved
	l2BridgdeSync, err := NewL2(
		ctx,
		bridgeSyncL2Cfg,
		mockReorgDetector,
		mockEthClient,
		originNetwork,
		testSyncFromInBridges,
		bridgesynctypes.EmptyLER,
	)

	require.NoError(t, err)
	require.NotNil(t, l1BridgeSync)
	require.Equal(t, originNetwork, l2BridgdeSync.OriginNetwork())

	mockEthClient = mocksethclient.NewEthClienter(t)
	mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
	mockEthClient.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

	l2BridgeSyncer, err := NewL2(
		ctx,
		bridgeSyncL2Cfg,
		mockReorgDetector,
		mockEthClient,
		originNetwork,
		testSyncFromInBridges,
		bridgesynctypes.EmptyLER,
	)
	require.Error(t, err)
	require.Nil(t, l2BridgeSyncer)
}

func TestGetLastProcessedBlock(t *testing.T) {
	s := BridgeSync{processor: &processor{
		halted: true,
		log:    log.WithFields("module", "L2BridgeSyncer"),
	}}
	_, _, err := s.GetLastProcessedBlock(context.Background())
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestGetLatestNetworkBlock(t *testing.T) {
	ctx := context.Background()
	mockEthClient := mocksethclient.NewEthClienter(t)

	t.Run("successful block number retrieval", func(t *testing.T) {
		expectedBlockNumber := uint64(12345678)
		mockEthClient.EXPECT().BlockNumber(mock.Anything).Return(expectedBlockNumber, nil).Once()

		s := BridgeSync{
			processor: &processor{
				halted: false,
				log:    log.WithFields("module", "L2BridgeSyncer"),
			},
			ethClient: mockEthClient,
		}

		blockNumber, err := s.GetLatestNetworkBlock(ctx)
		require.NoError(t, err)
		require.Equal(t, expectedBlockNumber, blockNumber)
	})

	t.Run("error from eth client", func(t *testing.T) {
		expectedError := errors.New("network error")
		mockEthClient.EXPECT().BlockNumber(mock.Anything).Return(uint64(0), expectedError).Once()

		s := BridgeSync{
			processor: &processor{
				halted: false,
				log:    log.WithFields("module", "L2BridgeSyncer"),
			},
			ethClient: mockEthClient,
		}

		blockNumber, err := s.GetLatestNetworkBlock(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get latest block number")
		require.Equal(t, uint64(0), blockNumber)
	})

	t.Run("processor halted", func(t *testing.T) {
		s := BridgeSync{processor: &processor{
			halted: true,
			log:    log.WithFields("module", "L2BridgeSyncer"),
		}}

		blockNumber, err := s.GetLatestNetworkBlock(ctx)
		require.ErrorIs(t, err, sync.ErrInconsistentState)
		require.Equal(t, uint64(0), blockNumber)
	})
}

func TestIsActive(t *testing.T) {
	ctx := context.Background()

	t.Run("active syncer", func(t *testing.T) {
		s := BridgeSync{processor: &processor{
			halted: false,
			log:    log.WithFields("module", "L2BridgeSyncer"),
		}}

		isActive := s.IsActive(ctx)
		require.True(t, isActive)
	})

	t.Run("inactive syncer", func(t *testing.T) {
		s := BridgeSync{processor: &processor{
			halted: true,
			log:    log.WithFields("module", "L2BridgeSyncer"),
		}}

		isActive := s.IsActive(ctx)
		require.False(t, isActive)
	})
}

func TestGetExitRootByHash(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetExitRootByHash(context.Background(), common.Hash{})
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestGetBridges(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetBridges(context.Background(), 0, 0)
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestGetProof(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetProof(context.Background(), 0, common.Hash{})
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestGetBlockByLER(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetBlockByLER(context.Background(), common.Hash{})
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestGetRootByLER(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetRootByLER(context.Background(), common.Hash{})
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestGetExitRootByIndex(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetExitRootByIndex(context.Background(), 0)
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestBridgeSync_GetTokenMappings(t *testing.T) {
	const (
		syncBlockChunkSize         = uint64(100)
		initialBlock               = uint64(0)
		waitForNewBlocksPeriod     = time.Second * 10
		retryAfterErrorPeriod      = time.Second * 5
		maxRetryAttemptsAfterError = 3
		originNetwork              = uint32(1)
		tokenMappingsCount         = 20
		blockNum                   = uint64(1)
	)

	var (
		blockFinalityType = aggkittypes.SafeBlock
		ctx               = context.Background()
		dbPath            = path.Join(t.TempDir(), "TestGetTokenMappings.sqlite")
		bridge            = common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	)

	mockEthClient := mocksethclient.NewEthClienter(t)
	// bridgeManager function call (bridge contract)
	mockEthClient.EXPECT().CallContract(mock.Anything,
		ethereum.CallMsg{
			To:   &bridge,
			Data: common.Hex2Bytes("14cc01a0"),
		}, mock.Anything).
		Return(common.LeftPadBytes(bridge.Bytes(), common.HashLength), nil).
		Once()

	// lastUpdatedDepositCount function call (bridge contract)
	mockEthClient.EXPECT().
		CallContract(mock.Anything,
			ethereum.CallMsg{
				To:   &bridge,
				Data: common.Hex2Bytes("be5831c7")},
			mock.Anything).
		Return(common.LeftPadBytes(common.Hex2Bytes("2a"), common.HashLength), nil).
		Once()

	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)
	mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(blockFinalityType)
	mockReorgDetector.EXPECT().String().Return("mockReorgDetector")
	mockEthClient.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(aggkittypes.NewBlockHeader(0, common.Hash{}, 0, nil), nil).Once()

	dbQueryTimeout := 30 * time.Second

	bridgeSyncCfg := Config{
		DBPath:                             dbPath,
		BridgeAddr:                         bridge,
		BlockFinality:                      aggkittypes.LatestBlock,
		SyncBlockChunkSize:                 syncBlockChunkSize,
		InitialBlockNum:                    initialBlock,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(waitForNewBlocksPeriod),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(retryAfterErrorPeriod),
		MaxRetryAttemptsAfterError:         maxRetryAttemptsAfterError,
		RequireStorageContentCompatibility: false,
		DBQueryTimeout:                     cfgtypes.NewDuration(dbQueryTimeout),
	}
	bridgeSyncCfg.SyncFromInBridges.Resolved = func() *bool { b := testSyncFromInBridges; return &b }()
	s, err := NewL2(
		ctx,
		bridgeSyncCfg,
		mockReorgDetector,
		mockEthClient,
		originNetwork,
		testSyncFromInBridges,
		bridgesynctypes.EmptyLER,
	)
	require.NoError(t, err)

	allTokenMappings := make([]*TokenMapping, 0, tokenMappingsCount)
	genericEvts := make([]any, 0, tokenMappingsCount)

	for i := tokenMappingsCount - 1; i >= 0; i-- {
		tokenMappingEvt := &TokenMapping{
			BlockNum:            blockNum,
			BlockPos:            uint64(i),
			OriginNetwork:       uint32(i),
			OriginTokenAddress:  common.HexToAddress(fmt.Sprintf("%d", i)),
			WrappedTokenAddress: common.HexToAddress(fmt.Sprintf("%d", i+1)),
		}

		allTokenMappings = append(allTokenMappings, tokenMappingEvt)
		genericEvts = append(genericEvts, Event{TokenMapping: tokenMappingEvt})
	}

	block := sync.Block{
		Num:    blockNum,
		Events: genericEvts,
	}

	err = s.processor.ProcessBlock(context.Background(), block)
	require.NoError(t, err)

	t.Run("retrieve all mappings", func(t *testing.T) {
		tokenMappings, totalTokenMappings, err := s.GetTokenMappings(context.Background(), 1, tokenMappingsCount, "")
		require.NoError(t, err)
		require.Equal(t, tokenMappingsCount, totalTokenMappings)
		require.Equal(t, allTokenMappings, tokenMappings)
	})

	t.Run("retrieve paginated mappings", func(t *testing.T) {
		pageSize := uint32(5)

		for page := uint32(1); page <= 4; page++ {
			tokenMappings, totalTokenMappings, err := s.GetTokenMappings(context.Background(), page, pageSize, "")
			require.NoError(t, err)
			require.Equal(t, tokenMappingsCount, totalTokenMappings)

			startIndex := (page - 1) * pageSize
			endIndex := startIndex + pageSize
			require.Equal(t, allTokenMappings[startIndex:endIndex], tokenMappings)
		}
	})

	t.Run("retrieve non-existent page", func(t *testing.T) {
		pageSize := uint32(5)
		pageNum := uint32(5)

		tokenMappings, totalTokenMappings, err := s.GetTokenMappings(context.Background(), pageNum, pageSize, "")
		require.ErrorContains(t, err, "invalid page number for given page size and total number of token mappings")
		require.Equal(t, 0, totalTokenMappings)
		require.Nil(t, tokenMappings)
	})

	t.Run("provide invalid page number", func(t *testing.T) {
		pageSize := uint32(0)
		pageNum := uint32(0)

		_, _, err := s.GetTokenMappings(context.Background(), pageNum, pageSize, "")
		require.ErrorIs(t, err, ErrInvalidPageNumber)
	})

	t.Run("provide invalid page size", func(t *testing.T) {
		pageSize := uint32(0)
		pageNum := uint32(4)

		_, _, err := s.GetTokenMappings(context.Background(), pageNum, pageSize, "")
		require.ErrorIs(t, err, ErrInvalidPageSize)
	})

	t.Run("filter by valid origin token address", func(t *testing.T) {
		s.processor.halted = false

		targetOriginAddress := common.HexToAddress("5").Hex()
		tokenMappings, totalTokenMappings, err := s.GetTokenMappings(context.Background(), 1, tokenMappingsCount, targetOriginAddress)
		require.NoError(t, err)

		require.Equal(t, 1, totalTokenMappings)
		require.Len(t, tokenMappings, 1)
		require.Equal(t, common.HexToAddress("5"), tokenMappings[0].OriginTokenAddress)
		require.Equal(t, uint32(5), tokenMappings[0].OriginNetwork)
		require.Equal(t, common.HexToAddress("6"), tokenMappings[0].WrappedTokenAddress)
	})

	t.Run("filter by non-existent origin token address", func(t *testing.T) {
		nonExistentAddress := common.HexToAddress("999").Hex()
		tokenMappings, totalTokenMappings, err := s.GetTokenMappings(context.Background(), 1, tokenMappingsCount, nonExistentAddress)
		require.NoError(t, err)

		require.Equal(t, 0, totalTokenMappings)
		require.Empty(t, tokenMappings)
	})

	t.Run("inconsistent state", func(t *testing.T) {
		s.processor.halted = true
		_, _, err := s.GetTokenMappings(context.Background(), 0, 0, "")
		require.ErrorIs(t, err, sync.ErrInconsistentState)
	})
}

func TestBridgeSync_GetLegacyTokenMigrations(t *testing.T) {
	const (
		syncBlockChunkSize         = uint64(100)
		initialBlock               = uint64(0)
		waitForNewBlocksPeriod     = time.Second * 10
		retryAfterErrorPeriod      = time.Second * 5
		maxRetryAttemptsAfterError = 3
		originNetwork              = uint32(1)
		tokenMigrationsCount       = 20
		blockNum                   = uint64(1)
	)

	var (
		blockFinalityType = aggkittypes.SafeBlock
		ctx               = context.Background()
		dbPath            = path.Join(t.TempDir(), "TestGetTokenMigrations.sqlite")
		bridge            = common.HexToAddress("0x123456")
	)

	mockEthClient := mocksethclient.NewEthClienter(t)
	// bridgeManager function call (bridge contract)
	mockEthClient.EXPECT().CallContract(mock.Anything,
		ethereum.CallMsg{
			To:   &bridge,
			Data: common.Hex2Bytes("14cc01a0"),
		}, mock.Anything).
		Return(common.LeftPadBytes(bridge.Bytes(), common.HashLength), nil).
		Once()

	// lastUpdatedDepositCount function call (bridge contract)
	mockEthClient.EXPECT().
		CallContract(mock.Anything,
			ethereum.CallMsg{
				To:   &bridge,
				Data: common.Hex2Bytes("be5831c7")},
			mock.Anything).
		Return(common.LeftPadBytes(common.Hex2Bytes("2a"), common.HashLength), nil).
		Once()

	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)
	mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(blockFinalityType)
	mockReorgDetector.EXPECT().String().Return("mockReorgDetector")
	mockEthClient.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(aggkittypes.NewBlockHeader(0, common.Hash{}, 0, nil), nil).Once()

	dbQueryTimeout := 30 * time.Second

	bridgeSyncCfg := Config{
		DBPath:                             dbPath,
		BridgeAddr:                         bridge,
		BlockFinality:                      aggkittypes.LatestBlock,
		SyncBlockChunkSize:                 syncBlockChunkSize,
		InitialBlockNum:                    initialBlock,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(waitForNewBlocksPeriod),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(retryAfterErrorPeriod),
		MaxRetryAttemptsAfterError:         maxRetryAttemptsAfterError,
		RequireStorageContentCompatibility: false,
		DBQueryTimeout:                     cfgtypes.NewDuration(dbQueryTimeout),
	}
	bridgeSyncCfg.SyncFromInBridges.Resolved = func() *bool { b := testSyncFromInBridges; return &b }()
	s, err := NewL2(
		ctx,
		bridgeSyncCfg,
		mockReorgDetector,
		mockEthClient,
		originNetwork,
		testSyncFromInBridges,
		bridgesynctypes.EmptyLER,
	)
	require.NoError(t, err)

	allTokenMirgations := make([]*LegacyTokenMigration, 0, tokenMigrationsCount)
	genericEvts := make([]any, 0, tokenMigrationsCount)

	for i := tokenMigrationsCount - 1; i >= 0; i-- {
		tokenMigrationEvt := &LegacyTokenMigration{
			BlockNum:            blockNum,
			BlockPos:            uint64(i),
			LegacyTokenAddress:  common.HexToAddress(fmt.Sprintf("%d", i+1)),
			UpdatedTokenAddress: common.HexToAddress(fmt.Sprintf("%d", i+2)),
			Amount:              big.NewInt(int64(i * 10)),
		}

		allTokenMirgations = append(allTokenMirgations, tokenMigrationEvt)
		genericEvts = append(genericEvts, Event{LegacyTokenMigration: tokenMigrationEvt})
	}

	block := sync.Block{
		Num:    blockNum,
		Events: genericEvts,
	}

	err = s.processor.ProcessBlock(context.Background(), block)
	require.NoError(t, err)

	t.Run("retrieve all token migrations", func(t *testing.T) {
		tokenMigrations, totalTokenMigrations, err := s.GetLegacyTokenMigrations(context.Background(), 1, tokenMigrationsCount)
		require.NoError(t, err)
		require.Equal(t, tokenMigrationsCount, totalTokenMigrations)
		require.Equal(t, allTokenMirgations, tokenMigrations)
	})

	t.Run("retrieve paginated token migrations", func(t *testing.T) {
		pageSize := uint32(5)

		for page := uint32(1); page <= 4; page++ {
			tokenMigrations, totalTokenMigrations, err := s.GetLegacyTokenMigrations(context.Background(), page, pageSize)
			require.NoError(t, err)
			require.Equal(t, tokenMigrationsCount, totalTokenMigrations)

			startIndex := (page - 1) * pageSize
			endIndex := startIndex + pageSize
			require.Equal(t, allTokenMirgations[startIndex:endIndex], tokenMigrations)
		}
	})

	t.Run("retrieve non-existent page", func(t *testing.T) {
		pageSize := uint32(5)
		pageNum := uint32(5)

		tokenMigrations, totalTokenMigrations, err := s.GetLegacyTokenMigrations(context.Background(), pageNum, pageSize)
		require.ErrorContains(t, err,
			"invalid page number for given page size and total number of legacy token migrations")
		require.Equal(t, 0, totalTokenMigrations)
		require.Nil(t, tokenMigrations)
	})

	t.Run("provide invalid page number", func(t *testing.T) {
		pageSize := uint32(0)
		pageNum := uint32(0)

		_, _, err := s.GetLegacyTokenMigrations(context.Background(), pageNum, pageSize)
		require.ErrorIs(t, err, ErrInvalidPageNumber)
	})

	t.Run("provide invalid page size", func(t *testing.T) {
		pageSize := uint32(0)
		pageNum := uint32(4)

		_, _, err := s.GetTokenMappings(context.Background(), pageNum, pageSize, "")
		require.ErrorIs(t, err, ErrInvalidPageSize)
	})

	t.Run("inconsistent state", func(t *testing.T) {
		s.processor.halted = true
		_, _, err := s.GetTokenMappings(context.Background(), 0, 0, "")
		require.ErrorIs(t, err, sync.ErrInconsistentState)
	})
}

func TestGetBridgePaged(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, _, err := s.GetBridgesPaged(context.Background(), 0, 0, nil, nil, "")
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestBridgeSync_GetLastReorgEvent(t *testing.T) {
	expectedReorgEvent := reorgdetector.ReorgEvent{
		DetectedAt: int64(1710000000),
		FromBlock:  uint64(100),
		ToBlock:    uint64(150),
	}
	ctx := context.Background()
	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)
	s := BridgeSync{
		reorgDetector: mockReorgDetector,
		processor: &processor{
			log: log.WithFields("module", "L2BridgeSyncer"),
		},
	}

	t.Run("retrieve last reorg event successfully", func(t *testing.T) {
		mockReorgDetector.EXPECT().GetLastReorgEvent(mock.Anything).Return(expectedReorgEvent, nil).Once()

		reorgEvent, err := s.GetLastReorgEvent(ctx)
		require.NoError(t, err)
		require.NotNil(t, reorgEvent)
		require.Equal(t, expectedReorgEvent.DetectedAt, reorgEvent.DetectedAt)
		require.Equal(t, expectedReorgEvent.FromBlock, reorgEvent.FromBlock)
		require.Equal(t, expectedReorgEvent.ToBlock, reorgEvent.ToBlock)
	})

	t.Run("error retrieving last reorg event", func(t *testing.T) {
		mockReorgDetector.EXPECT().GetLastReorgEvent(mock.Anything).Return(reorgdetector.ReorgEvent{}, errors.New("reorg event not found")).Once()

		reorgEvent, err := s.GetLastReorgEvent(ctx)
		require.Error(t, err)
		require.Nil(t, reorgEvent)
	})
}

func TestBridgeSync_GetLastRoot(t *testing.T) {
	const (
		syncBlockChunkSize         = uint64(100)
		initialBlock               = uint64(0)
		waitForNewBlocksPeriod     = time.Second * 10
		retryAfterErrorPeriod      = time.Second * 5
		maxRetryAttemptsAfterError = 3
		originNetwork              = uint32(1)
	)

	var (
		blockFinalityType = aggkittypes.SafeBlock
		ctx               = context.Background()
		dbPath            = path.Join(t.TempDir(), "TestGetLastRoot.sqlite")
		bridge            = common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	)

	mockEthClient := mocksethclient.NewEthClienter(t)
	// bridgeManager function call (bridge contract)
	mockEthClient.EXPECT().CallContract(mock.Anything,
		ethereum.CallMsg{
			To:   &bridge,
			Data: common.Hex2Bytes("14cc01a0"),
		}, mock.Anything).
		Return(common.LeftPadBytes(bridge.Bytes(), common.HashLength), nil).
		Once()

	// lastUpdatedDepositCount function call (bridge contract)
	mockEthClient.EXPECT().
		CallContract(mock.Anything,
			ethereum.CallMsg{
				To:   &bridge,
				Data: common.Hex2Bytes("be5831c7")},
			mock.Anything).
		Return(common.LeftPadBytes(common.Hex2Bytes("2a"), common.HashLength), nil).
		Once()

	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)
	mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(blockFinalityType)
	mockReorgDetector.EXPECT().String().Return("mockReorgDetector")
	mockEthClient.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(aggkittypes.NewBlockHeader(0, common.Hash{}, 0, nil), nil).Once()

	dbQueryTimeout := 30 * time.Second

	bridgeSyncCfg := Config{
		DBPath:                             dbPath,
		BridgeAddr:                         bridge,
		BlockFinality:                      aggkittypes.LatestBlock,
		SyncBlockChunkSize:                 syncBlockChunkSize,
		InitialBlockNum:                    initialBlock,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(waitForNewBlocksPeriod),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(retryAfterErrorPeriod),
		MaxRetryAttemptsAfterError:         maxRetryAttemptsAfterError,
		RequireStorageContentCompatibility: false,
		DBQueryTimeout:                     cfgtypes.NewDuration(dbQueryTimeout),
	}
	bridgeSyncCfg.SyncFromInBridges.Resolved = func() *bool { b := testSyncFromInBridges; return &b }()
	s, err := NewL2(
		ctx,
		bridgeSyncCfg,
		mockReorgDetector,
		mockEthClient,
		originNetwork,
		testSyncFromInBridges,
		bridgesynctypes.EmptyLER,
	)
	require.NoError(t, err)

	t.Run("get last root when no roots exist", func(t *testing.T) {
		root, err := s.GetLastRoot(ctx)
		require.Error(t, err)
		require.Nil(t, root)
		require.Contains(t, err.Error(), "not found")
	})

	t.Run("get last root after processing bridge events", func(t *testing.T) {
		bridgeEvents := []interface{}{
			Event{Bridge: &Bridge{
				BlockNum: 1,
				BlockPos: 0,
				FromAddress: func() *common.Address {
					addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
					return &addr
				}(),
				TxHash:             common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
				BlockTimestamp:     1234567890,
				LeafType:           1,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x3333333333333333333333333333333333333333"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
				Amount:             big.NewInt(1000000),
				Metadata:           []byte{0x04, 0x05, 0x06},
				DepositCount:       0,
			}},
			Event{Bridge: &Bridge{
				BlockNum: 1,
				BlockPos: 1,
				FromAddress: func() *common.Address {
					addr := common.HexToAddress("0x5555555555555555555555555555555555555555")
					return &addr
				}(),
				TxHash:             common.HexToHash("0x6666666666666666666666666666666666666666666666666666666666666666"),
				BlockTimestamp:     1234567890,
				LeafType:           1,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x7777777777777777777777777777777777777777"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x8888888888888888888888888888888888888888"),
				Amount:             big.NewInt(2000000),
				Metadata:           []byte{0x0a, 0x0b, 0x0c},
				DepositCount:       1,
			}},
		}

		block := sync.Block{
			Num:    1,
			Events: bridgeEvents,
		}

		err = s.processor.ProcessBlock(ctx, block)
		require.NoError(t, err)

		root, err := s.GetLastRoot(ctx)
		require.NoError(t, err)
		require.NotNil(t, root)
		require.Equal(t, uint64(1), root.BlockNum)
		require.Equal(t, uint64(1), root.BlockPosition)
		require.Equal(t, uint32(1), root.Index)
		require.NotEqual(t, common.Hash{}, root.Hash)
	})

	t.Run("get last root when processor is halted", func(t *testing.T) {
		s.processor.halted = true
		root, err := s.GetLastRoot(ctx)
		require.ErrorIs(t, err, sync.ErrInconsistentState)
		require.Nil(t, root)
	})

	t.Run("get last root after multiple blocks", func(t *testing.T) {
		s.processor.halted = false

		bridgeEvents := []interface{}{
			Event{Bridge: &Bridge{
				BlockNum: 2,
				BlockPos: 0,
				FromAddress: func() *common.Address {
					addr := common.HexToAddress("0x9999999999999999999999999999999999999999")
					return &addr
				}(),
				TxHash:             common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
				BlockTimestamp:     1234567891,
				LeafType:           1,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc"),
				Amount:             big.NewInt(3000000),
				Metadata:           []byte{0x10, 0x11, 0x12},
				DepositCount:       2,
			}},
		}

		block := sync.Block{
			Num:    2,
			Events: bridgeEvents,
		}

		err = s.processor.ProcessBlock(ctx, block)
		require.NoError(t, err)

		root, err := s.GetLastRoot(ctx)
		require.NoError(t, err)
		require.NotNil(t, root)
		require.Equal(t, uint64(2), root.BlockNum)
		require.Equal(t, uint64(0), root.BlockPosition)
		require.Equal(t, uint32(2), root.Index)
		require.NotEqual(t, common.Hash{}, root.Hash)
	})
}

func TestBridgeSync_SubscribeToSync(t *testing.T) {
	const (
		syncBlockChunkSize         = uint64(100)
		initialBlock               = uint64(0)
		waitForNewBlocksPeriod     = time.Second * 10
		retryAfterErrorPeriod      = time.Second * 5
		maxRetryAttemptsAfterError = 3
		originNetwork              = uint32(1)
	)

	var (
		blockFinalityType = aggkittypes.SafeBlock
		ctx               = context.Background()
		dbPath            = path.Join(t.TempDir(), "TestSubscribeToSync.sqlite")
		bridge            = common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	)

	mockEthClient := mocksethclient.NewEthClienter(t)
	// bridgeManager function call (bridge contract)
	mockEthClient.EXPECT().CallContract(mock.Anything,
		ethereum.CallMsg{
			To:   &bridge,
			Data: common.Hex2Bytes("14cc01a0"),
		}, mock.Anything).
		Return(common.LeftPadBytes(bridge.Bytes(), common.HashLength), nil).
		Once()

	// lastUpdatedDepositCount function call (bridge contract)
	mockEthClient.EXPECT().
		CallContract(mock.Anything,
			ethereum.CallMsg{
				To:   &bridge,
				Data: common.Hex2Bytes("be5831c7")},
			mock.Anything).
		Return(common.LeftPadBytes(common.Hex2Bytes("2a"), common.HashLength), nil).
		Once()

	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)
	mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(blockFinalityType)
	mockReorgDetector.EXPECT().String().Return("mockReorgDetector")
	mockEthClient.EXPECT().CustomHeaderByNumber(mock.Anything, mock.Anything).
		Return(aggkittypes.NewBlockHeader(0, common.Hash{}, 0, nil), nil).Once()

	dbQueryTimeout := 30 * time.Second

	bridgeSyncCfg := Config{
		DBPath:                             dbPath,
		BridgeAddr:                         bridge,
		BlockFinality:                      aggkittypes.LatestBlock,
		SyncBlockChunkSize:                 syncBlockChunkSize,
		InitialBlockNum:                    initialBlock,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(waitForNewBlocksPeriod),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(retryAfterErrorPeriod),
		MaxRetryAttemptsAfterError:         maxRetryAttemptsAfterError,
		RequireStorageContentCompatibility: false,
		DBQueryTimeout:                     cfgtypes.NewDuration(dbQueryTimeout),
	}
	bridgeSyncCfg.SyncFromInBridges.Resolved = func() *bool { b := testSyncFromInBridges; return &b }()

	s, err := NewL2(
		ctx,
		bridgeSyncCfg,
		mockReorgDetector,
		mockEthClient,
		originNetwork,
		testSyncFromInBridges,
		bridgesynctypes.EmptyLER,
	)
	require.NoError(t, err)

	t.Run("subscribe to sync with valid parameters", func(t *testing.T) {
		subscriberID := "test-subscriber"

		blockChan := s.SubscribeToSync(subscriberID)
		require.NotNil(t, blockChan)

		// Verify the channel is not closed immediately
		select {
		case _, ok := <-blockChan:
			if !ok {
				t.Fatal("channel should not be closed immediately")
			}
		default:
			// Expected - no blocks available initially
		}
	})

	t.Run("subscribe with empty subscriber ID", func(t *testing.T) {
		subscriberID := ""

		blockChan := s.SubscribeToSync(subscriberID)
		require.NotNil(t, blockChan)
	})

	t.Run("multiple subscribers", func(t *testing.T) {
		subscriber1ID := "subscriber-1"
		subscriber2ID := "subscriber-2"

		blockChan1 := s.SubscribeToSync(subscriber1ID)
		blockChan2 := s.SubscribeToSync(subscriber2ID)

		require.NotNil(t, blockChan1)
		require.NotNil(t, blockChan2)

		// Channels should be different instances
		require.NotEqual(t, blockChan1, blockChan2)
	})

	t.Run("subscribe with same subscriber ID multiple times", func(t *testing.T) {
		subscriberID := "duplicate-subscriber"

		blockChan1 := s.SubscribeToSync(subscriberID)
		blockChan2 := s.SubscribeToSync(subscriberID)

		require.NotNil(t, blockChan1)
		require.NotNil(t, blockChan2)
	})
}

func TestBridgeSync_GetBridgeByDepositCount(t *testing.T) {
	ctx := context.Background()
	p := createTestProcessor(t, "test_bridgesync_get_bridge_by_deposit_count")
	s := BridgeSync{processor: p}

	originAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	destAddr := common.HexToAddress("0x2222222222222222222222222222222222222222")

	t.Run("returns ErrNotFound for missing deposit count", func(t *testing.T) {
		got, err := s.GetBridgeByDepositCount(ctx, 99)
		require.ErrorIs(t, err, db.ErrNotFound)
		require.Nil(t, got)
	})

	t.Run("returns bridge by deposit count", func(t *testing.T) {
		tx, err := p.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(1))
		require.NoError(t, err)
		bridge := &Bridge{
			BlockNum:           1,
			BlockPos:           0,
			DepositCount:       7,
			OriginNetwork:      0,
			OriginAddress:      originAddr,
			DestinationNetwork: 1,
			DestinationAddress: destAddr,
			Amount:             big.NewInt(500),
			LeafType:           0,
		}
		require.NoError(t, meddler.Insert(tx, "bridge", bridge))
		require.NoError(t, tx.Commit())

		got, err := s.GetBridgeByDepositCount(ctx, 7)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, uint32(7), got.DepositCount)
	})
}

func TestBridgeSync_GetBridgesByContent(t *testing.T) {
	ctx := context.Background()
	p := createTestProcessor(t, "test_bridgesync_get_bridges_by_content")
	s := BridgeSync{processor: p}

	originAddr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	destAddr := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	amount := big.NewInt(1000)

	t.Run("returns empty for no matching bridge", func(t *testing.T) {
		result, err := s.GetBridgesByContent(ctx, 0, originAddr, 1, destAddr, amount, nil)
		require.NoError(t, err)
		require.Empty(t, result)
	})

	t.Run("returns matching bridge without metadata", func(t *testing.T) {
		tx, err := p.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, uint64(1))
		require.NoError(t, err)
		bridge := &Bridge{
			BlockNum:           1,
			BlockPos:           0,
			DepositCount:       15,
			OriginNetwork:      0,
			LeafType:           0,
			OriginAddress:      originAddr,
			DestinationNetwork: 1,
			DestinationAddress: destAddr,
			Amount:             new(big.Int).Set(amount),
			Metadata:           nil,
		}
		require.NoError(t, meddler.Insert(tx, "bridge", bridge))
		require.NoError(t, tx.Commit())

		result, err := s.GetBridgesByContent(ctx, 0, originAddr, 1, destAddr, amount, nil)
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Equal(t, uint32(15), result[0].DepositCount)
	})
}
