package multidownloader

import (
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewEVMMultidownloaderRPC(t *testing.T) {
	logger := log.WithFields("module", "test")
	downloader := &EVMMultidownloader{}

	rpcService := NewEVMMultidownloaderRPC(logger, downloader)

	require.NotNil(t, rpcService)
	require.Equal(t, logger, rpcService.logger)
	require.Equal(t, downloader, rpcService.downloader)
}

func TestEVMMultidownloaderRPC_Status(t *testing.T) {
	logger := log.WithFields("module", "test")
	testData := newEVMMultidownloaderTestData(t, false)
	testData.mdr.state = NewEmptyState()
	testData.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything,
		mock.Anything).Return(uint64(100), nil)
	rpcService := NewEVMMultidownloaderRPC(logger, testData.mdr)

	result, err := rpcService.Status()

	require.Nil(t, err)
	require.NotNil(t, result)

	require.Contains(t, fmt.Sprintf("%+v", result), "Status")
}

func TestEVMMultidownloaderRPC_Reorg(t *testing.T) {
	testData := newEVMMultidownloaderTestData(t, false)
	t.Run("returns error if debug is not enabled", func(t *testing.T) {
		sut := EVMMultidownloaderRPC{
			logger:     log.WithFields("module", "test"),
			downloader: testData.mdr,
		}
		_, err := sut.Reorg(123)
		require.Error(t, err)
		require.Contains(t, err.Error(), "debug is not enabled")
	})
	t.Run("calls ForceReorg on downloader when debug is enabled", func(t *testing.T) {
		testData.mdr.debug = &EVMMultidownloaderDebug{}
		sut := EVMMultidownloaderRPC{
			logger:     log.WithFields("module", "test"),
			downloader: testData.mdr,
		}
		_, err := sut.Reorg(123)
		require.NoError(t, err)
	})
}
