// Code generated by mockery v2.51.0. DO NOT EDIT.

package mocks

import (
	types "github.com/agglayer/aggkit/aggsender/types"
	mock "github.com/stretchr/testify/mock"
)

// AggsenderStorer is an autogenerated mock type for the AggsenderStorer type
type AggsenderStorer struct {
	mock.Mock
}

type AggsenderStorer_Expecter struct {
	mock *mock.Mock
}

func (_m *AggsenderStorer) EXPECT() *AggsenderStorer_Expecter {
	return &AggsenderStorer_Expecter{mock: &_m.Mock}
}

// GetCertificateByHeight provides a mock function with given fields: height
func (_m *AggsenderStorer) GetCertificateByHeight(height uint64) (*types.CertificateInfo, error) {
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

// AggsenderStorer_GetCertificateByHeight_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetCertificateByHeight'
type AggsenderStorer_GetCertificateByHeight_Call struct {
	*mock.Call
}

// GetCertificateByHeight is a helper method to define mock.On call
//   - height uint64
func (_e *AggsenderStorer_Expecter) GetCertificateByHeight(height interface{}) *AggsenderStorer_GetCertificateByHeight_Call {
	return &AggsenderStorer_GetCertificateByHeight_Call{Call: _e.mock.On("GetCertificateByHeight", height)}
}

func (_c *AggsenderStorer_GetCertificateByHeight_Call) Run(run func(height uint64)) *AggsenderStorer_GetCertificateByHeight_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(uint64))
	})
	return _c
}

func (_c *AggsenderStorer_GetCertificateByHeight_Call) Return(_a0 *types.CertificateInfo, _a1 error) *AggsenderStorer_GetCertificateByHeight_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggsenderStorer_GetCertificateByHeight_Call) RunAndReturn(run func(uint64) (*types.CertificateInfo, error)) *AggsenderStorer_GetCertificateByHeight_Call {
	_c.Call.Return(run)
	return _c
}

// GetLastSentCertificate provides a mock function with no fields
func (_m *AggsenderStorer) GetLastSentCertificate() (*types.CertificateInfo, error) {
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

// AggsenderStorer_GetLastSentCertificate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetLastSentCertificate'
type AggsenderStorer_GetLastSentCertificate_Call struct {
	*mock.Call
}

// GetLastSentCertificate is a helper method to define mock.On call
func (_e *AggsenderStorer_Expecter) GetLastSentCertificate() *AggsenderStorer_GetLastSentCertificate_Call {
	return &AggsenderStorer_GetLastSentCertificate_Call{Call: _e.mock.On("GetLastSentCertificate")}
}

func (_c *AggsenderStorer_GetLastSentCertificate_Call) Run(run func()) *AggsenderStorer_GetLastSentCertificate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *AggsenderStorer_GetLastSentCertificate_Call) Return(_a0 *types.CertificateInfo, _a1 error) *AggsenderStorer_GetLastSentCertificate_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggsenderStorer_GetLastSentCertificate_Call) RunAndReturn(run func() (*types.CertificateInfo, error)) *AggsenderStorer_GetLastSentCertificate_Call {
	_c.Call.Return(run)
	return _c
}

// NewAggsenderStorer creates a new instance of AggsenderStorer. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewAggsenderStorer(t interface {
	mock.TestingT
	Cleanup(func())
}) *AggsenderStorer {
	mock := &AggsenderStorer{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
