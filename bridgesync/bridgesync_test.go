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
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
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
		blockFinalityType = etherman.SafeBlock
		ctx               = context.Background()
		dbPath            = path.Join(t.TempDir(), "TestNewLx.sqlite")
		bridge            = common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	)

	mockEthClient := mocksethclient.NewEthClienter(t)
	mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(
		common.FromHex("0x000000000000000000000000000000000000000000000000000000000000002a"), nil).Times(2)
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

	// Fails the sanity check of the contract address
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
	s := BridgeSync{processor: &processor{halted: true}}
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

func TestGetBridgesPublishedTopLevel(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetBridgesPublished(context.Background(), 0, 0)
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
		blockFinalityType = etherman.SafeBlock
		ctx               = context.Background()
		dbPath            = path.Join(t.TempDir(), "TestGetTokenMappings.sqlite")
		bridge            = common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	)

	mockEthClient := mocksethclient.NewEthClienter(t)
	mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(
		common.FromHex("0x000000000000000000000000000000000000000000000000000000000000002a"), nil).Once()
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
		require.ErrorContains(t, err, "provided page number is invalid for given page size")
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
		blockFinalityType = etherman.SafeBlock
		ctx               = context.Background()
		dbPath            = path.Join(t.TempDir(), "TestGetTokenMigrations.sqlite")
		bridge            = common.HexToAddress("0x123456")
	)

	mockEthClient := mocksethclient.NewEthClienter(t)
	mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(
		common.FromHex("0x000000000000000000000000000000000000000000000000000000000000002a"), nil).Once()
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
			"provided page number is invalid for given page size and total number of legacy token migrations")
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
	_, _, err := s.GetBridgesPaged(context.Background(), 0, 0, nil)
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}

func TestGetClaimPaged(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, _, err := s.GetClaimsPaged(context.Background(), 0, 0)
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
