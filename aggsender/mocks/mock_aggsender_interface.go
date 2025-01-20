// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	types "github.com/agglayer/aggkit/aggsender/types"
	mock "github.com/stretchr/testify/mock"
)

// AggsenderInterface is an autogenerated mock type for the AggsenderInterface type
type AggsenderInterface struct {
	mock.Mock
}

type AggsenderInterface_Expecter struct {
	mock *mock.Mock
}

func (_m *AggsenderInterface) EXPECT() *AggsenderInterface_Expecter {
	return &AggsenderInterface_Expecter{mock: &_m.Mock}
}

// Info provides a mock function with no fields
func (_m *AggsenderInterface) Info() types.AggsenderInfo {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Info")
	}

	var r0 types.AggsenderInfo
	if rf, ok := ret.Get(0).(func() types.AggsenderInfo); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(types.AggsenderInfo)
	}

	return r0
}

// AggsenderInterface_Info_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Info'
type AggsenderInterface_Info_Call struct {
	*mock.Call
}

// Info is a helper method to define mock.On call
func (_e *AggsenderInterface_Expecter) Info() *AggsenderInterface_Info_Call {
	return &AggsenderInterface_Info_Call{Call: _e.mock.On("Info")}
}

func (_c *AggsenderInterface_Info_Call) Run(run func()) *AggsenderInterface_Info_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *AggsenderInterface_Info_Call) Return(_a0 types.AggsenderInfo) *AggsenderInterface_Info_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *AggsenderInterface_Info_Call) RunAndReturn(run func() types.AggsenderInfo) *AggsenderInterface_Info_Call {
	_c.Call.Return(run)
	return _c
}

// NewAggsenderInterface creates a new instance of AggsenderInterface. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewAggsenderInterface(t interface {
	mock.TestingT
	Cleanup(func())
}) *AggsenderInterface {
	mock := &AggsenderInterface{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
