// Code generated by mockery v2.51.0. DO NOT EDIT.

package mocks

import (
	agglayer "github.com/agglayer/aggkit/agglayer"
	common "github.com/ethereum/go-ethereum/common"

	context "context"

	mock "github.com/stretchr/testify/mock"

	types "github.com/agglayer/aggkit/aggsender/types"
)

// AggSenderStorage is an autogenerated mock type for the AggSenderStorage type
type AggSenderStorage struct {
	mock.Mock
}

type AggSenderStorage_Expecter struct {
	mock *mock.Mock
}

func (_m *AggSenderStorage) EXPECT() *AggSenderStorage_Expecter {
	return &AggSenderStorage_Expecter{mock: &_m.Mock}
}

// AddAuthProof provides a mock function with given fields: ctx, authProof
func (_m *AggSenderStorage) AddAuthProof(ctx context.Context, authProof types.AuthProof) error {
	ret := _m.Called(ctx, authProof)

	if len(ret) == 0 {
		panic("no return value specified for AddAuthProof")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, types.AuthProof) error); ok {
		r0 = rf(ctx, authProof)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// AggSenderStorage_AddAuthProof_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'AddAuthProof'
type AggSenderStorage_AddAuthProof_Call struct {
	*mock.Call
}

// AddAuthProof is a helper method to define mock.On call
//   - ctx context.Context
//   - authProof types.AuthProof
func (_e *AggSenderStorage_Expecter) AddAuthProof(ctx interface{}, authProof interface{}) *AggSenderStorage_AddAuthProof_Call {
	return &AggSenderStorage_AddAuthProof_Call{Call: _e.mock.On("AddAuthProof", ctx, authProof)}
}

func (_c *AggSenderStorage_AddAuthProof_Call) Run(run func(ctx context.Context, authProof types.AuthProof)) *AggSenderStorage_AddAuthProof_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(types.AuthProof))
	})
	return _c
}

func (_c *AggSenderStorage_AddAuthProof_Call) Return(_a0 error) *AggSenderStorage_AddAuthProof_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *AggSenderStorage_AddAuthProof_Call) RunAndReturn(run func(context.Context, types.AuthProof) error) *AggSenderStorage_AddAuthProof_Call {
	_c.Call.Return(run)
	return _c
}

// DeleteCertificate provides a mock function with given fields: ctx, certificateID
func (_m *AggSenderStorage) DeleteCertificate(ctx context.Context, certificateID common.Hash) error {
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

// AggSenderStorage_DeleteCertificate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'DeleteCertificate'
type AggSenderStorage_DeleteCertificate_Call struct {
	*mock.Call
}

// DeleteCertificate is a helper method to define mock.On call
//   - ctx context.Context
//   - certificateID common.Hash
func (_e *AggSenderStorage_Expecter) DeleteCertificate(ctx interface{}, certificateID interface{}) *AggSenderStorage_DeleteCertificate_Call {
	return &AggSenderStorage_DeleteCertificate_Call{Call: _e.mock.On("DeleteCertificate", ctx, certificateID)}
}

func (_c *AggSenderStorage_DeleteCertificate_Call) Run(run func(ctx context.Context, certificateID common.Hash)) *AggSenderStorage_DeleteCertificate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(common.Hash))
	})
	return _c
}

func (_c *AggSenderStorage_DeleteCertificate_Call) Return(_a0 error) *AggSenderStorage_DeleteCertificate_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *AggSenderStorage_DeleteCertificate_Call) RunAndReturn(run func(context.Context, common.Hash) error) *AggSenderStorage_DeleteCertificate_Call {
	_c.Call.Return(run)
	return _c
}

// GetAuthProof provides a mock function with given fields: identifier
func (_m *AggSenderStorage) GetAuthProof(identifier string) (*types.AuthProof, error) {
	ret := _m.Called(identifier)

	if len(ret) == 0 {
		panic("no return value specified for GetAuthProof")
	}

	var r0 *types.AuthProof
	var r1 error
	if rf, ok := ret.Get(0).(func(string) (*types.AuthProof, error)); ok {
		return rf(identifier)
	}
	if rf, ok := ret.Get(0).(func(string) *types.AuthProof); ok {
		r0 = rf(identifier)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.AuthProof)
		}
	}

	if rf, ok := ret.Get(1).(func(string) error); ok {
		r1 = rf(identifier)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggSenderStorage_GetAuthProof_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetAuthProof'
type AggSenderStorage_GetAuthProof_Call struct {
	*mock.Call
}

// GetAuthProof is a helper method to define mock.On call
//   - identifier string
func (_e *AggSenderStorage_Expecter) GetAuthProof(identifier interface{}) *AggSenderStorage_GetAuthProof_Call {
	return &AggSenderStorage_GetAuthProof_Call{Call: _e.mock.On("GetAuthProof", identifier)}
}

func (_c *AggSenderStorage_GetAuthProof_Call) Run(run func(identifier string)) *AggSenderStorage_GetAuthProof_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(string))
	})
	return _c
}

func (_c *AggSenderStorage_GetAuthProof_Call) Return(_a0 *types.AuthProof, _a1 error) *AggSenderStorage_GetAuthProof_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggSenderStorage_GetAuthProof_Call) RunAndReturn(run func(string) (*types.AuthProof, error)) *AggSenderStorage_GetAuthProof_Call {
	_c.Call.Return(run)
	return _c
}

// GetCertificateByHeight provides a mock function with given fields: height
func (_m *AggSenderStorage) GetCertificateByHeight(height uint64) (*types.CertificateInfo, error) {
	ret := _m.Called(height)

	if len(ret) == 0 {
		panic("no return value specified for GetCertificateByHeight")
	}

	var r0 *types.CertificateInfo
	var r1 error
	if rf, ok := ret.Get(0).(func(uint64) (*types.CertificateInfo, error)); ok {
		return rf(height)
	}
	if rf, ok := ret.Get(0).(func(uint64) *types.CertificateInfo); ok {
		r0 = rf(height)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.CertificateInfo)
		}
	}

	if rf, ok := ret.Get(1).(func(uint64) error); ok {
		r1 = rf(height)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggSenderStorage_GetCertificateByHeight_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetCertificateByHeight'
type AggSenderStorage_GetCertificateByHeight_Call struct {
	*mock.Call
}

// GetCertificateByHeight is a helper method to define mock.On call
//   - height uint64
func (_e *AggSenderStorage_Expecter) GetCertificateByHeight(height interface{}) *AggSenderStorage_GetCertificateByHeight_Call {
	return &AggSenderStorage_GetCertificateByHeight_Call{Call: _e.mock.On("GetCertificateByHeight", height)}
}

func (_c *AggSenderStorage_GetCertificateByHeight_Call) Run(run func(height uint64)) *AggSenderStorage_GetCertificateByHeight_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(uint64))
	})
	return _c
}

func (_c *AggSenderStorage_GetCertificateByHeight_Call) Return(_a0 *types.CertificateInfo, _a1 error) *AggSenderStorage_GetCertificateByHeight_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggSenderStorage_GetCertificateByHeight_Call) RunAndReturn(run func(uint64) (*types.CertificateInfo, error)) *AggSenderStorage_GetCertificateByHeight_Call {
	_c.Call.Return(run)
	return _c
}

// GetCertificatesByStatus provides a mock function with given fields: status
func (_m *AggSenderStorage) GetCertificatesByStatus(status []agglayer.CertificateStatus) ([]*types.CertificateInfo, error) {
	ret := _m.Called(status)

	if len(ret) == 0 {
		panic("no return value specified for GetCertificatesByStatus")
	}

	var r0 []*types.CertificateInfo
	var r1 error
	if rf, ok := ret.Get(0).(func([]agglayer.CertificateStatus) ([]*types.CertificateInfo, error)); ok {
		return rf(status)
	}
	if rf, ok := ret.Get(0).(func([]agglayer.CertificateStatus) []*types.CertificateInfo); ok {
		r0 = rf(status)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*types.CertificateInfo)
		}
	}

	if rf, ok := ret.Get(1).(func([]agglayer.CertificateStatus) error); ok {
		r1 = rf(status)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggSenderStorage_GetCertificatesByStatus_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetCertificatesByStatus'
type AggSenderStorage_GetCertificatesByStatus_Call struct {
	*mock.Call
}

// GetCertificatesByStatus is a helper method to define mock.On call
//   - status []agglayer.CertificateStatus
func (_e *AggSenderStorage_Expecter) GetCertificatesByStatus(status interface{}) *AggSenderStorage_GetCertificatesByStatus_Call {
	return &AggSenderStorage_GetCertificatesByStatus_Call{Call: _e.mock.On("GetCertificatesByStatus", status)}
}

func (_c *AggSenderStorage_GetCertificatesByStatus_Call) Run(run func(status []agglayer.CertificateStatus)) *AggSenderStorage_GetCertificatesByStatus_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].([]agglayer.CertificateStatus))
	})
	return _c
}

func (_c *AggSenderStorage_GetCertificatesByStatus_Call) Return(_a0 []*types.CertificateInfo, _a1 error) *AggSenderStorage_GetCertificatesByStatus_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggSenderStorage_GetCertificatesByStatus_Call) RunAndReturn(run func([]agglayer.CertificateStatus) ([]*types.CertificateInfo, error)) *AggSenderStorage_GetCertificatesByStatus_Call {
	_c.Call.Return(run)
	return _c
}

// GetLastSentCertificate provides a mock function with no fields
func (_m *AggSenderStorage) GetLastSentCertificate() (*types.CertificateInfo, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetLastSentCertificate")
	}

	var r0 *types.CertificateInfo
	var r1 error
	if rf, ok := ret.Get(0).(func() (*types.CertificateInfo, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() *types.CertificateInfo); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.CertificateInfo)
		}
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggSenderStorage_GetLastSentCertificate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetLastSentCertificate'
type AggSenderStorage_GetLastSentCertificate_Call struct {
	*mock.Call
}

// GetLastSentCertificate is a helper method to define mock.On call
func (_e *AggSenderStorage_Expecter) GetLastSentCertificate() *AggSenderStorage_GetLastSentCertificate_Call {
	return &AggSenderStorage_GetLastSentCertificate_Call{Call: _e.mock.On("GetLastSentCertificate")}
}

func (_c *AggSenderStorage_GetLastSentCertificate_Call) Run(run func()) *AggSenderStorage_GetLastSentCertificate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *AggSenderStorage_GetLastSentCertificate_Call) Return(_a0 *types.CertificateInfo, _a1 error) *AggSenderStorage_GetLastSentCertificate_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggSenderStorage_GetLastSentCertificate_Call) RunAndReturn(run func() (*types.CertificateInfo, error)) *AggSenderStorage_GetLastSentCertificate_Call {
	_c.Call.Return(run)
	return _c
}

// SaveLastSentCertificate provides a mock function with given fields: ctx, certificate
func (_m *AggSenderStorage) SaveLastSentCertificate(ctx context.Context, certificate types.CertificateInfo) error {
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

// AggSenderStorage_SaveLastSentCertificate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SaveLastSentCertificate'
type AggSenderStorage_SaveLastSentCertificate_Call struct {
	*mock.Call
}

// SaveLastSentCertificate is a helper method to define mock.On call
//   - ctx context.Context
//   - certificate types.CertificateInfo
func (_e *AggSenderStorage_Expecter) SaveLastSentCertificate(ctx interface{}, certificate interface{}) *AggSenderStorage_SaveLastSentCertificate_Call {
	return &AggSenderStorage_SaveLastSentCertificate_Call{Call: _e.mock.On("SaveLastSentCertificate", ctx, certificate)}
}

func (_c *AggSenderStorage_SaveLastSentCertificate_Call) Run(run func(ctx context.Context, certificate types.CertificateInfo)) *AggSenderStorage_SaveLastSentCertificate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(types.CertificateInfo))
	})
	return _c
}

func (_c *AggSenderStorage_SaveLastSentCertificate_Call) Return(_a0 error) *AggSenderStorage_SaveLastSentCertificate_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *AggSenderStorage_SaveLastSentCertificate_Call) RunAndReturn(run func(context.Context, types.CertificateInfo) error) *AggSenderStorage_SaveLastSentCertificate_Call {
	_c.Call.Return(run)
	return _c
}

// UpdateCertificate provides a mock function with given fields: ctx, certificate
func (_m *AggSenderStorage) UpdateCertificate(ctx context.Context, certificate types.CertificateInfo) error {
	ret := _m.Called(ctx, certificate)

	if len(ret) == 0 {
		panic("no return value specified for UpdateCertificate")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, types.CertificateInfo) error); ok {
		r0 = rf(ctx, certificate)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// AggSenderStorage_UpdateCertificate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'UpdateCertificate'
type AggSenderStorage_UpdateCertificate_Call struct {
	*mock.Call
}

// UpdateCertificate is a helper method to define mock.On call
//   - ctx context.Context
//   - certificate types.CertificateInfo
func (_e *AggSenderStorage_Expecter) UpdateCertificate(ctx interface{}, certificate interface{}) *AggSenderStorage_UpdateCertificate_Call {
	return &AggSenderStorage_UpdateCertificate_Call{Call: _e.mock.On("UpdateCertificate", ctx, certificate)}
}

func (_c *AggSenderStorage_UpdateCertificate_Call) Run(run func(ctx context.Context, certificate types.CertificateInfo)) *AggSenderStorage_UpdateCertificate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(types.CertificateInfo))
	})
	return _c
}

func (_c *AggSenderStorage_UpdateCertificate_Call) Return(_a0 error) *AggSenderStorage_UpdateCertificate_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *AggSenderStorage_UpdateCertificate_Call) RunAndReturn(run func(context.Context, types.CertificateInfo) error) *AggSenderStorage_UpdateCertificate_Call {
	_c.Call.Return(run)
	return _c
}

// ValidateProof provides a mock function with given fields: req
func (_m *AggSenderStorage) ValidateProof(req *types.ProofRequest) (bool, error) {
	ret := _m.Called(req)

	if len(ret) == 0 {
		panic("no return value specified for ValidateProof")
	}

	var r0 bool
	var r1 error
	if rf, ok := ret.Get(0).(func(*types.ProofRequest) (bool, error)); ok {
		return rf(req)
	}
	if rf, ok := ret.Get(0).(func(*types.ProofRequest) bool); ok {
		r0 = rf(req)
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func(*types.ProofRequest) error); ok {
		r1 = rf(req)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggSenderStorage_ValidateProof_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ValidateProof'
type AggSenderStorage_ValidateProof_Call struct {
	*mock.Call
}

// ValidateProof is a helper method to define mock.On call
//   - req *types.ProofRequest
func (_e *AggSenderStorage_Expecter) ValidateProof(req interface{}) *AggSenderStorage_ValidateProof_Call {
	return &AggSenderStorage_ValidateProof_Call{Call: _e.mock.On("ValidateProof", req)}
}

func (_c *AggSenderStorage_ValidateProof_Call) Run(run func(req *types.ProofRequest)) *AggSenderStorage_ValidateProof_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(*types.ProofRequest))
	})
	return _c
}

func (_c *AggSenderStorage_ValidateProof_Call) Return(_a0 bool, _a1 error) *AggSenderStorage_ValidateProof_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggSenderStorage_ValidateProof_Call) RunAndReturn(run func(*types.ProofRequest) (bool, error)) *AggSenderStorage_ValidateProof_Call {
	_c.Call.Return(run)
	return _c
}

// NewAggSenderStorage creates a new instance of AggSenderStorage. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewAggSenderStorage(t interface {
	mock.TestingT
	Cleanup(func())
}) *AggSenderStorage {
	mock := &AggSenderStorage{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
