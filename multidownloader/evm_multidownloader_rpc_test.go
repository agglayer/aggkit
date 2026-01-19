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
