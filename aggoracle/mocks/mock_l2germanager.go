// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	big "math/big"

	bind "github.com/ethereum/go-ethereum/accounts/abi/bind"

	mock "github.com/stretchr/testify/mock"
)

// L2GERManagerMock is an autogenerated mock type for the L2GERManagerContract type
type L2GERManagerMock struct {
	mock.Mock
}

type L2GERManagerMock_Expecter struct {
	mock *mock.Mock
}

func (_m *L2GERManagerMock) EXPECT() *L2GERManagerMock_Expecter {
	return &L2GERManagerMock_Expecter{mock: &_m.Mock}
}

// GlobalExitRootMap provides a mock function with given fields: opts, ger
func (_m *L2GERManagerMock) GlobalExitRootMap(opts *bind.CallOpts, ger [32]byte) (*big.Int, error) {
	ret := _m.Called(opts, ger)

	if len(ret) == 0 {
		panic("no return value specified for GlobalExitRootMap")
	}

	var r0 *big.Int
	var r1 error
	if rf, ok := ret.Get(0).(func(*bind.CallOpts, [32]byte) (*big.Int, error)); ok {
		return rf(opts, ger)
	}
	if rf, ok := ret.Get(0).(func(*bind.CallOpts, [32]byte) *big.Int); ok {
		r0 = rf(opts, ger)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*big.Int)
		}
	}

	if rf, ok := ret.Get(1).(func(*bind.CallOpts, [32]byte) error); ok {
		r1 = rf(opts, ger)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// L2GERManagerMock_GlobalExitRootMap_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GlobalExitRootMap'
type L2GERManagerMock_GlobalExitRootMap_Call struct {
	*mock.Call
}

// GlobalExitRootMap is a helper method to define mock.On call
//   - opts *bind.CallOpts
//   - ger [32]byte
func (_e *L2GERManagerMock_Expecter) GlobalExitRootMap(opts interface{}, ger interface{}) *L2GERManagerMock_GlobalExitRootMap_Call {
	return &L2GERManagerMock_GlobalExitRootMap_Call{Call: _e.mock.On("GlobalExitRootMap", opts, ger)}
}

func (_c *L2GERManagerMock_GlobalExitRootMap_Call) Run(run func(opts *bind.CallOpts, ger [32]byte)) *L2GERManagerMock_GlobalExitRootMap_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(*bind.CallOpts), args[1].([32]byte))
	})
	return _c
}

func (_c *L2GERManagerMock_GlobalExitRootMap_Call) Return(_a0 *big.Int, _a1 error) *L2GERManagerMock_GlobalExitRootMap_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *L2GERManagerMock_GlobalExitRootMap_Call) RunAndReturn(run func(*bind.CallOpts, [32]byte) (*big.Int, error)) *L2GERManagerMock_GlobalExitRootMap_Call {
	_c.Call.Return(run)
	return _c
}

// NewL2GERManagerMock creates a new instance of L2GERManagerMock. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewL2GERManagerMock(t interface {
	mock.TestingT
	Cleanup(func())
}) *L2GERManagerMock {
	mock := &L2GERManagerMock{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
