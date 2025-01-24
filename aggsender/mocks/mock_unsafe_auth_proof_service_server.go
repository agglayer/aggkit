// Code generated by mockery. DO NOT EDIT.

package mocks

import mock "github.com/stretchr/testify/mock"

// UnsafeAuthProofServiceServer is an autogenerated mock type for the UnsafeAuthProofServiceServer type
type UnsafeAuthProofServiceServer struct {
	mock.Mock
}

type UnsafeAuthProofServiceServer_Expecter struct {
	mock *mock.Mock
}

func (_m *UnsafeAuthProofServiceServer) EXPECT() *UnsafeAuthProofServiceServer_Expecter {
	return &UnsafeAuthProofServiceServer_Expecter{mock: &_m.Mock}
}

// mustEmbedUnimplementedAuthProofServiceServer provides a mock function with no fields
func (_m *UnsafeAuthProofServiceServer) mustEmbedUnimplementedAuthProofServiceServer() {
	_m.Called()
}

// UnsafeAuthProofServiceServer_mustEmbedUnimplementedAuthProofServiceServer_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'mustEmbedUnimplementedAuthProofServiceServer'
type UnsafeAuthProofServiceServer_mustEmbedUnimplementedAuthProofServiceServer_Call struct {
	*mock.Call
}

// mustEmbedUnimplementedAuthProofServiceServer is a helper method to define mock.On call
func (_e *UnsafeAuthProofServiceServer_Expecter) mustEmbedUnimplementedAuthProofServiceServer() *UnsafeAuthProofServiceServer_mustEmbedUnimplementedAuthProofServiceServer_Call {
	return &UnsafeAuthProofServiceServer_mustEmbedUnimplementedAuthProofServiceServer_Call{Call: _e.mock.On("mustEmbedUnimplementedAuthProofServiceServer")}
}

func (_c *UnsafeAuthProofServiceServer_mustEmbedUnimplementedAuthProofServiceServer_Call) Run(run func()) *UnsafeAuthProofServiceServer_mustEmbedUnimplementedAuthProofServiceServer_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *UnsafeAuthProofServiceServer_mustEmbedUnimplementedAuthProofServiceServer_Call) Return() *UnsafeAuthProofServiceServer_mustEmbedUnimplementedAuthProofServiceServer_Call {
	_c.Call.Return()
	return _c
}

func (_c *UnsafeAuthProofServiceServer_mustEmbedUnimplementedAuthProofServiceServer_Call) RunAndReturn(run func()) *UnsafeAuthProofServiceServer_mustEmbedUnimplementedAuthProofServiceServer_Call {
	_c.Run(run)
	return _c
}

// NewUnsafeAuthProofServiceServer creates a new instance of UnsafeAuthProofServiceServer. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewUnsafeAuthProofServiceServer(t interface {
	mock.TestingT
	Cleanup(func())
}) *UnsafeAuthProofServiceServer {
	mock := &UnsafeAuthProofServiceServer{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
