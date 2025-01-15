// Code generated by mockery v2.51.0. DO NOT EDIT.

package sync

import (
	context "context"

	types "github.com/ethereum/go-ethereum/core/types"
	mock "github.com/stretchr/testify/mock"
)

// EVMDownloaderMock is an autogenerated mock type for the evmDownloaderFull type
type EVMDownloaderMock struct {
	mock.Mock
}

type EVMDownloaderMock_Expecter struct {
	mock *mock.Mock
}

func (_m *EVMDownloaderMock) EXPECT() *EVMDownloaderMock_Expecter {
	return &EVMDownloaderMock_Expecter{mock: &_m.Mock}
}

// Download provides a mock function with given fields: ctx, fromBlock, downloadedCh
func (_m *EVMDownloaderMock) Download(ctx context.Context, fromBlock uint64, downloadedCh chan EVMBlock) {
	_m.Called(ctx, fromBlock, downloadedCh)
}

// EVMDownloaderMock_Download_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Download'
type EVMDownloaderMock_Download_Call struct {
	*mock.Call
}

// Download is a helper method to define mock.On call
//   - ctx context.Context
//   - fromBlock uint64
//   - downloadedCh chan EVMBlock
func (_e *EVMDownloaderMock_Expecter) Download(ctx interface{}, fromBlock interface{}, downloadedCh interface{}) *EVMDownloaderMock_Download_Call {
	return &EVMDownloaderMock_Download_Call{Call: _e.mock.On("Download", ctx, fromBlock, downloadedCh)}
}

func (_c *EVMDownloaderMock_Download_Call) Run(run func(ctx context.Context, fromBlock uint64, downloadedCh chan EVMBlock)) *EVMDownloaderMock_Download_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint64), args[2].(chan EVMBlock))
	})
	return _c
}

func (_c *EVMDownloaderMock_Download_Call) Return() *EVMDownloaderMock_Download_Call {
	_c.Call.Return()
	return _c
}

func (_c *EVMDownloaderMock_Download_Call) RunAndReturn(run func(context.Context, uint64, chan EVMBlock)) *EVMDownloaderMock_Download_Call {
	_c.Call.Return(run)
	return _c
}

// GetBlockHeader provides a mock function with given fields: ctx, blockNum
func (_m *EVMDownloaderMock) GetBlockHeader(ctx context.Context, blockNum uint64) (EVMBlockHeader, bool) {
	ret := _m.Called(ctx, blockNum)

	if len(ret) == 0 {
		panic("no return value specified for GetBlockHeader")
	}

	var r0 EVMBlockHeader
	var r1 bool
	if rf, ok := ret.Get(0).(func(context.Context, uint64) (EVMBlockHeader, bool)); ok {
		return rf(ctx, blockNum)
	}
	if rf, ok := ret.Get(0).(func(context.Context, uint64) EVMBlockHeader); ok {
		r0 = rf(ctx, blockNum)
	} else {
		r0 = ret.Get(0).(EVMBlockHeader)
	}

	if rf, ok := ret.Get(1).(func(context.Context, uint64) bool); ok {
		r1 = rf(ctx, blockNum)
	} else {
		r1 = ret.Get(1).(bool)
	}

	return r0, r1
}

// EVMDownloaderMock_GetBlockHeader_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetBlockHeader'
type EVMDownloaderMock_GetBlockHeader_Call struct {
	*mock.Call
}

// GetBlockHeader is a helper method to define mock.On call
//   - ctx context.Context
//   - blockNum uint64
func (_e *EVMDownloaderMock_Expecter) GetBlockHeader(ctx interface{}, blockNum interface{}) *EVMDownloaderMock_GetBlockHeader_Call {
	return &EVMDownloaderMock_GetBlockHeader_Call{Call: _e.mock.On("GetBlockHeader", ctx, blockNum)}
}

func (_c *EVMDownloaderMock_GetBlockHeader_Call) Run(run func(ctx context.Context, blockNum uint64)) *EVMDownloaderMock_GetBlockHeader_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint64))
	})
	return _c
}

func (_c *EVMDownloaderMock_GetBlockHeader_Call) Return(_a0 EVMBlockHeader, _a1 bool) *EVMDownloaderMock_GetBlockHeader_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EVMDownloaderMock_GetBlockHeader_Call) RunAndReturn(run func(context.Context, uint64) (EVMBlockHeader, bool)) *EVMDownloaderMock_GetBlockHeader_Call {
	_c.Call.Return(run)
	return _c
}

// GetEventsByBlockRange provides a mock function with given fields: ctx, fromBlock, toBlock
func (_m *EVMDownloaderMock) GetEventsByBlockRange(ctx context.Context, fromBlock uint64, toBlock uint64) EVMBlocks {
	ret := _m.Called(ctx, fromBlock, toBlock)

	if len(ret) == 0 {
		panic("no return value specified for GetEventsByBlockRange")
	}

	var r0 EVMBlocks
	if rf, ok := ret.Get(0).(func(context.Context, uint64, uint64) EVMBlocks); ok {
		r0 = rf(ctx, fromBlock, toBlock)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(EVMBlocks)
		}
	}

	return r0
}

// EVMDownloaderMock_GetEventsByBlockRange_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetEventsByBlockRange'
type EVMDownloaderMock_GetEventsByBlockRange_Call struct {
	*mock.Call
}

// GetEventsByBlockRange is a helper method to define mock.On call
//   - ctx context.Context
//   - fromBlock uint64
//   - toBlock uint64
func (_e *EVMDownloaderMock_Expecter) GetEventsByBlockRange(ctx interface{}, fromBlock interface{}, toBlock interface{}) *EVMDownloaderMock_GetEventsByBlockRange_Call {
	return &EVMDownloaderMock_GetEventsByBlockRange_Call{Call: _e.mock.On("GetEventsByBlockRange", ctx, fromBlock, toBlock)}
}

func (_c *EVMDownloaderMock_GetEventsByBlockRange_Call) Run(run func(ctx context.Context, fromBlock uint64, toBlock uint64)) *EVMDownloaderMock_GetEventsByBlockRange_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint64), args[2].(uint64))
	})
	return _c
}

func (_c *EVMDownloaderMock_GetEventsByBlockRange_Call) Return(_a0 EVMBlocks) *EVMDownloaderMock_GetEventsByBlockRange_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *EVMDownloaderMock_GetEventsByBlockRange_Call) RunAndReturn(run func(context.Context, uint64, uint64) EVMBlocks) *EVMDownloaderMock_GetEventsByBlockRange_Call {
	_c.Call.Return(run)
	return _c
}

// GetLastFinalizedBlock provides a mock function with given fields: ctx
func (_m *EVMDownloaderMock) GetLastFinalizedBlock(ctx context.Context) (*types.Header, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for GetLastFinalizedBlock")
	}

	var r0 *types.Header
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (*types.Header, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) *types.Header); ok {
		r0 = rf(ctx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.Header)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EVMDownloaderMock_GetLastFinalizedBlock_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetLastFinalizedBlock'
type EVMDownloaderMock_GetLastFinalizedBlock_Call struct {
	*mock.Call
}

// GetLastFinalizedBlock is a helper method to define mock.On call
//   - ctx context.Context
func (_e *EVMDownloaderMock_Expecter) GetLastFinalizedBlock(ctx interface{}) *EVMDownloaderMock_GetLastFinalizedBlock_Call {
	return &EVMDownloaderMock_GetLastFinalizedBlock_Call{Call: _e.mock.On("GetLastFinalizedBlock", ctx)}
}

func (_c *EVMDownloaderMock_GetLastFinalizedBlock_Call) Run(run func(ctx context.Context)) *EVMDownloaderMock_GetLastFinalizedBlock_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *EVMDownloaderMock_GetLastFinalizedBlock_Call) Return(_a0 *types.Header, _a1 error) *EVMDownloaderMock_GetLastFinalizedBlock_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EVMDownloaderMock_GetLastFinalizedBlock_Call) RunAndReturn(run func(context.Context) (*types.Header, error)) *EVMDownloaderMock_GetLastFinalizedBlock_Call {
	_c.Call.Return(run)
	return _c
}

// GetLogs provides a mock function with given fields: ctx, fromBlock, toBlock
func (_m *EVMDownloaderMock) GetLogs(ctx context.Context, fromBlock uint64, toBlock uint64) []types.Log {
	ret := _m.Called(ctx, fromBlock, toBlock)

	if len(ret) == 0 {
		panic("no return value specified for GetLogs")
	}

	var r0 []types.Log
	if rf, ok := ret.Get(0).(func(context.Context, uint64, uint64) []types.Log); ok {
		r0 = rf(ctx, fromBlock, toBlock)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]types.Log)
		}
	}

	return r0
}

// EVMDownloaderMock_GetLogs_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetLogs'
type EVMDownloaderMock_GetLogs_Call struct {
	*mock.Call
}

// GetLogs is a helper method to define mock.On call
//   - ctx context.Context
//   - fromBlock uint64
//   - toBlock uint64
func (_e *EVMDownloaderMock_Expecter) GetLogs(ctx interface{}, fromBlock interface{}, toBlock interface{}) *EVMDownloaderMock_GetLogs_Call {
	return &EVMDownloaderMock_GetLogs_Call{Call: _e.mock.On("GetLogs", ctx, fromBlock, toBlock)}
}

func (_c *EVMDownloaderMock_GetLogs_Call) Run(run func(ctx context.Context, fromBlock uint64, toBlock uint64)) *EVMDownloaderMock_GetLogs_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint64), args[2].(uint64))
	})
	return _c
}

func (_c *EVMDownloaderMock_GetLogs_Call) Return(_a0 []types.Log) *EVMDownloaderMock_GetLogs_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *EVMDownloaderMock_GetLogs_Call) RunAndReturn(run func(context.Context, uint64, uint64) []types.Log) *EVMDownloaderMock_GetLogs_Call {
	_c.Call.Return(run)
	return _c
}

// WaitForNewBlocks provides a mock function with given fields: ctx, lastBlockSeen
func (_m *EVMDownloaderMock) WaitForNewBlocks(ctx context.Context, lastBlockSeen uint64) uint64 {
	ret := _m.Called(ctx, lastBlockSeen)

	if len(ret) == 0 {
		panic("no return value specified for WaitForNewBlocks")
	}

	var r0 uint64
	if rf, ok := ret.Get(0).(func(context.Context, uint64) uint64); ok {
		r0 = rf(ctx, lastBlockSeen)
	} else {
		r0 = ret.Get(0).(uint64)
	}

	return r0
}

// EVMDownloaderMock_WaitForNewBlocks_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'WaitForNewBlocks'
type EVMDownloaderMock_WaitForNewBlocks_Call struct {
	*mock.Call
}

// WaitForNewBlocks is a helper method to define mock.On call
//   - ctx context.Context
//   - lastBlockSeen uint64
func (_e *EVMDownloaderMock_Expecter) WaitForNewBlocks(ctx interface{}, lastBlockSeen interface{}) *EVMDownloaderMock_WaitForNewBlocks_Call {
	return &EVMDownloaderMock_WaitForNewBlocks_Call{Call: _e.mock.On("WaitForNewBlocks", ctx, lastBlockSeen)}
}

func (_c *EVMDownloaderMock_WaitForNewBlocks_Call) Run(run func(ctx context.Context, lastBlockSeen uint64)) *EVMDownloaderMock_WaitForNewBlocks_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint64))
	})
	return _c
}

func (_c *EVMDownloaderMock_WaitForNewBlocks_Call) Return(newLastBlock uint64) *EVMDownloaderMock_WaitForNewBlocks_Call {
	_c.Call.Return(newLastBlock)
	return _c
}

func (_c *EVMDownloaderMock_WaitForNewBlocks_Call) RunAndReturn(run func(context.Context, uint64) uint64) *EVMDownloaderMock_WaitForNewBlocks_Call {
	_c.Call.Return(run)
	return _c
}

// NewEVMDownloaderMock creates a new instance of EVMDownloaderMock. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewEVMDownloaderMock(t interface {
	mock.TestingT
	Cleanup(func())
}) *EVMDownloaderMock {
	mock := &EVMDownloaderMock{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
