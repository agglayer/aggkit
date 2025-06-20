// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"

	mock "github.com/stretchr/testify/mock"

	types "github.com/agglayer/aggkit/aggsender/types"
)

// AggsenderFlow is an autogenerated mock type for the AggsenderFlow type
type AggsenderFlow struct {
	mock.Mock
}

type AggsenderFlow_Expecter struct {
	mock *mock.Mock
}

func (_m *AggsenderFlow) EXPECT() *AggsenderFlow_Expecter {
	return &AggsenderFlow_Expecter{mock: &_m.Mock}
}

// BuildCertificate provides a mock function with given fields: ctx, buildParams
func (_m *AggsenderFlow) BuildCertificate(ctx context.Context, buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error) {
	ret := _m.Called(ctx, buildParams)

	if len(ret) == 0 {
		panic("no return value specified for BuildCertificate")
	}

	var r0 *agglayertypes.Certificate
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *types.CertificateBuildParams) (*agglayertypes.Certificate, error)); ok {
		return rf(ctx, buildParams)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *types.CertificateBuildParams) *agglayertypes.Certificate); ok {
		r0 = rf(ctx, buildParams)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*agglayertypes.Certificate)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *types.CertificateBuildParams) error); ok {
		r1 = rf(ctx, buildParams)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggsenderFlow_BuildCertificate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'BuildCertificate'
type AggsenderFlow_BuildCertificate_Call struct {
	*mock.Call
}

// BuildCertificate is a helper method to define mock.On call
//   - ctx context.Context
//   - buildParams *types.CertificateBuildParams
func (_e *AggsenderFlow_Expecter) BuildCertificate(ctx interface{}, buildParams interface{}) *AggsenderFlow_BuildCertificate_Call {
	return &AggsenderFlow_BuildCertificate_Call{Call: _e.mock.On("BuildCertificate", ctx, buildParams)}
}

func (_c *AggsenderFlow_BuildCertificate_Call) Run(run func(ctx context.Context, buildParams *types.CertificateBuildParams)) *AggsenderFlow_BuildCertificate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*types.CertificateBuildParams))
	})
	return _c
}

func (_c *AggsenderFlow_BuildCertificate_Call) Return(_a0 *agglayertypes.Certificate, _a1 error) *AggsenderFlow_BuildCertificate_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggsenderFlow_BuildCertificate_Call) RunAndReturn(run func(context.Context, *types.CertificateBuildParams) (*agglayertypes.Certificate, error)) *AggsenderFlow_BuildCertificate_Call {
	_c.Call.Return(run)
	return _c
}

// CheckInitialStatus provides a mock function with given fields: ctx
func (_m *AggsenderFlow) CheckInitialStatus(ctx context.Context) error {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for CheckInitialStatus")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context) error); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// AggsenderFlow_CheckInitialStatus_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'CheckInitialStatus'
type AggsenderFlow_CheckInitialStatus_Call struct {
	*mock.Call
}

// CheckInitialStatus is a helper method to define mock.On call
//   - ctx context.Context
func (_e *AggsenderFlow_Expecter) CheckInitialStatus(ctx interface{}) *AggsenderFlow_CheckInitialStatus_Call {
	return &AggsenderFlow_CheckInitialStatus_Call{Call: _e.mock.On("CheckInitialStatus", ctx)}
}

func (_c *AggsenderFlow_CheckInitialStatus_Call) Run(run func(ctx context.Context)) *AggsenderFlow_CheckInitialStatus_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *AggsenderFlow_CheckInitialStatus_Call) Return(_a0 error) *AggsenderFlow_CheckInitialStatus_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *AggsenderFlow_CheckInitialStatus_Call) RunAndReturn(run func(context.Context) error) *AggsenderFlow_CheckInitialStatus_Call {
	_c.Call.Return(run)
	return _c
}

// GetCertificateBuildParams provides a mock function with given fields: ctx
func (_m *AggsenderFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for GetCertificateBuildParams")
	}

	var r0 *types.CertificateBuildParams
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (*types.CertificateBuildParams, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) *types.CertificateBuildParams); ok {
		r0 = rf(ctx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.CertificateBuildParams)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggsenderFlow_GetCertificateBuildParams_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetCertificateBuildParams'
type AggsenderFlow_GetCertificateBuildParams_Call struct {
	*mock.Call
}

// GetCertificateBuildParams is a helper method to define mock.On call
//   - ctx context.Context
func (_e *AggsenderFlow_Expecter) GetCertificateBuildParams(ctx interface{}) *AggsenderFlow_GetCertificateBuildParams_Call {
	return &AggsenderFlow_GetCertificateBuildParams_Call{Call: _e.mock.On("GetCertificateBuildParams", ctx)}
}

func (_c *AggsenderFlow_GetCertificateBuildParams_Call) Run(run func(ctx context.Context)) *AggsenderFlow_GetCertificateBuildParams_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context))
	})
	return _c
}

func (_c *AggsenderFlow_GetCertificateBuildParams_Call) Return(_a0 *types.CertificateBuildParams, _a1 error) *AggsenderFlow_GetCertificateBuildParams_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggsenderFlow_GetCertificateBuildParams_Call) RunAndReturn(run func(context.Context) (*types.CertificateBuildParams, error)) *AggsenderFlow_GetCertificateBuildParams_Call {
	_c.Call.Return(run)
	return _c
}

// NewAggsenderFlow creates a new instance of AggsenderFlow. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewAggsenderFlow(t interface {
	mock.TestingT
	Cleanup(func())
}) *AggsenderFlow {
	mock := &AggsenderFlow{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
