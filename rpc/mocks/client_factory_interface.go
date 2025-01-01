// Code generated by mockery v2.50.2. DO NOT EDIT.

package mocks

import (
	rpc "github.com/agglayer/aggkit/rpc/client"
	mock "github.com/stretchr/testify/mock"
)

// ClientFactoryInterface is an autogenerated mock type for the ClientFactoryInterface type
type ClientFactoryInterface struct {
	mock.Mock
}

type ClientFactoryInterface_Expecter struct {
	mock *mock.Mock
}

func (_m *ClientFactoryInterface) EXPECT() *ClientFactoryInterface_Expecter {
	return &ClientFactoryInterface_Expecter{mock: &_m.Mock}
}

// NewClient provides a mock function with given fields: url
func (_m *ClientFactoryInterface) NewClient(url string) rpc.ClientInterface {
	ret := _m.Called(url)

	if len(ret) == 0 {
		panic("no return value specified for NewClient")
	}

	var r0 rpc.ClientInterface
	if rf, ok := ret.Get(0).(func(string) rpc.ClientInterface); ok {
		r0 = rf(url)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(rpc.ClientInterface)
		}
	}

	return r0
}

// ClientFactoryInterface_NewClient_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'NewClient'
type ClientFactoryInterface_NewClient_Call struct {
	*mock.Call
}

// NewClient is a helper method to define mock.On call
//   - url string
func (_e *ClientFactoryInterface_Expecter) NewClient(url interface{}) *ClientFactoryInterface_NewClient_Call {
	return &ClientFactoryInterface_NewClient_Call{Call: _e.mock.On("NewClient", url)}
}

func (_c *ClientFactoryInterface_NewClient_Call) Run(run func(url string)) *ClientFactoryInterface_NewClient_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(string))
	})
	return _c
}

func (_c *ClientFactoryInterface_NewClient_Call) Return(_a0 rpc.ClientInterface) *ClientFactoryInterface_NewClient_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *ClientFactoryInterface_NewClient_Call) RunAndReturn(run func(string) rpc.ClientInterface) *ClientFactoryInterface_NewClient_Call {
	_c.Call.Return(run)
	return _c
}

// NewClientFactoryInterface creates a new instance of ClientFactoryInterface. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewClientFactoryInterface(t interface {
	mock.TestingT
	Cleanup(func())
}) *ClientFactoryInterface {
	mock := &ClientFactoryInterface{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
