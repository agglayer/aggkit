package multidownloader

import (
	"testing"

	"github.com/agglayer/aggkit/log"
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
	downloader := &EVMMultidownloader{}
	rpcService := NewEVMMultidownloaderRPC(logger, downloader)

	result, err := rpcService.Status()

	require.Nil(t, err)
	require.NotNil(t, result)

	statusInfo, ok := result.(struct {
		Status string `json:"status"`
	})
	require.True(t, ok)
	require.Equal(t, "running", statusInfo.Status)
}
