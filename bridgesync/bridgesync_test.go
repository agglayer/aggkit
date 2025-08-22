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
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
	mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(
		common.FromHex("0x000000000000000000000000000000000000000000000000000000000000002a"), nil).Times(2)
	mockEthClient.EXPECT().
		CallContract(
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).
		Return(common.LeftPadBytes(common.HexToAddress("0x3c351e10").Bytes(), 32), nil).
		Maybe()
	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)

	mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(blockFinalityType)
	mockReorgDetector.EXPECT().String().Return("mockReorgDetector")
	l1BridgeSync, err := NewL1(
		ctx,
		dbPath,
		bridge,
		syncBlockChunkSize,
		blockFinalityType,
		mockEthClient,
		initialBlock,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		false,
		true,
	)

	require.NoError(t, err)
	require.NotNil(t, l1BridgeSync)
	require.Equal(t, originNetwork, l1BridgeSync.OriginNetwork())
	require.Equal(t, blockFinalityType, l1BridgeSync.BlockFinality())

	l2BridgdeSync, err := NewL2(
		ctx,
		dbPath,
		bridge,
		syncBlockChunkSize,
		blockFinalityType,
		mockReorgDetector,
		mockEthClient,
		initialBlock,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		false,
		true,
	)

	require.NoError(t, err)
	require.NotNil(t, l1BridgeSync)
	require.Equal(t, originNetwork, l2BridgdeSync.OriginNetwork())
	require.Equal(t, blockFinalityType, l2BridgdeSync.BlockFinality())

	mockEthClient = mocksethclient.NewEthClienter(t)
	mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
	mockEthClient.EXPECT().CodeAt(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
	l2BridgdeSyncErr, err := NewL2(
		ctx,
		dbPath,
		bridge,
		syncBlockChunkSize,
		blockFinalityType,
		mockReorgDetector,
		mockEthClient,
		initialBlock,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		false,
		true,
	)
	t.Log(err)
	require.Error(t, err)
	require.Nil(t, l2BridgdeSyncErr)
}

func TestGetLastProcessedBlock(t *testing.T) {
	s := BridgeSync{processor: &processor{
		halted: true,
		log:    log.WithFields("module", "L2BridgeSyncer"),
	}}
	_, err := s.GetLastProcessedBlock(context.Background())
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestGetBridgeRootByHash(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetBridgeRootByHash(context.Background(), common.Hash{})
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

func TestGetClaims(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetClaims(context.Background(), 0, 0)
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
	mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(
		common.FromHex("0x000000000000000000000000000000000000000000000000000000000000002a"), nil).Once()
	mockEthClient.EXPECT().
		CallContract(
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).
		Return(common.LeftPadBytes(common.HexToAddress("0x3c351e10").Bytes(), 32), nil).
		Maybe()
	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)

	mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(blockFinalityType)
	mockReorgDetector.EXPECT().String().Return("mockReorgDetector")

	s, err := NewL2(
		ctx,
		dbPath,
		bridge,
		syncBlockChunkSize,
		blockFinalityType,
		mockReorgDetector,
		mockEthClient,
		initialBlock,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		false,
		false,
	)
	require.NoError(t, err)

	allTokenMappings := make([]*TokenMapping, 0, tokenMappingsCount)
	genericEvts := make([]interface{}, 0, tokenMappingsCount)

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
		tokenMappings, totalTokenMappings, err := s.GetTokenMappings(context.Background(), 1, tokenMappingsCount)
		require.NoError(t, err)
		require.Equal(t, tokenMappingsCount, totalTokenMappings)
		require.Equal(t, allTokenMappings, tokenMappings)
	})

	t.Run("retrieve paginated mappings", func(t *testing.T) {
		pageSize := uint32(5)

		for page := uint32(1); page <= 4; page++ {
			tokenMappings, totalTokenMappings, err := s.GetTokenMappings(context.Background(), page, pageSize)
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

		tokenMappings, totalTokenMappings, err := s.GetTokenMappings(context.Background(), pageNum, pageSize)
		require.ErrorContains(t, err, "invalid page number for given page size and total number of token mappings")
		require.Equal(t, 0, totalTokenMappings)
		require.Nil(t, tokenMappings)
	})

	t.Run("provide invalid page number", func(t *testing.T) {
		pageSize := uint32(0)
		pageNum := uint32(0)

		_, _, err := s.GetTokenMappings(context.Background(), pageNum, pageSize)
		require.ErrorIs(t, err, ErrInvalidPageNumber)
	})

	t.Run("provide invalid page size", func(t *testing.T) {
		pageSize := uint32(0)
		pageNum := uint32(4)

		_, _, err := s.GetTokenMappings(context.Background(), pageNum, pageSize)
		require.ErrorIs(t, err, ErrInvalidPageSize)
	})

	t.Run("inconsistent state", func(t *testing.T) {
		s.processor.halted = true
		_, _, err := s.GetTokenMappings(context.Background(), 0, 0)
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
	mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(
		common.FromHex("0x000000000000000000000000000000000000000000000000000000000000002a"), nil).Once()
	mockEthClient.EXPECT().
		CallContract(
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).
		Return(common.LeftPadBytes(common.HexToAddress("0x3c351e10").Bytes(), 32), nil).
		Maybe()
	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)

	mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(blockFinalityType)
	mockReorgDetector.EXPECT().String().Return("mockReorgDetector")

	s, err := NewL2(
		ctx,
		dbPath,
		bridge,
		syncBlockChunkSize,
		blockFinalityType,
		mockReorgDetector,
		mockEthClient,
		initialBlock,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		false,
		false,
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

		_, _, err := s.GetTokenMappings(context.Background(), pageNum, pageSize)
		require.ErrorIs(t, err, ErrInvalidPageSize)
	})

	t.Run("inconsistent state", func(t *testing.T) {
		s.processor.halted = true
		_, _, err := s.GetTokenMappings(context.Background(), 0, 0)
		require.ErrorIs(t, err, sync.ErrInconsistentState)
	})
}

func TestGetBridgePaged(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, _, err := s.GetBridgesPaged(context.Background(), 0, 0, nil, nil, "")
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestGetClaimPaged(t *testing.T) {
	s := BridgeSync{processor: &processor{
		halted: true,
		log:    log.WithFields("module", "L2BridgeSyncer"),
	}}
	_, _, err := s.GetClaimsPaged(context.Background(), 0, 0, nil, "")
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
	mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(
		common.FromHex("0x000000000000000000000000000000000000000000000000000000000000002a"), nil).Once()
	mockEthClient.EXPECT().
		CallContract(
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).
		Return(common.LeftPadBytes(common.HexToAddress("0x3c351e10").Bytes(), 32), nil).
		Maybe()
	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)

	mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(blockFinalityType)
	mockReorgDetector.EXPECT().String().Return("mockReorgDetector")

	s, err := NewL2(
		ctx,
		dbPath,
		bridge,
		syncBlockChunkSize,
		blockFinalityType,
		mockReorgDetector,
		mockEthClient,
		initialBlock,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		false,
		false,
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
				BlockNum:           1,
				BlockPos:           0,
				FromAddress:        common.HexToAddress("0x1111111111111111111111111111111111111111"),
				TxHash:             common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
				Calldata:           []byte{0x01, 0x02, 0x03},
				BlockTimestamp:     1234567890,
				LeafType:           1,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x3333333333333333333333333333333333333333"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
				Amount:             big.NewInt(1000000),
				Metadata:           []byte{0x04, 0x05, 0x06},
				DepositCount:       0,
				IsNativeToken:      true,
			}},
			Event{Bridge: &Bridge{
				BlockNum:           1,
				BlockPos:           1,
				FromAddress:        common.HexToAddress("0x5555555555555555555555555555555555555555"),
				TxHash:             common.HexToHash("0x6666666666666666666666666666666666666666666666666666666666666666"),
				Calldata:           []byte{0x07, 0x08, 0x09},
				BlockTimestamp:     1234567890,
				LeafType:           1,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x7777777777777777777777777777777777777777"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x8888888888888888888888888888888888888888"),
				Amount:             big.NewInt(2000000),
				Metadata:           []byte{0x0a, 0x0b, 0x0c},
				DepositCount:       1,
				IsNativeToken:      false,
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
				BlockNum:           2,
				BlockPos:           0,
				FromAddress:        common.HexToAddress("0x9999999999999999999999999999999999999999"),
				TxHash:             common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
				Calldata:           []byte{0x0d, 0x0e, 0x0f},
				BlockTimestamp:     1234567891,
				LeafType:           1,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc"),
				Amount:             big.NewInt(3000000),
				Metadata:           []byte{0x10, 0x11, 0x12},
				DepositCount:       2,
				IsNativeToken:      true,
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

//nolint:tparallel
func TestBridgeSync_GetClaimByGlobalIndex(t *testing.T) {
	t.Parallel()

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
		dbPath            = path.Join(t.TempDir(), "TestBridgeSync_GetClaimByGlobalIndex.sqlite")
		bridge            = common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	)

	mockEthClient := mocksethclient.NewEthClienter(t)
	mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(
		common.FromHex("0x000000000000000000000000000000000000000000000000000000000000002a"), nil).Times(2)
	mockEthClient.EXPECT().
		CallContract(
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).
		Return(common.LeftPadBytes(common.HexToAddress("0x3c351e10").Bytes(), 32), nil).
		Maybe()

	mockReorgDetector := mocksbridgesync.NewReorgDetector(t)
	mockReorgDetector.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	mockReorgDetector.EXPECT().GetFinalizedBlockType().Return(blockFinalityType)
	mockReorgDetector.EXPECT().String().Return("mockReorgDetector")

	s, err := NewL2(
		ctx,
		dbPath,
		bridge,
		syncBlockChunkSize,
		blockFinalityType,
		mockReorgDetector,
		mockEthClient,
		initialBlock,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		false,
		false,
	)
	require.NoError(t, err)

	// Create test claims
	testClaims := []*Claim{
		{
			BlockNum:            5,
			BlockPos:            1,
			FromAddress:         common.HexToAddress("0x1111111111111111111111111111111111111111"),
			TxHash:              common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
			GlobalIndex:         big.NewInt(100),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0x3333333333333333333333333333333333333333"),
			DestinationAddress:  common.HexToAddress("0x4444444444444444444444444444444444444444"),
			Amount:              big.NewInt(1000),
			ProofLocalExitRoot:  types.Proof{},
			ProofRollupExitRoot: types.Proof{},
			MainnetExitRoot:     common.HexToHash("0x5555555555555555555555555555555555555555555555555555555555555555"),
			RollupExitRoot:      common.HexToHash("0x6666666666666666666666666666666666666666666666666666666666666666"),
			GlobalExitRoot:      common.HexToHash("0x7777777777777777777777777777777777777777777777777777777777777777"),
			DestinationNetwork:  2,
			Metadata:            []byte("test metadata 1"),
			IsMessage:           false,
			BlockTimestamp:      1234567890,
		},
		{
			BlockNum:            7,
			BlockPos:            2,
			FromAddress:         common.HexToAddress("0x8888888888888888888888888888888888888888"),
			TxHash:              common.HexToHash("0x9999999999999999999999999999999999999999999999999999999999999999"),
			GlobalIndex:         big.NewInt(200),
			OriginNetwork:       1,
			OriginAddress:       common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			DestinationAddress:  common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			Amount:              big.NewInt(2000),
			ProofLocalExitRoot:  types.Proof{},
			ProofRollupExitRoot: types.Proof{},
			MainnetExitRoot:     common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
			RollupExitRoot:      common.HexToHash("0xdddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
			GlobalExitRoot:      common.HexToHash("0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
			DestinationNetwork:  2,
			Metadata:            []byte("test metadata 2"),
			IsMessage:           true,
			BlockTimestamp:      1234567891,
		},
	}

	// Process claims by creating claim events directly
	for _, claim := range testClaims {
		claimEvent := Event{
			Claim: claim,
		}

		block := sync.Block{
			Num:    claim.BlockNum,
			Events: []interface{}{claimEvent},
		}

		err = s.processor.ProcessBlock(ctx, block)
		require.NoError(t, err)
	}

	t.Run("successful retrieval of claim by global index", func(t *testing.T) {
		globalIndex := big.NewInt(100)
		blockNumber := uint64(10)

		claim, err := s.GetClaimByGlobalIndex(ctx, globalIndex, blockNumber)
		require.NoError(t, err)
		require.NotEqual(t, Claim{}, claim)
		require.Equal(t, globalIndex, claim.GlobalIndex)
		require.Equal(t, uint64(5), claim.BlockNum)
		require.Equal(t, uint64(1), claim.BlockPos)
		require.Equal(t, big.NewInt(1000), claim.Amount)
		require.Equal(t, []byte("test metadata 1"), claim.Metadata)
		require.False(t, claim.IsMessage)
	})

	t.Run("successful retrieval of claim with higher global index", func(t *testing.T) {
		globalIndex := big.NewInt(200)
		blockNumber := uint64(10)

		claim, err := s.GetClaimByGlobalIndex(ctx, globalIndex, blockNumber)
		require.NoError(t, err)
		require.NotEqual(t, Claim{}, claim)
		require.Equal(t, globalIndex, claim.GlobalIndex)
		require.Equal(t, uint64(7), claim.BlockNum)
		require.Equal(t, uint64(2), claim.BlockPos)
		require.Equal(t, big.NewInt(2000), claim.Amount)
		require.Equal(t, []byte("test metadata 2"), claim.Metadata)
		require.True(t, claim.IsMessage)
	})

	t.Run("returns error when processor is halted", func(t *testing.T) {
		s.processor.halted = true
		defer func() { s.processor.halted = false }()

		globalIndex := big.NewInt(100)
		blockNumber := uint64(10)

		claim, err := s.GetClaimByGlobalIndex(ctx, globalIndex, blockNumber)
		require.ErrorIs(t, err, sync.ErrInconsistentState)
		require.Equal(t, Claim{}, claim)
	})

	t.Run("returns error for non-existent global index", func(t *testing.T) {
		globalIndex := big.NewInt(999)
		blockNumber := uint64(10)

		claim, err := s.GetClaimByGlobalIndex(ctx, globalIndex, blockNumber)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get claim by global index")
		require.Contains(t, err.Error(), "globalIndex: 999")
		require.Equal(t, Claim{}, claim)
	})

	t.Run("returns error for block number before claim", func(t *testing.T) {
		globalIndex := big.NewInt(100)
		blockNumber := uint64(3) // Before the claim at block 5

		claim, err := s.GetClaimByGlobalIndex(ctx, globalIndex, blockNumber)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get claim by global index")
		require.Equal(t, Claim{}, claim)
	})

	t.Run("handles nil global index gracefully", func(t *testing.T) {
		var globalIndex *big.Int = nil
		blockNumber := uint64(10)

		claim, err := s.GetClaimByGlobalIndex(ctx, globalIndex, blockNumber)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get claim by global index")
		require.Equal(t, Claim{}, claim)
	})
}
