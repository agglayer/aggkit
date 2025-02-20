package bridgesync

import (
	"context"
	"fmt"
	"path"
	"testing"
	"time"

	mocksbridgesync "github.com/agglayer/aggkit/bridgesync/mocks"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/sync"
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

	mockEthClient := mocksbridgesync.NewEthClienter(t)
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
	)

	require.NoError(t, err)
	require.NotNil(t, l1BridgeSync)
	require.Equal(t, originNetwork, l2BridgdeSync.OriginNetwork())
	require.Equal(t, blockFinalityType, l2BridgdeSync.BlockFinality())

	// Fails the sanity check of the contract address
	mockEthClient = mocksbridgesync.NewEthClienter(t)
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

func TestGetTokenMappings(t *testing.T) {
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

	mockEthClient := mocksbridgesync.NewEthClienter(t)
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
		require.ErrorIs(t, err, db.ErrNotFound)
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

func TestGetBridgePaged(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, _, err := s.GetBridgesPaged(context.Background(), 0, 0, nil)
	require.ErrorIs(t, err, sync.ErrInconsistentState)
}
