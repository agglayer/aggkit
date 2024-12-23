// Code generated by mockery. DO NOT EDIT.

package helpers

import (
	big "math/big"

	common "github.com/ethereum/go-ethereum/common"

	context "context"

	mock "github.com/stretchr/testify/mock"

	types "github.com/ethereum/go-ethereum/core/types"

	zkevm_ethtx_managertypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
)

// EthTxManagerMock is an autogenerated mock type for the EthTxManager type
type EthTxManagerMock struct {
	mock.Mock
}

type EthTxManagerMock_Expecter struct {
	mock *mock.Mock
}

func (_m *EthTxManagerMock) EXPECT() *EthTxManagerMock_Expecter {
	return &EthTxManagerMock_Expecter{mock: &_m.Mock}
}

// Add provides a mock function with given fields: ctx, to, value, data, gasOffset, sidecar
func (_m *EthTxManagerMock) Add(ctx context.Context, to *common.Address, value *big.Int, data []byte, gasOffset uint64, sidecar *types.BlobTxSidecar) (common.Hash, error) {
	ret := _m.Called(ctx, to, value, data, gasOffset, sidecar)

	if len(ret) == 0 {
		panic("no return value specified for Add")
	}

	var r0 common.Hash
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *common.Address, *big.Int, []byte, uint64, *types.BlobTxSidecar) (common.Hash, error)); ok {
		return rf(ctx, to, value, data, gasOffset, sidecar)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *common.Address, *big.Int, []byte, uint64, *types.BlobTxSidecar) common.Hash); ok {
		r0 = rf(ctx, to, value, data, gasOffset, sidecar)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(common.Hash)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *common.Address, *big.Int, []byte, uint64, *types.BlobTxSidecar) error); ok {
		r1 = rf(ctx, to, value, data, gasOffset, sidecar)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthTxManagerMock_Add_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Add'
type EthTxManagerMock_Add_Call struct {
	*mock.Call
}

// Add is a helper method to define mock.On call
//   - ctx context.Context
//   - to *common.Address
//   - value *big.Int
//   - data []byte
//   - gasOffset uint64
//   - sidecar *types.BlobTxSidecar
func (_e *EthTxManagerMock_Expecter) Add(ctx interface{}, to interface{}, value interface{}, data interface{}, gasOffset interface{}, sidecar interface{}) *EthTxManagerMock_Add_Call {
	return &EthTxManagerMock_Add_Call{Call: _e.mock.On("Add", ctx, to, value, data, gasOffset, sidecar)}
}

func (_c *EthTxManagerMock_Add_Call) Run(run func(ctx context.Context, to *common.Address, value *big.Int, data []byte, gasOffset uint64, sidecar *types.BlobTxSidecar)) *EthTxManagerMock_Add_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*common.Address), args[2].(*big.Int), args[3].([]byte), args[4].(uint64), args[5].(*types.BlobTxSidecar))
	})
	return _c
}

func (_c *EthTxManagerMock_Add_Call) Return(_a0 common.Hash, _a1 error) *EthTxManagerMock_Add_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthTxManagerMock_Add_Call) RunAndReturn(run func(context.Context, *common.Address, *big.Int, []byte, uint64, *types.BlobTxSidecar) (common.Hash, error)) *EthTxManagerMock_Add_Call {
	_c.Call.Return(run)
	return _c
}

// Remove provides a mock function with given fields: ctx, id
func (_m *EthTxManagerMock) Remove(ctx context.Context, id common.Hash) error {
	ret := _m.Called(ctx, id)

	if len(ret) == 0 {
		panic("no return value specified for Remove")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash) error); ok {
		r0 = rf(ctx, id)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// EthTxManagerMock_Remove_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Remove'
type EthTxManagerMock_Remove_Call struct {
	*mock.Call
}

// Remove is a helper method to define mock.On call
//   - ctx context.Context
//   - id common.Hash
func (_e *EthTxManagerMock_Expecter) Remove(ctx interface{}, id interface{}) *EthTxManagerMock_Remove_Call {
	return &EthTxManagerMock_Remove_Call{Call: _e.mock.On("Remove", ctx, id)}
}

func (_c *EthTxManagerMock_Remove_Call) Run(run func(ctx context.Context, id common.Hash)) *EthTxManagerMock_Remove_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Hash))
	})
	return _c
}

func (_c *EthTxManagerMock_Remove_Call) Return(_a0 error) *EthTxManagerMock_Remove_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *EthTxManagerMock_Remove_Call) RunAndReturn(run func(context.Context, common.Hash) error) *EthTxManagerMock_Remove_Call {
	_c.Call.Return(run)
	return _c
}

// Result provides a mock function with given fields: ctx, id
func (_m *EthTxManagerMock) Result(ctx context.Context, id common.Hash) (zkevm_ethtx_managertypes.MonitoredTxResult, error) {
	ret := _m.Called(ctx, id)

	if len(ret) == 0 {
		panic("no return value specified for Result")
	}

	var r0 zkevm_ethtx_managertypes.MonitoredTxResult
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash) (zkevm_ethtx_managertypes.MonitoredTxResult, error)); ok {
		return rf(ctx, id)
	}
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash) zkevm_ethtx_managertypes.MonitoredTxResult); ok {
		r0 = rf(ctx, id)
	} else {
		r0 = ret.Get(0).(zkevm_ethtx_managertypes.MonitoredTxResult)
	}

	if rf, ok := ret.Get(1).(func(context.Context, common.Hash) error); ok {
		r1 = rf(ctx, id)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthTxManagerMock_Result_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Result'
type EthTxManagerMock_Result_Call struct {
	*mock.Call
}

// Result is a helper method to define mock.On call
//   - ctx context.Context
//   - id common.Hash
func (_e *EthTxManagerMock_Expecter) Result(ctx interface{}, id interface{}) *EthTxManagerMock_Result_Call {
	return &EthTxManagerMock_Result_Call{Call: _e.mock.On("Result", ctx, id)}
}

func (_c *EthTxManagerMock_Result_Call) Run(run func(ctx context.Context, id common.Hash)) *EthTxManagerMock_Result_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Hash))
	})
	return _c
}

func (_c *EthTxManagerMock_Result_Call) Return(_a0 zkevm_ethtx_managertypes.MonitoredTxResult, _a1 error) *EthTxManagerMock_Result_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthTxManagerMock_Result_Call) RunAndReturn(run func(context.Context, common.Hash) (zkevm_ethtx_managertypes.MonitoredTxResult, error)) *EthTxManagerMock_Result_Call {
	_c.Call.Return(run)
	return _c
}

// ResultsByStatus provides a mock function with given fields: ctx, statuses
func (_m *EthTxManagerMock) ResultsByStatus(ctx context.Context, statuses []zkevm_ethtx_managertypes.MonitoredTxStatus) ([]zkevm_ethtx_managertypes.MonitoredTxResult, error) {
	ret := _m.Called(ctx, statuses)

	if len(ret) == 0 {
		panic("no return value specified for ResultsByStatus")
	}

	var r0 []zkevm_ethtx_managertypes.MonitoredTxResult
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, []zkevm_ethtx_managertypes.MonitoredTxStatus) ([]zkevm_ethtx_managertypes.MonitoredTxResult, error)); ok {
		return rf(ctx, statuses)
	}
	if rf, ok := ret.Get(0).(func(context.Context, []zkevm_ethtx_managertypes.MonitoredTxStatus) []zkevm_ethtx_managertypes.MonitoredTxResult); ok {
		r0 = rf(ctx, statuses)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]zkevm_ethtx_managertypes.MonitoredTxResult)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, []zkevm_ethtx_managertypes.MonitoredTxStatus) error); ok {
		r1 = rf(ctx, statuses)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// EthTxManagerMock_ResultsByStatus_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ResultsByStatus'
type EthTxManagerMock_ResultsByStatus_Call struct {
	*mock.Call
}

// ResultsByStatus is a helper method to define mock.On call
//   - ctx context.Context
//   - statuses []zkevm_ethtx_managertypes.MonitoredTxStatus
func (_e *EthTxManagerMock_Expecter) ResultsByStatus(ctx interface{}, statuses interface{}) *EthTxManagerMock_ResultsByStatus_Call {
	return &EthTxManagerMock_ResultsByStatus_Call{Call: _e.mock.On("ResultsByStatus", ctx, statuses)}
}

func (_c *EthTxManagerMock_ResultsByStatus_Call) Run(run func(ctx context.Context, statuses []zkevm_ethtx_managertypes.MonitoredTxStatus)) *EthTxManagerMock_ResultsByStatus_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].([]zkevm_ethtx_managertypes.MonitoredTxStatus))
	})
	return _c
}

func (_c *EthTxManagerMock_ResultsByStatus_Call) Return(_a0 []zkevm_ethtx_managertypes.MonitoredTxResult, _a1 error) *EthTxManagerMock_ResultsByStatus_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *EthTxManagerMock_ResultsByStatus_Call) RunAndReturn(run func(context.Context, []zkevm_ethtx_managertypes.MonitoredTxStatus) ([]zkevm_ethtx_managertypes.MonitoredTxResult, error)) *EthTxManagerMock_ResultsByStatus_Call {
	_c.Call.Return(run)
	return _c
}

// NewEthTxManagerMock creates a new instance of EthTxManagerMock. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewEthTxManagerMock(t interface {
	mock.TestingT
	Cleanup(func())
}) *EthTxManagerMock {
	mock := &EthTxManagerMock{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
