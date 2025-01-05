// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	big "math/big"

	common "github.com/ethereum/go-ethereum/common"

	context "context"

	ethereum "github.com/ethereum/go-ethereum"

	mock "github.com/stretchr/testify/mock"

	rpc "github.com/ethereum/go-ethereum/rpc"

	types "github.com/ethereum/go-ethereum/core/types"
)

// EthClienter is an autogenerated mock type for the EthClienter type
type EthClienter struct {
	mock.Mock
}

type EthClienter_Expecter struct {
	mock *mock.Mock
}

func (_m *EthClienter) EXPECT() *EthClienter_Expecter {
	return &EthClienter_Expecter{mock: &_m.Mock}
}

// BlockByHash provides a mock function with given fields: ctx, hash
func (_m *EthClienter) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	ret := _m.Called(ctx, hash)

	if len(ret) == 0 {
		panic("no return value specified for BlockByHash")
	}

	var r0 *types.Block
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash) (*types.Block, error)); ok {
		return rf(ctx, hash)
	}
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash) *types.Block); ok {
		r0 = rf(ctx, hash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.Block)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, common.Hash) error); ok {
		r1 = rf(ctx, hash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_BlockByHash_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'BlockByHash'
type EthClienter_BlockByHash_Call struct {
	*mock.Call
}

// BlockByHash is a helper method to define mock.On call
//   - ctx context.Context
//   - hash common.Hash
func (_e *EthClienter_Expecter) BlockByHash(ctx interface{}, hash interface{}) *EthClienter_BlockByHash_Call {
	return &EthClienter_BlockByHash_Call{Call: _e.mock.On("BlockByHash", ctx, hash)}
}

func (_c *EthClienter_BlockByHash_Call) Run(run func(ctx context.Context, hash common.Hash)) *EthClienter_BlockByHash_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Hash))
	})
	return _c
}

func (_c *EthClienter_BlockByHash_Call) Return(_a0 *types.Block, _a1 error) *EthClienter_BlockByHash_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_BlockByHash_Call) RunAndReturn(run func(context.Context, common.Hash) (*types.Block, error)) *EthClienter_BlockByHash_Call {
	_c.Call.Return(run)
	return _c
}

// BlockByNumber provides a mock function with given fields: ctx, number
func (_m *EthClienter) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	ret := _m.Called(ctx, number)

	if len(ret) == 0 {
		panic("no return value specified for BlockByNumber")
	}

	var r0 *types.Block
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *big.Int) (*types.Block, error)); ok {
		return rf(ctx, number)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *big.Int) *types.Block); ok {
		r0 = rf(ctx, number)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.Block)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *big.Int) error); ok {
		r1 = rf(ctx, number)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_BlockByNumber_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'BlockByNumber'
type EthClienter_BlockByNumber_Call struct {
	*mock.Call
}

// BlockByNumber is a helper method to define mock.On call
//   - ctx context.Context
//   - number *big.Int
func (_e *EthClienter_Expecter) BlockByNumber(ctx interface{}, number interface{}) *EthClienter_BlockByNumber_Call {
	return &EthClienter_BlockByNumber_Call{Call: _e.mock.On("BlockByNumber", ctx, number)}
}

func (_c *EthClienter_BlockByNumber_Call) Run(run func(ctx context.Context, number *big.Int)) *EthClienter_BlockByNumber_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*big.Int))
	})
	return _c
}

func (_c *EthClienter_BlockByNumber_Call) Return(_a0 *types.Block, _a1 error) *EthClienter_BlockByNumber_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_BlockByNumber_Call) RunAndReturn(run func(context.Context, *big.Int) (*types.Block, error)) *EthClienter_BlockByNumber_Call {
	_c.Call.Return(run)
	return _c
}

// BlockNumber provides a mock function with given fields: ctx
func (_m *EthClienter) BlockNumber(ctx context.Context) (uint64, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for BlockNumber")
	}

	var r0 uint64
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (uint64, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) uint64); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Get(0).(uint64)
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_BlockNumber_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'BlockNumber'
type EthClienter_BlockNumber_Call struct {
	*mock.Call
}

// BlockNumber is a helper method to define mock.On call
//   - ctx context.Context
func (_e *EthClienter_Expecter) BlockNumber(ctx interface{}) *EthClienter_BlockNumber_Call {
	return &EthClienter_BlockNumber_Call{Call: _e.mock.On("BlockNumber", ctx)}
}

func (_c *EthClienter_BlockNumber_Call) Run(run func(ctx context.Context)) *EthClienter_BlockNumber_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *EthClienter_BlockNumber_Call) Return(_a0 uint64, _a1 error) *EthClienter_BlockNumber_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_BlockNumber_Call) RunAndReturn(run func(context.Context) (uint64, error)) *EthClienter_BlockNumber_Call {
	_c.Call.Return(run)
	return _c
}

// CallContract provides a mock function with given fields: ctx, call, blockNumber
func (_m *EthClienter) CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	ret := _m.Called(ctx, call, blockNumber)

	if len(ret) == 0 {
		panic("no return value specified for CallContract")
	}

	var r0 []byte
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error)); ok {
		return rf(ctx, call, blockNumber)
	}
	if rf, ok := ret.Get(0).(func(context.Context, ethereum.CallMsg, *big.Int) []byte); ok {
		r0 = rf(ctx, call, blockNumber)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]byte)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, ethereum.CallMsg, *big.Int) error); ok {
		r1 = rf(ctx, call, blockNumber)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_CallContract_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'CallContract'
type EthClienter_CallContract_Call struct {
	*mock.Call
}

// CallContract is a helper method to define mock.On call
//   - ctx context.Context
//   - call ethereum.CallMsg
//   - blockNumber *big.Int
func (_e *EthClienter_Expecter) CallContract(ctx interface{}, call interface{}, blockNumber interface{}) *EthClienter_CallContract_Call {
	return &EthClienter_CallContract_Call{Call: _e.mock.On("CallContract", ctx, call, blockNumber)}
}

func (_c *EthClienter_CallContract_Call) Run(run func(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int)) *EthClienter_CallContract_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(ethereum.CallMsg), args[2].(*big.Int))
	})
	return _c
}

func (_c *EthClienter_CallContract_Call) Return(_a0 []byte, _a1 error) *EthClienter_CallContract_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_CallContract_Call) RunAndReturn(run func(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error)) *EthClienter_CallContract_Call {
	_c.Call.Return(run)
	return _c
}

// Client provides a mock function with no fields
func (_m *EthClienter) Client() *rpc.Client {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Client")
	}

	var r0 *rpc.Client
	if rf, ok := ret.Get(0).(func() *rpc.Client); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*rpc.Client)
		}
	}

	return r0
}

// EthClienter_Client_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Client'
type EthClienter_Client_Call struct {
	*mock.Call
}

// Client is a helper method to define mock.On call
func (_e *EthClienter_Expecter) Client() *EthClienter_Client_Call {
	return &EthClienter_Client_Call{Call: _e.mock.On("Client")}
}

func (_c *EthClienter_Client_Call) Run(run func()) *EthClienter_Client_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *EthClienter_Client_Call) Return(_a0 *rpc.Client) *EthClienter_Client_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *EthClienter_Client_Call) RunAndReturn(run func() *rpc.Client) *EthClienter_Client_Call {
	_c.Call.Return(run)
	return _c
}

// CodeAt provides a mock function with given fields: ctx, contract, blockNumber
func (_m *EthClienter) CodeAt(ctx context.Context, contract common.Address, blockNumber *big.Int) ([]byte, error) {
	ret := _m.Called(ctx, contract, blockNumber)

	if len(ret) == 0 {
		panic("no return value specified for CodeAt")
	}

	var r0 []byte
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, common.Address, *big.Int) ([]byte, error)); ok {
		return rf(ctx, contract, blockNumber)
	}
	if rf, ok := ret.Get(0).(func(context.Context, common.Address, *big.Int) []byte); ok {
		r0 = rf(ctx, contract, blockNumber)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]byte)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, common.Address, *big.Int) error); ok {
		r1 = rf(ctx, contract, blockNumber)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_CodeAt_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'CodeAt'
type EthClienter_CodeAt_Call struct {
	*mock.Call
}

// CodeAt is a helper method to define mock.On call
//   - ctx context.Context
//   - contract common.Address
//   - blockNumber *big.Int
func (_e *EthClienter_Expecter) CodeAt(ctx interface{}, contract interface{}, blockNumber interface{}) *EthClienter_CodeAt_Call {
	return &EthClienter_CodeAt_Call{Call: _e.mock.On("CodeAt", ctx, contract, blockNumber)}
}

func (_c *EthClienter_CodeAt_Call) Run(run func(ctx context.Context, contract common.Address, blockNumber *big.Int)) *EthClienter_CodeAt_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Address), args[2].(*big.Int))
	})
	return _c
}

func (_c *EthClienter_CodeAt_Call) Return(_a0 []byte, _a1 error) *EthClienter_CodeAt_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_CodeAt_Call) RunAndReturn(run func(context.Context, common.Address, *big.Int) ([]byte, error)) *EthClienter_CodeAt_Call {
	_c.Call.Return(run)
	return _c
}

// EstimateGas provides a mock function with given fields: ctx, call
func (_m *EthClienter) EstimateGas(ctx context.Context, call ethereum.CallMsg) (uint64, error) {
	ret := _m.Called(ctx, call)

	if len(ret) == 0 {
		panic("no return value specified for EstimateGas")
	}

	var r0 uint64
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, ethereum.CallMsg) (uint64, error)); ok {
		return rf(ctx, call)
	}
	if rf, ok := ret.Get(0).(func(context.Context, ethereum.CallMsg) uint64); ok {
		r0 = rf(ctx, call)
	} else {
		r0 = ret.Get(0).(uint64)
	}

	if rf, ok := ret.Get(1).(func(context.Context, ethereum.CallMsg) error); ok {
		r1 = rf(ctx, call)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_EstimateGas_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'EstimateGas'
type EthClienter_EstimateGas_Call struct {
	*mock.Call
}

// EstimateGas is a helper method to define mock.On call
//   - ctx context.Context
//   - call ethereum.CallMsg
func (_e *EthClienter_Expecter) EstimateGas(ctx interface{}, call interface{}) *EthClienter_EstimateGas_Call {
	return &EthClienter_EstimateGas_Call{Call: _e.mock.On("EstimateGas", ctx, call)}
}

func (_c *EthClienter_EstimateGas_Call) Run(run func(ctx context.Context, call ethereum.CallMsg)) *EthClienter_EstimateGas_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(ethereum.CallMsg))
	})
	return _c
}

func (_c *EthClienter_EstimateGas_Call) Return(_a0 uint64, _a1 error) *EthClienter_EstimateGas_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_EstimateGas_Call) RunAndReturn(run func(context.Context, ethereum.CallMsg) (uint64, error)) *EthClienter_EstimateGas_Call {
	_c.Call.Return(run)
	return _c
}

// FilterLogs provides a mock function with given fields: ctx, q
func (_m *EthClienter) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	ret := _m.Called(ctx, q)

	if len(ret) == 0 {
		panic("no return value specified for FilterLogs")
	}

	var r0 []types.Log
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, ethereum.FilterQuery) ([]types.Log, error)); ok {
		return rf(ctx, q)
	}
	if rf, ok := ret.Get(0).(func(context.Context, ethereum.FilterQuery) []types.Log); ok {
		r0 = rf(ctx, q)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]types.Log)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, ethereum.FilterQuery) error); ok {
		r1 = rf(ctx, q)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_FilterLogs_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'FilterLogs'
type EthClienter_FilterLogs_Call struct {
	*mock.Call
}

// FilterLogs is a helper method to define mock.On call
//   - ctx context.Context
//   - q ethereum.FilterQuery
func (_e *EthClienter_Expecter) FilterLogs(ctx interface{}, q interface{}) *EthClienter_FilterLogs_Call {
	return &EthClienter_FilterLogs_Call{Call: _e.mock.On("FilterLogs", ctx, q)}
}

func (_c *EthClienter_FilterLogs_Call) Run(run func(ctx context.Context, q ethereum.FilterQuery)) *EthClienter_FilterLogs_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(ethereum.FilterQuery))
	})
	return _c
}

func (_c *EthClienter_FilterLogs_Call) Return(_a0 []types.Log, _a1 error) *EthClienter_FilterLogs_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_FilterLogs_Call) RunAndReturn(run func(context.Context, ethereum.FilterQuery) ([]types.Log, error)) *EthClienter_FilterLogs_Call {
	_c.Call.Return(run)
	return _c
}

// HeaderByHash provides a mock function with given fields: ctx, hash
func (_m *EthClienter) HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error) {
	ret := _m.Called(ctx, hash)

	if len(ret) == 0 {
		panic("no return value specified for HeaderByHash")
	}

	var r0 *types.Header
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash) (*types.Header, error)); ok {
		return rf(ctx, hash)
	}
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash) *types.Header); ok {
		r0 = rf(ctx, hash)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.Header)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, common.Hash) error); ok {
		r1 = rf(ctx, hash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_HeaderByHash_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'HeaderByHash'
type EthClienter_HeaderByHash_Call struct {
	*mock.Call
}

// HeaderByHash is a helper method to define mock.On call
//   - ctx context.Context
//   - hash common.Hash
func (_e *EthClienter_Expecter) HeaderByHash(ctx interface{}, hash interface{}) *EthClienter_HeaderByHash_Call {
	return &EthClienter_HeaderByHash_Call{Call: _e.mock.On("HeaderByHash", ctx, hash)}
}

func (_c *EthClienter_HeaderByHash_Call) Run(run func(ctx context.Context, hash common.Hash)) *EthClienter_HeaderByHash_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Hash))
	})
	return _c
}

func (_c *EthClienter_HeaderByHash_Call) Return(_a0 *types.Header, _a1 error) *EthClienter_HeaderByHash_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_HeaderByHash_Call) RunAndReturn(run func(context.Context, common.Hash) (*types.Header, error)) *EthClienter_HeaderByHash_Call {
	_c.Call.Return(run)
	return _c
}

// HeaderByNumber provides a mock function with given fields: ctx, number
func (_m *EthClienter) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	ret := _m.Called(ctx, number)

	if len(ret) == 0 {
		panic("no return value specified for HeaderByNumber")
	}

	var r0 *types.Header
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *big.Int) (*types.Header, error)); ok {
		return rf(ctx, number)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *big.Int) *types.Header); ok {
		r0 = rf(ctx, number)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.Header)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *big.Int) error); ok {
		r1 = rf(ctx, number)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_HeaderByNumber_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'HeaderByNumber'
type EthClienter_HeaderByNumber_Call struct {
	*mock.Call
}

// HeaderByNumber is a helper method to define mock.On call
//   - ctx context.Context
//   - number *big.Int
func (_e *EthClienter_Expecter) HeaderByNumber(ctx interface{}, number interface{}) *EthClienter_HeaderByNumber_Call {
	return &EthClienter_HeaderByNumber_Call{Call: _e.mock.On("HeaderByNumber", ctx, number)}
}

func (_c *EthClienter_HeaderByNumber_Call) Run(run func(ctx context.Context, number *big.Int)) *EthClienter_HeaderByNumber_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*big.Int))
	})
	return _c
}

func (_c *EthClienter_HeaderByNumber_Call) Return(_a0 *types.Header, _a1 error) *EthClienter_HeaderByNumber_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_HeaderByNumber_Call) RunAndReturn(run func(context.Context, *big.Int) (*types.Header, error)) *EthClienter_HeaderByNumber_Call {
	_c.Call.Return(run)
	return _c
}

// PendingCodeAt provides a mock function with given fields: ctx, account
func (_m *EthClienter) PendingCodeAt(ctx context.Context, account common.Address) ([]byte, error) {
	ret := _m.Called(ctx, account)

	if len(ret) == 0 {
		panic("no return value specified for PendingCodeAt")
	}

	var r0 []byte
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, common.Address) ([]byte, error)); ok {
		return rf(ctx, account)
	}
	if rf, ok := ret.Get(0).(func(context.Context, common.Address) []byte); ok {
		r0 = rf(ctx, account)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]byte)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, common.Address) error); ok {
		r1 = rf(ctx, account)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_PendingCodeAt_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'PendingCodeAt'
type EthClienter_PendingCodeAt_Call struct {
	*mock.Call
}

// PendingCodeAt is a helper method to define mock.On call
//   - ctx context.Context
//   - account common.Address
func (_e *EthClienter_Expecter) PendingCodeAt(ctx interface{}, account interface{}) *EthClienter_PendingCodeAt_Call {
	return &EthClienter_PendingCodeAt_Call{Call: _e.mock.On("PendingCodeAt", ctx, account)}
}

func (_c *EthClienter_PendingCodeAt_Call) Run(run func(ctx context.Context, account common.Address)) *EthClienter_PendingCodeAt_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Address))
	})
	return _c
}

func (_c *EthClienter_PendingCodeAt_Call) Return(_a0 []byte, _a1 error) *EthClienter_PendingCodeAt_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_PendingCodeAt_Call) RunAndReturn(run func(context.Context, common.Address) ([]byte, error)) *EthClienter_PendingCodeAt_Call {
	_c.Call.Return(run)
	return _c
}

// PendingNonceAt provides a mock function with given fields: ctx, account
func (_m *EthClienter) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	ret := _m.Called(ctx, account)

	if len(ret) == 0 {
		panic("no return value specified for PendingNonceAt")
	}

	var r0 uint64
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, common.Address) (uint64, error)); ok {
		return rf(ctx, account)
	}
	if rf, ok := ret.Get(0).(func(context.Context, common.Address) uint64); ok {
		r0 = rf(ctx, account)
	} else {
		r0 = ret.Get(0).(uint64)
	}

	if rf, ok := ret.Get(1).(func(context.Context, common.Address) error); ok {
		r1 = rf(ctx, account)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_PendingNonceAt_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'PendingNonceAt'
type EthClienter_PendingNonceAt_Call struct {
	*mock.Call
}

// PendingNonceAt is a helper method to define mock.On call
//   - ctx context.Context
//   - account common.Address
func (_e *EthClienter_Expecter) PendingNonceAt(ctx interface{}, account interface{}) *EthClienter_PendingNonceAt_Call {
	return &EthClienter_PendingNonceAt_Call{Call: _e.mock.On("PendingNonceAt", ctx, account)}
}

func (_c *EthClienter_PendingNonceAt_Call) Run(run func(ctx context.Context, account common.Address)) *EthClienter_PendingNonceAt_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Address))
	})
	return _c
}

func (_c *EthClienter_PendingNonceAt_Call) Return(_a0 uint64, _a1 error) *EthClienter_PendingNonceAt_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_PendingNonceAt_Call) RunAndReturn(run func(context.Context, common.Address) (uint64, error)) *EthClienter_PendingNonceAt_Call {
	_c.Call.Return(run)
	return _c
}

// SendTransaction provides a mock function with given fields: ctx, tx
func (_m *EthClienter) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	ret := _m.Called(ctx, tx)

	if len(ret) == 0 {
		panic("no return value specified for SendTransaction")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *types.Transaction) error); ok {
		r0 = rf(ctx, tx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// EthClienter_SendTransaction_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SendTransaction'
type EthClienter_SendTransaction_Call struct {
	*mock.Call
}

// SendTransaction is a helper method to define mock.On call
//   - ctx context.Context
//   - tx *types.Transaction
func (_e *EthClienter_Expecter) SendTransaction(ctx interface{}, tx interface{}) *EthClienter_SendTransaction_Call {
	return &EthClienter_SendTransaction_Call{Call: _e.mock.On("SendTransaction", ctx, tx)}
}

func (_c *EthClienter_SendTransaction_Call) Run(run func(ctx context.Context, tx *types.Transaction)) *EthClienter_SendTransaction_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*types.Transaction))
	})
	return _c
}

func (_c *EthClienter_SendTransaction_Call) Return(_a0 error) *EthClienter_SendTransaction_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *EthClienter_SendTransaction_Call) RunAndReturn(run func(context.Context, *types.Transaction) error) *EthClienter_SendTransaction_Call {
	_c.Call.Return(run)
	return _c
}

// SubscribeFilterLogs provides a mock function with given fields: ctx, q, ch
func (_m *EthClienter) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	ret := _m.Called(ctx, q, ch)

	if len(ret) == 0 {
		panic("no return value specified for SubscribeFilterLogs")
	}

	var r0 ethereum.Subscription
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error)); ok {
		return rf(ctx, q, ch)
	}
	if rf, ok := ret.Get(0).(func(context.Context, ethereum.FilterQuery, chan<- types.Log) ethereum.Subscription); ok {
		r0 = rf(ctx, q, ch)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(ethereum.Subscription)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, ethereum.FilterQuery, chan<- types.Log) error); ok {
		r1 = rf(ctx, q, ch)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_SubscribeFilterLogs_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SubscribeFilterLogs'
type EthClienter_SubscribeFilterLogs_Call struct {
	*mock.Call
}

// SubscribeFilterLogs is a helper method to define mock.On call
//   - ctx context.Context
//   - q ethereum.FilterQuery
//   - ch chan<- types.Log
func (_e *EthClienter_Expecter) SubscribeFilterLogs(ctx interface{}, q interface{}, ch interface{}) *EthClienter_SubscribeFilterLogs_Call {
	return &EthClienter_SubscribeFilterLogs_Call{Call: _e.mock.On("SubscribeFilterLogs", ctx, q, ch)}
}

func (_c *EthClienter_SubscribeFilterLogs_Call) Run(run func(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log)) *EthClienter_SubscribeFilterLogs_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(ethereum.FilterQuery), args[2].(chan<- types.Log))
	})
	return _c
}

func (_c *EthClienter_SubscribeFilterLogs_Call) Return(_a0 ethereum.Subscription, _a1 error) *EthClienter_SubscribeFilterLogs_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_SubscribeFilterLogs_Call) RunAndReturn(run func(context.Context, ethereum.FilterQuery, chan<- types.Log) (ethereum.Subscription, error)) *EthClienter_SubscribeFilterLogs_Call {
	_c.Call.Return(run)
	return _c
}

// SubscribeNewHead provides a mock function with given fields: ctx, ch
func (_m *EthClienter) SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
	ret := _m.Called(ctx, ch)

	if len(ret) == 0 {
		panic("no return value specified for SubscribeNewHead")
	}

	var r0 ethereum.Subscription
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, chan<- *types.Header) (ethereum.Subscription, error)); ok {
		return rf(ctx, ch)
	}
	if rf, ok := ret.Get(0).(func(context.Context, chan<- *types.Header) ethereum.Subscription); ok {
		r0 = rf(ctx, ch)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(ethereum.Subscription)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, chan<- *types.Header) error); ok {
		r1 = rf(ctx, ch)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_SubscribeNewHead_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SubscribeNewHead'
type EthClienter_SubscribeNewHead_Call struct {
	*mock.Call
}

// SubscribeNewHead is a helper method to define mock.On call
//   - ctx context.Context
//   - ch chan<- *types.Header
func (_e *EthClienter_Expecter) SubscribeNewHead(ctx interface{}, ch interface{}) *EthClienter_SubscribeNewHead_Call {
	return &EthClienter_SubscribeNewHead_Call{Call: _e.mock.On("SubscribeNewHead", ctx, ch)}
}

func (_c *EthClienter_SubscribeNewHead_Call) Run(run func(ctx context.Context, ch chan<- *types.Header)) *EthClienter_SubscribeNewHead_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(chan<- *types.Header))
	})
	return _c
}

func (_c *EthClienter_SubscribeNewHead_Call) Return(_a0 ethereum.Subscription, _a1 error) *EthClienter_SubscribeNewHead_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_SubscribeNewHead_Call) RunAndReturn(run func(context.Context, chan<- *types.Header) (ethereum.Subscription, error)) *EthClienter_SubscribeNewHead_Call {
	_c.Call.Return(run)
	return _c
}

// SuggestGasPrice provides a mock function with given fields: ctx
func (_m *EthClienter) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for SuggestGasPrice")
	}

	var r0 *big.Int
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (*big.Int, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) *big.Int); ok {
		r0 = rf(ctx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*big.Int)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_SuggestGasPrice_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SuggestGasPrice'
type EthClienter_SuggestGasPrice_Call struct {
	*mock.Call
}

// SuggestGasPrice is a helper method to define mock.On call
//   - ctx context.Context
func (_e *EthClienter_Expecter) SuggestGasPrice(ctx interface{}) *EthClienter_SuggestGasPrice_Call {
	return &EthClienter_SuggestGasPrice_Call{Call: _e.mock.On("SuggestGasPrice", ctx)}
}

func (_c *EthClienter_SuggestGasPrice_Call) Run(run func(ctx context.Context)) *EthClienter_SuggestGasPrice_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *EthClienter_SuggestGasPrice_Call) Return(_a0 *big.Int, _a1 error) *EthClienter_SuggestGasPrice_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_SuggestGasPrice_Call) RunAndReturn(run func(context.Context) (*big.Int, error)) *EthClienter_SuggestGasPrice_Call {
	_c.Call.Return(run)
	return _c
}

// SuggestGasTipCap provides a mock function with given fields: ctx
func (_m *EthClienter) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for SuggestGasTipCap")
	}

	var r0 *big.Int
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (*big.Int, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) *big.Int); ok {
		r0 = rf(ctx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*big.Int)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_SuggestGasTipCap_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SuggestGasTipCap'
type EthClienter_SuggestGasTipCap_Call struct {
	*mock.Call
}

// SuggestGasTipCap is a helper method to define mock.On call
//   - ctx context.Context
func (_e *EthClienter_Expecter) SuggestGasTipCap(ctx interface{}) *EthClienter_SuggestGasTipCap_Call {
	return &EthClienter_SuggestGasTipCap_Call{Call: _e.mock.On("SuggestGasTipCap", ctx)}
}

func (_c *EthClienter_SuggestGasTipCap_Call) Run(run func(ctx context.Context)) *EthClienter_SuggestGasTipCap_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *EthClienter_SuggestGasTipCap_Call) Return(_a0 *big.Int, _a1 error) *EthClienter_SuggestGasTipCap_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_SuggestGasTipCap_Call) RunAndReturn(run func(context.Context) (*big.Int, error)) *EthClienter_SuggestGasTipCap_Call {
	_c.Call.Return(run)
	return _c
}

// TransactionCount provides a mock function with given fields: ctx, blockHash
func (_m *EthClienter) TransactionCount(ctx context.Context, blockHash common.Hash) (uint, error) {
	ret := _m.Called(ctx, blockHash)

	if len(ret) == 0 {
		panic("no return value specified for TransactionCount")
	}

	var r0 uint
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash) (uint, error)); ok {
		return rf(ctx, blockHash)
	}
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash) uint); ok {
		r0 = rf(ctx, blockHash)
	} else {
		r0 = ret.Get(0).(uint)
	}

	if rf, ok := ret.Get(1).(func(context.Context, common.Hash) error); ok {
		r1 = rf(ctx, blockHash)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_TransactionCount_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'TransactionCount'
type EthClienter_TransactionCount_Call struct {
	*mock.Call
}

// TransactionCount is a helper method to define mock.On call
//   - ctx context.Context
//   - blockHash common.Hash
func (_e *EthClienter_Expecter) TransactionCount(ctx interface{}, blockHash interface{}) *EthClienter_TransactionCount_Call {
	return &EthClienter_TransactionCount_Call{Call: _e.mock.On("TransactionCount", ctx, blockHash)}
}

func (_c *EthClienter_TransactionCount_Call) Run(run func(ctx context.Context, blockHash common.Hash)) *EthClienter_TransactionCount_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Hash))
	})
	return _c
}

func (_c *EthClienter_TransactionCount_Call) Return(_a0 uint, _a1 error) *EthClienter_TransactionCount_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_TransactionCount_Call) RunAndReturn(run func(context.Context, common.Hash) (uint, error)) *EthClienter_TransactionCount_Call {
	_c.Call.Return(run)
	return _c
}

// TransactionInBlock provides a mock function with given fields: ctx, blockHash, index
func (_m *EthClienter) TransactionInBlock(ctx context.Context, blockHash common.Hash, index uint) (*types.Transaction, error) {
	ret := _m.Called(ctx, blockHash, index)

	if len(ret) == 0 {
		panic("no return value specified for TransactionInBlock")
	}

	var r0 *types.Transaction
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash, uint) (*types.Transaction, error)); ok {
		return rf(ctx, blockHash, index)
	}
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash, uint) *types.Transaction); ok {
		r0 = rf(ctx, blockHash, index)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.Transaction)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, common.Hash, uint) error); ok {
		r1 = rf(ctx, blockHash, index)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthClienter_TransactionInBlock_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'TransactionInBlock'
type EthClienter_TransactionInBlock_Call struct {
	*mock.Call
}

// TransactionInBlock is a helper method to define mock.On call
//   - ctx context.Context
//   - blockHash common.Hash
//   - index uint
func (_e *EthClienter_Expecter) TransactionInBlock(ctx interface{}, blockHash interface{}, index interface{}) *EthClienter_TransactionInBlock_Call {
	return &EthClienter_TransactionInBlock_Call{Call: _e.mock.On("TransactionInBlock", ctx, blockHash, index)}
}

func (_c *EthClienter_TransactionInBlock_Call) Run(run func(ctx context.Context, blockHash common.Hash, index uint)) *EthClienter_TransactionInBlock_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Hash), args[2].(uint))
	})
	return _c
}

func (_c *EthClienter_TransactionInBlock_Call) Return(_a0 *types.Transaction, _a1 error) *EthClienter_TransactionInBlock_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClienter_TransactionInBlock_Call) RunAndReturn(run func(context.Context, common.Hash, uint) (*types.Transaction, error)) *EthClienter_TransactionInBlock_Call {
	_c.Call.Return(run)
	return _c
}

// NewEthClienter creates a new instance of EthClienter. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewEthClienter(t interface {
	mock.TestingT
	Cleanup(func())
}) *EthClienter {
	mock := &EthClienter{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
