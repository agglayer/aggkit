// Code generated by mockery v2.51.0. DO NOT EDIT.

package reorgdetector

import (
	context "context"
	big "math/big"

	common "github.com/ethereum/go-ethereum/common"

	ethereum "github.com/ethereum/go-ethereum"

	mock "github.com/stretchr/testify/mock"

	types "github.com/ethereum/go-ethereum/core/types"
)

// EthClientMock is an autogenerated mock type for the EthClient type
type EthClientMock struct {
	mock.Mock
}

type EthClientMock_Expecter struct {
	mock *mock.Mock
}

func (_m *EthClientMock) EXPECT() *EthClientMock_Expecter {
	return &EthClientMock_Expecter{mock: &_m.Mock}
}

// HeaderByHash provides a mock function with given fields: ctx, hash
func (_m *EthClientMock) HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error) {
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

// EthClientMock_HeaderByHash_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'HeaderByHash'
type EthClientMock_HeaderByHash_Call struct {
	*mock.Call
}

// HeaderByHash is a helper method to define mock.On call
//   - ctx context.Context
//   - hash common.Hash
func (_e *EthClientMock_Expecter) HeaderByHash(ctx interface{}, hash interface{}) *EthClientMock_HeaderByHash_Call {
	return &EthClientMock_HeaderByHash_Call{Call: _e.mock.On("HeaderByHash", ctx, hash)}
}

func (_c *EthClientMock_HeaderByHash_Call) Run(run func(ctx context.Context, hash common.Hash)) *EthClientMock_HeaderByHash_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Hash))
	})
	return _c
}

func (_c *EthClientMock_HeaderByHash_Call) Return(_a0 *types.Header, _a1 error) *EthClientMock_HeaderByHash_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClientMock_HeaderByHash_Call) RunAndReturn(run func(context.Context, common.Hash) (*types.Header, error)) *EthClientMock_HeaderByHash_Call {
	_c.Call.Return(run)
	return _c
}

// HeaderByNumber provides a mock function with given fields: ctx, number
func (_m *EthClientMock) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
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

// EthClientMock_HeaderByNumber_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'HeaderByNumber'
type EthClientMock_HeaderByNumber_Call struct {
	*mock.Call
}

// HeaderByNumber is a helper method to define mock.On call
//   - ctx context.Context
//   - number *big.Int
func (_e *EthClientMock_Expecter) HeaderByNumber(ctx interface{}, number interface{}) *EthClientMock_HeaderByNumber_Call {
	return &EthClientMock_HeaderByNumber_Call{Call: _e.mock.On("HeaderByNumber", ctx, number)}
}

func (_c *EthClientMock_HeaderByNumber_Call) Run(run func(ctx context.Context, number *big.Int)) *EthClientMock_HeaderByNumber_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*big.Int))
	})
	return _c
}

func (_c *EthClientMock_HeaderByNumber_Call) Return(_a0 *types.Header, _a1 error) *EthClientMock_HeaderByNumber_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClientMock_HeaderByNumber_Call) RunAndReturn(run func(context.Context, *big.Int) (*types.Header, error)) *EthClientMock_HeaderByNumber_Call {
	_c.Call.Return(run)
	return _c
}

// SubscribeNewHead provides a mock function with given fields: ctx, ch
func (_m *EthClientMock) SubscribeNewHead(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
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

// EthClientMock_SubscribeNewHead_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SubscribeNewHead'
type EthClientMock_SubscribeNewHead_Call struct {
	*mock.Call
}

// SubscribeNewHead is a helper method to define mock.On call
//   - ctx context.Context
//   - ch chan<- *types.Header
func (_e *EthClientMock_Expecter) SubscribeNewHead(ctx interface{}, ch interface{}) *EthClientMock_SubscribeNewHead_Call {
	return &EthClientMock_SubscribeNewHead_Call{Call: _e.mock.On("SubscribeNewHead", ctx, ch)}
}

func (_c *EthClientMock_SubscribeNewHead_Call) Run(run func(ctx context.Context, ch chan<- *types.Header)) *EthClientMock_SubscribeNewHead_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(chan<- *types.Header))
	})
	return _c
}

func (_c *EthClientMock_SubscribeNewHead_Call) Return(_a0 ethereum.Subscription, _a1 error) *EthClientMock_SubscribeNewHead_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthClientMock_SubscribeNewHead_Call) RunAndReturn(run func(context.Context, chan<- *types.Header) (ethereum.Subscription, error)) *EthClientMock_SubscribeNewHead_Call {
	_c.Call.Return(run)
	return _c
}

// NewEthClientMock creates a new instance of EthClientMock. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewEthClientMock(t interface {
	mock.TestingT
	Cleanup(func())
}) *EthClientMock {
	mock := &EthClientMock{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
