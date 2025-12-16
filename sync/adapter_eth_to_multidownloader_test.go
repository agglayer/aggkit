package sync

import (
	"fmt"
	"math/big"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestAdapterEthClientToMultidownloader_ChainID(t *testing.T) {
	mockEthClient := mocks.NewBaseEthereumClienter(t)
	exampleError := fmt.Errorf("example error")
	sut := NewAdapterEthClientToMultidownloader(mockEthClient)
	t.Run("chainID defined", func(t *testing.T) {
		expectedID := uint64(137)
		bigIntID := big.NewInt(int64(expectedID))
		mockEthClient.EXPECT().ChainID(t.Context()).Return(bigIntID, nil)
		chainID, err := sut.ChainID(t.Context())
		require.NoError(t, err)
		require.Equal(t, expectedID, chainID)
	})
	t.Run("chainID error", func(t *testing.T) {
		mockEthClient.EXPECT().ChainID(t.Context()).Return(nil, exampleError)
		_, err := sut.ChainID(t.Context())
		require.Error(t, err)
	})
	t.Run("chainID nil", func(t *testing.T) {
		mockEthClient.EXPECT().ChainID(t.Context()).Return(nil, nil)
		_, err := sut.ChainID(t.Context())
		require.Error(t, err)
	})
}

func TestAdapterEthClientToMultidownloader_BlockNumber(t *testing.T) {
	mockEthClient := mocks.NewBaseEthereumClienter(t)
	sut := NewAdapterEthClientToMultidownloader(mockEthClient)

	header := &aggkittypes.BlockHeader{
		Number: 12345,
	}
	mockEthClient.EXPECT().CustomHeaderByNumber(t.Context(), mock.Anything).Return(header, nil)
	blockNumber, err := sut.BlockNumber(t.Context(), aggkittypes.FinalizedBlock)
	require.NoError(t, err)
	require.Equal(t, uint64(12345), blockNumber)
}

func TestAdapterEthClientToMultidownloader_BlockHeader(t *testing.T) {
	mockEthClient := mocks.NewBaseEthereumClienter(t)
	sut := NewAdapterEthClientToMultidownloader(mockEthClient)

	header := &aggkittypes.BlockHeader{
		Number: 12345,
	}
	mockEthClient.EXPECT().CustomHeaderByNumber(t.Context(), mock.Anything).Return(header, nil)
	blockHeader, err := sut.BlockHeader(t.Context(), aggkittypes.FinalizedBlock)
	require.NoError(t, err)
	require.Equal(t, uint64(12345), blockHeader.Number)
}
func TestAdapterEthClientToMultidownloader_HeaderByNumber(t *testing.T) {
	mockEthClient := mocks.NewBaseEthereumClienter(t)
	sut := NewAdapterEthClientToMultidownloader(mockEthClient)

	header := &aggkittypes.BlockHeader{
		Number: 12345,
	}
	mockEthClient.EXPECT().CustomHeaderByNumber(t.Context(), aggkittypes.NewBlockNumber(12345)).Return(header, nil)
	blockHeader, err := sut.HeaderByNumber(t.Context(), aggkittypes.NewBlockNumber(12345))
	require.NoError(t, err)
	require.Equal(t, uint64(12345), blockHeader.Number)
}

func TestAdapterEthClientToMultidownloader_FilterLogs(t *testing.T) {
	mockEthClient := mocks.NewBaseEthereumClienter(t)
	sut := NewAdapterEthClientToMultidownloader(mockEthClient)

	query := ethereum.FilterQuery{}
	expectedLogs := []types.Log{}
	mockEthClient.EXPECT().FilterLogs(t.Context(), mock.Anything).Return(expectedLogs, nil)
	logs, err := sut.FilterLogs(t.Context(), query)
	require.NoError(t, err)
	require.Equal(t, expectedLogs, logs)
}

func TestAdapterEthClientToMultidownloader_EthClient(t *testing.T) {
	mockEthClient := mocks.NewBaseEthereumClienter(t)
	sut := NewAdapterEthClientToMultidownloader(mockEthClient)

	returnedClient := sut.EthClient()
	require.Equal(t, mockEthClient, returnedClient)
}

func TestAdapterEthClientToMultidownloader_RegisterSyncer(t *testing.T) {
	mockEthClient := mocks.NewBaseEthereumClienter(t)
	sut := NewAdapterEthClientToMultidownloader(mockEthClient)

	err := sut.RegisterSyncer(aggkittypes.SyncerConfig{})
	require.NoError(t, err)
}

func TestAdapterEthClientToMultidownloader_Start(t *testing.T) {
	mockEthClient := mocks.NewBaseEthereumClienter(t)
	sut := NewAdapterEthClientToMultidownloader(mockEthClient)

	err := sut.Start(t.Context())
	require.NoError(t, err)
}
