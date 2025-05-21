package mocks

import (
	"context"

	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/core/types"
)

// MockEVMDownloader is a mock implementation of sync.EVMDownloaderInterface
type MockEVMDownloader struct {
	RuntimeDataFunc           func(ctx context.Context) (*sync.RuntimeData, error)
	GetEthClientFunc          func() interface{}
	ChainIDFunc               func(ctx context.Context) (uint64, error)
	GetBlockHeaderFunc        func(ctx context.Context, blockNum uint64) (sync.EVMBlockHeader, bool)
	GetEventsByBlockRangeFunc func(ctx context.Context, fromBlock, toBlock uint64) sync.EVMBlocks
	GetLastFinalizedBlockFunc func(ctx context.Context) (*types.Header, error)
	GetLogsFunc               func(ctx context.Context, fromBlock, toBlock uint64) []types.Log
	WaitForNewBlocksFunc      func(ctx context.Context, lastBlockSeen uint64) uint64
}

// RuntimeData implements sync.EVMDownloaderInterface
func (m *MockEVMDownloader) RuntimeData(ctx context.Context) (*sync.RuntimeData, error) {
	if m.RuntimeDataFunc != nil {
		return m.RuntimeDataFunc(ctx)
	}
	return nil, nil
}

// GetEthClient implements sync.EVMDownloaderInterface
func (m *MockEVMDownloader) GetEthClient() interface{} {
	if m.GetEthClientFunc != nil {
		return m.GetEthClientFunc()
	}
	return nil
}

// ChainID implements sync.EVMDownloaderInterface
func (m *MockEVMDownloader) ChainID(ctx context.Context) (uint64, error) {
	if m.ChainIDFunc != nil {
		return m.ChainIDFunc(ctx)
	}
	return 0, nil
}

// GetBlockHeader implements sync.EVMDownloaderInterface
func (m *MockEVMDownloader) GetBlockHeader(ctx context.Context, blockNum uint64) (sync.EVMBlockHeader, bool) {
	if m.GetBlockHeaderFunc != nil {
		return m.GetBlockHeaderFunc(ctx, blockNum)
	}
	return sync.EVMBlockHeader{}, false
}

// GetEventsByBlockRange implements sync.EVMDownloaderInterface
func (m *MockEVMDownloader) GetEventsByBlockRange(ctx context.Context, fromBlock, toBlock uint64) sync.EVMBlocks {
	if m.GetEventsByBlockRangeFunc != nil {
		return m.GetEventsByBlockRangeFunc(ctx, fromBlock, toBlock)
	}
	return nil
}

// GetLastFinalizedBlock implements sync.EVMDownloaderInterface
func (m *MockEVMDownloader) GetLastFinalizedBlock(ctx context.Context) (*types.Header, error) {
	if m.GetLastFinalizedBlockFunc != nil {
		return m.GetLastFinalizedBlockFunc(ctx)
	}
	return nil, nil
}

// GetLogs implements sync.EVMDownloaderInterface
func (m *MockEVMDownloader) GetLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log {
	if m.GetLogsFunc != nil {
		return m.GetLogsFunc(ctx, fromBlock, toBlock)
	}
	return nil
}

// WaitForNewBlocks implements sync.EVMDownloaderInterface
func (m *MockEVMDownloader) WaitForNewBlocks(ctx context.Context, lastBlockSeen uint64) uint64 {
	if m.WaitForNewBlocksFunc != nil {
		return m.WaitForNewBlocksFunc(ctx, lastBlockSeen)
	}
	return 0
}

// EVMDownloaderInterface is the interface that MockEVMDownloader implements
type EVMDownloaderInterface interface {
	RuntimeData(ctx context.Context) (*sync.RuntimeData, error)
	GetEthClient() interface{}
	ChainID(ctx context.Context) (uint64, error)
	GetBlockHeader(ctx context.Context, blockNum uint64) (sync.EVMBlockHeader, bool)
	GetEventsByBlockRange(ctx context.Context, fromBlock, toBlock uint64) sync.EVMBlocks
	GetLastFinalizedBlock(ctx context.Context) (*types.Header, error)
	GetLogs(ctx context.Context, fromBlock, toBlock uint64) []types.Log
	WaitForNewBlocks(ctx context.Context, lastBlockSeen uint64) uint64
}
