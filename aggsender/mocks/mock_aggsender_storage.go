// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	agglayer "github.com/0xPolygon/cdk/agglayer"
	common "github.com/ethereum/go-ethereum/common"

	context "context"

	mock "github.com/stretchr/testify/mock"

	types "github.com/0xPolygon/cdk/aggsender/types"
)

// AggSenderStorageMock is an autogenerated mock type for the AggSenderStorage type
type AggSenderStorageMock struct {
	mock.Mock
}

type AggSenderStorageMock_Expecter struct {
	mock *mock.Mock
}

func (_m *AggSenderStorageMock) EXPECT() *AggSenderStorageMock_Expecter {
	return &AggSenderStorageMock_Expecter{mock: &_m.Mock}
}

// DeleteCertificate provides a mock function with given fields: ctx, certificateID
func (_m *AggSenderStorageMock) DeleteCertificate(ctx context.Context, certificateID common.Hash) error {
	ret := _m.Called(ctx, certificateID)

	if len(ret) == 0 {
		panic("no return value specified for DeleteCertificate")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, common.Hash) error); ok {
		r0 = rf(ctx, certificateID)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// AggSenderStorageMock_DeleteCertificate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'DeleteCertificate'
type AggSenderStorageMock_DeleteCertificate_Call struct {
	*mock.Call
}

// DeleteCertificate is a helper method to define mock.On call
//   - ctx context.Context
//   - certificateID common.Hash
func (_e *AggSenderStorageMock_Expecter) DeleteCertificate(ctx interface{}, certificateID interface{}) *AggSenderStorageMock_DeleteCertificate_Call {
	return &AggSenderStorageMock_DeleteCertificate_Call{Call: _e.mock.On("DeleteCertificate", ctx, certificateID)}
}

func (_c *AggSenderStorageMock_DeleteCertificate_Call) Run(run func(ctx context.Context, certificateID common.Hash)) *AggSenderStorageMock_DeleteCertificate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Hash))
	})
	return _c
}

func (_c *AggSenderStorageMock_DeleteCertificate_Call) Return(_a0 error) *AggSenderStorageMock_DeleteCertificate_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *AggSenderStorageMock_DeleteCertificate_Call) RunAndReturn(run func(context.Context, common.Hash) error) *AggSenderStorageMock_DeleteCertificate_Call {
	_c.Call.Return(run)
	return _c
}

// GetCertificateByHeight provides a mock function with given fields: ctx, height
func (_m *AggSenderStorageMock) GetCertificateByHeight(ctx context.Context, height uint64) (types.CertificateInfo, error) {
	ret := _m.Called(ctx, height)

	if len(ret) == 0 {
		panic("no return value specified for GetCertificateByHeight")
	}

	var r0 types.CertificateInfo
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, uint64) (types.CertificateInfo, error)); ok {
		return rf(ctx, height)
	}
	if rf, ok := ret.Get(0).(func(context.Context, uint64) types.CertificateInfo); ok {
		r0 = rf(ctx, height)
	} else {
		r0 = ret.Get(0).(types.CertificateInfo)
	}

	if rf, ok := ret.Get(1).(func(context.Context, uint64) error); ok {
		r1 = rf(ctx, height)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggSenderStorageMock_GetCertificateByHeight_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetCertificateByHeight'
type AggSenderStorageMock_GetCertificateByHeight_Call struct {
	*mock.Call
}

// GetCertificateByHeight is a helper method to define mock.On call
//   - ctx context.Context
//   - height uint64
func (_e *AggSenderStorageMock_Expecter) GetCertificateByHeight(ctx interface{}, height interface{}) *AggSenderStorageMock_GetCertificateByHeight_Call {
	return &AggSenderStorageMock_GetCertificateByHeight_Call{Call: _e.mock.On("GetCertificateByHeight", ctx, height)}
}

func (_c *AggSenderStorageMock_GetCertificateByHeight_Call) Run(run func(ctx context.Context, height uint64)) *AggSenderStorageMock_GetCertificateByHeight_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint64))
	})
	return _c
}

func (_c *AggSenderStorageMock_GetCertificateByHeight_Call) Return(_a0 types.CertificateInfo, _a1 error) *AggSenderStorageMock_GetCertificateByHeight_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggSenderStorageMock_GetCertificateByHeight_Call) RunAndReturn(run func(context.Context, uint64) (types.CertificateInfo, error)) *AggSenderStorageMock_GetCertificateByHeight_Call {
	_c.Call.Return(run)
	return _c
}

// GetCertificatesByStatus provides a mock function with given fields: ctx, status
func (_m *AggSenderStorageMock) GetCertificatesByStatus(ctx context.Context, status []agglayer.CertificateStatus) ([]*types.CertificateInfo, error) {
	ret := _m.Called(ctx, status)

	if len(ret) == 0 {
		panic("no return value specified for GetCertificatesByStatus")
	}

	var r0 []*types.CertificateInfo
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, []agglayer.CertificateStatus) ([]*types.CertificateInfo, error)); ok {
		return rf(ctx, status)
	}
	if rf, ok := ret.Get(0).(func(context.Context, []agglayer.CertificateStatus) []*types.CertificateInfo); ok {
		r0 = rf(ctx, status)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*types.CertificateInfo)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, []agglayer.CertificateStatus) error); ok {
		r1 = rf(ctx, status)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggSenderStorageMock_GetCertificatesByStatus_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetCertificatesByStatus'
type AggSenderStorageMock_GetCertificatesByStatus_Call struct {
	*mock.Call
}

// GetCertificatesByStatus is a helper method to define mock.On call
//   - ctx context.Context
//   - status []agglayer.CertificateStatus
func (_e *AggSenderStorageMock_Expecter) GetCertificatesByStatus(ctx interface{}, status interface{}) *AggSenderStorageMock_GetCertificatesByStatus_Call {
	return &AggSenderStorageMock_GetCertificatesByStatus_Call{Call: _e.mock.On("GetCertificatesByStatus", ctx, status)}
}

func (_c *AggSenderStorageMock_GetCertificatesByStatus_Call) Run(run func(ctx context.Context, status []agglayer.CertificateStatus)) *AggSenderStorageMock_GetCertificatesByStatus_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].([]agglayer.CertificateStatus))
	})
	return _c
}

func (_c *AggSenderStorageMock_GetCertificatesByStatus_Call) Return(_a0 []*types.CertificateInfo, _a1 error) *AggSenderStorageMock_GetCertificatesByStatus_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggSenderStorageMock_GetCertificatesByStatus_Call) RunAndReturn(run func(context.Context, []agglayer.CertificateStatus) ([]*types.CertificateInfo, error)) *AggSenderStorageMock_GetCertificatesByStatus_Call {
	_c.Call.Return(run)
	return _c
}

// GetLastSentCertificate provides a mock function with given fields: ctx
func (_m *AggSenderStorageMock) GetLastSentCertificate(ctx context.Context) (types.CertificateInfo, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for GetLastSentCertificate")
	}

	var r0 types.CertificateInfo
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (types.CertificateInfo, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) types.CertificateInfo); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Get(0).(types.CertificateInfo)
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggSenderStorageMock_GetLastSentCertificate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetLastSentCertificate'
type AggSenderStorageMock_GetLastSentCertificate_Call struct {
	*mock.Call
}

// GetLastSentCertificate is a helper method to define mock.On call
//   - ctx context.Context
func (_e *AggSenderStorageMock_Expecter) GetLastSentCertificate(ctx interface{}) *AggSenderStorageMock_GetLastSentCertificate_Call {
	return &AggSenderStorageMock_GetLastSentCertificate_Call{Call: _e.mock.On("GetLastSentCertificate", ctx)}
}

func (_c *AggSenderStorageMock_GetLastSentCertificate_Call) Run(run func(ctx context.Context)) *AggSenderStorageMock_GetLastSentCertificate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *AggSenderStorageMock_GetLastSentCertificate_Call) Return(_a0 types.CertificateInfo, _a1 error) *AggSenderStorageMock_GetLastSentCertificate_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggSenderStorageMock_GetLastSentCertificate_Call) RunAndReturn(run func(context.Context) (types.CertificateInfo, error)) *AggSenderStorageMock_GetLastSentCertificate_Call {
	_c.Call.Return(run)
	return _c
}

// SaveLastSentCertificate provides a mock function with given fields: ctx, certificate
func (_m *AggSenderStorageMock) SaveLastSentCertificate(ctx context.Context, certificate types.CertificateInfo) error {
	ret := _m.Called(ctx, certificate)

	if len(ret) == 0 {
		panic("no return value specified for SaveLastSentCertificate")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, types.CertificateInfo) error); ok {
		r0 = rf(ctx, certificate)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// AggSenderStorageMock_SaveLastSentCertificate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SaveLastSentCertificate'
type AggSenderStorageMock_SaveLastSentCertificate_Call struct {
	*mock.Call
}

// SaveLastSentCertificate is a helper method to define mock.On call
//   - ctx context.Context
//   - certificate types.CertificateInfo
func (_e *AggSenderStorageMock_Expecter) SaveLastSentCertificate(ctx interface{}, certificate interface{}) *AggSenderStorageMock_SaveLastSentCertificate_Call {
	return &AggSenderStorageMock_SaveLastSentCertificate_Call{Call: _e.mock.On("SaveLastSentCertificate", ctx, certificate)}
}

func (_c *AggSenderStorageMock_SaveLastSentCertificate_Call) Run(run func(ctx context.Context, certificate types.CertificateInfo)) *AggSenderStorageMock_SaveLastSentCertificate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(types.CertificateInfo))
	})
	return _c
}

func (_c *AggSenderStorageMock_SaveLastSentCertificate_Call) Return(_a0 error) *AggSenderStorageMock_SaveLastSentCertificate_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *AggSenderStorageMock_SaveLastSentCertificate_Call) RunAndReturn(run func(context.Context, types.CertificateInfo) error) *AggSenderStorageMock_SaveLastSentCertificate_Call {
	_c.Call.Return(run)
	return _c
}

// UpdateCertificateStatus provides a mock function with given fields: ctx, certificate
func (_m *AggSenderStorageMock) UpdateCertificateStatus(ctx context.Context, certificate types.CertificateInfo) error {
	ret := _m.Called(ctx, certificate)

	if len(ret) == 0 {
		panic("no return value specified for UpdateCertificateStatus")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, types.CertificateInfo) error); ok {
		r0 = rf(ctx, certificate)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// AggSenderStorageMock_UpdateCertificateStatus_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'UpdateCertificateStatus'
type AggSenderStorageMock_UpdateCertificateStatus_Call struct {
	*mock.Call
}

// UpdateCertificateStatus is a helper method to define mock.On call
//   - ctx context.Context
//   - certificate types.CertificateInfo
func (_e *AggSenderStorageMock_Expecter) UpdateCertificateStatus(ctx interface{}, certificate interface{}) *AggSenderStorageMock_UpdateCertificateStatus_Call {
	return &AggSenderStorageMock_UpdateCertificateStatus_Call{Call: _e.mock.On("UpdateCertificateStatus", ctx, certificate)}
}

func (_c *AggSenderStorageMock_UpdateCertificateStatus_Call) Run(run func(ctx context.Context, certificate types.CertificateInfo)) *AggSenderStorageMock_UpdateCertificateStatus_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(types.CertificateInfo))
	})
	return _c
}

func (_c *AggSenderStorageMock_UpdateCertificateStatus_Call) Return(_a0 error) *AggSenderStorageMock_UpdateCertificateStatus_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *AggSenderStorageMock_UpdateCertificateStatus_Call) RunAndReturn(run func(context.Context, types.CertificateInfo) error) *AggSenderStorageMock_UpdateCertificateStatus_Call {
	_c.Call.Return(run)
	return _c
}

// NewAggSenderStorageMock creates a new instance of AggSenderStorageMock. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewAggSenderStorageMock(t interface {
	mock.TestingT
	Cleanup(func())
}) *AggSenderStorageMock {
	mock := &AggSenderStorageMock{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
