// Code generated by mockery. DO NOT EDIT.

package mocks

import mock "github.com/stretchr/testify/mock"

// OptimisticModeQuerier is an autogenerated mock type for the OptimisticModeQuerier type
type OptimisticModeQuerier struct {
	mock.Mock
}

type OptimisticModeQuerier_Expecter struct {
	mock *mock.Mock
}

func (_m *OptimisticModeQuerier) EXPECT() *OptimisticModeQuerier_Expecter {
	return &OptimisticModeQuerier_Expecter{mock: &_m.Mock}
}

// IsOptimisticModeOn provides a mock function with no fields
func (_m *OptimisticModeQuerier) IsOptimisticModeOn() (bool, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for IsOptimisticModeOn")
	}

	var r0 bool
	var r1 error
	if rf, ok := ret.Get(0).(func() (bool, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// OptimisticModeQuerier_IsOptimisticModeOn_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'IsOptimisticModeOn'
type OptimisticModeQuerier_IsOptimisticModeOn_Call struct {
	*mock.Call
}

// IsOptimisticModeOn is a helper method to define mock.On call
func (_e *OptimisticModeQuerier_Expecter) IsOptimisticModeOn() *OptimisticModeQuerier_IsOptimisticModeOn_Call {
	return &OptimisticModeQuerier_IsOptimisticModeOn_Call{Call: _e.mock.On("IsOptimisticModeOn")}
}

func (_c *OptimisticModeQuerier_IsOptimisticModeOn_Call) Run(run func()) *OptimisticModeQuerier_IsOptimisticModeOn_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *OptimisticModeQuerier_IsOptimisticModeOn_Call) Return(_a0 bool, _a1 error) *OptimisticModeQuerier_IsOptimisticModeOn_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *OptimisticModeQuerier_IsOptimisticModeOn_Call) RunAndReturn(run func() (bool, error)) *OptimisticModeQuerier_IsOptimisticModeOn_Call {
	_c.Call.Return(run)
	return _c
}

// NewOptimisticModeQuerier creates a new instance of OptimisticModeQuerier. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewOptimisticModeQuerier(t interface {
	mock.TestingT
	Cleanup(func())
}) *OptimisticModeQuerier {
	mock := &OptimisticModeQuerier{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
