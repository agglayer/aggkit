package blocknotifier

import (
	"testing"

	ethermantypes "github.com/agglayer/aggkit/etherman/types"
	ethermantypesmocks "github.com/agglayer/aggkit/etherman/types/mocks"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBlockNotifierManager_GetBlockNotifier(t *testing.T) {
	logger := log.WithFields("test", "test")
	mockBlockNotifier := ethermantypesmocks.NewBlockNotifier(t)
	constFunc := func(finality aggkittypes.BlockNumberFinality) (ethermantypes.BlockNotifier, error) {
		return mockBlockNotifier, nil
	}
	sut := NewBlockNotifierManager(logger, constFunc)
	require.NotNil(t, sut)
	mockBlockNotifier.EXPECT().Initialize(mock.Anything).Return(nil)
	mockBlockNotifier.EXPECT().GetCurrentBlockNumber().Return(uint64(1234))
	mockBlockNotifier.EXPECT().Start(mock.Anything).Return().Maybe()
	blockNotifier, err := sut.GetBlockNotifier(t.Context(), aggkittypes.LatestBlock)
	require.NoError(t, err)
	require.Equal(t, mockBlockNotifier, blockNotifier)
}

func TestBlockNotifierManager_GetCurrentBlockNumber(t *testing.T) {
	logger := log.WithFields("test", "test")
	mockBlockNotifier := ethermantypesmocks.NewBlockNotifier(t)
	constFunc := func(finality aggkittypes.BlockNumberFinality) (ethermantypes.BlockNotifier, error) {
		return mockBlockNotifier, nil
	}
	sut := NewBlockNotifierManager(logger, constFunc)
	require.NotNil(t, sut)
	mockBlockNotifier.EXPECT().Initialize(mock.Anything).Return(nil)
	mockBlockNotifier.EXPECT().GetCurrentBlockNumber().Return(uint64(1234))
	mockBlockNotifier.EXPECT().Start(mock.Anything).Return().Maybe()
	currentBlockNumber, err := sut.GetCurrentBlockNumber(t.Context(), aggkittypes.LatestBlock)
	require.NoError(t, err)
	require.Equal(t, uint64(1234), currentBlockNumber)
}
