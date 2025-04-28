package bridgesync

import (
	"context"
	"errors"
	"path"
	"testing"
	"time"

	mocksbridgesync "github.com/agglayer/aggkit/bridgesync/mocks"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Mock implementations for the interfaces
type MockEthClienter struct {
	mock.Mock
}

type MockBridgeContractor struct {
	mock.Mock
}

func TestNewLx(t *testing.T) {
	ctx := context.Background()
	dbPath := path.Join(t.TempDir(), "TestNewLx.sqlite")
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	const (
		syncBlockChunkSize         = uint64(100)
		initialBlock               = uint64(0)
		waitForNewBlocksPeriod     = time.Second * 10
		retryAfterErrorPeriod      = time.Second * 5
		maxRetryAttemptsAfterError = 3
		originNetwork              = uint32(1)
	)
	var blockFinalityType = etherman.SafeBlock

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
		true,
	)

	assert.NoError(t, err)
	assert.NotNil(t, l1BridgeSync)
	assert.Equal(t, originNetwork, l1BridgeSync.OriginNetwork())
	assert.Equal(t, blockFinalityType, l1BridgeSync.BlockFinality())

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

	assert.NoError(t, err)
	assert.NotNil(t, l1BridgeSync)
	assert.Equal(t, originNetwork, l2BridgdeSync.OriginNetwork())
	assert.Equal(t, blockFinalityType, l2BridgdeSync.BlockFinality())

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
		true,
	)
	t.Log(err)
	assert.Error(t, err)
	assert.Nil(t, l2BridgdeSyncErr)
}

func TestGetLastProcessedBlock(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetLastProcessedBlock(context.Background())
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetBridgeRootByHash(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetBridgeRootByHash(context.Background(), common.Hash{})
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetBridges(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetBridges(context.Background(), 0, 0)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetProof(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetProof(context.Background(), 0, common.Hash{})
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetBlockByLER(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetBlockByLER(context.Background(), common.Hash{})
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetRootByLER(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetRootByLER(context.Background(), common.Hash{})
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetExitRootByIndex(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetExitRootByIndex(context.Background(), 0)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetClaims(t *testing.T) {
	s := BridgeSync{processor: &processor{halted: true}}
	_, err := s.GetClaims(context.Background(), 0, 0)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}
