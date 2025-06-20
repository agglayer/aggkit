// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	types "github.com/agglayer/aggkit/db/types"
	mock "github.com/stretchr/testify/mock"
)

// CompatibilityDataStorager is an autogenerated mock type for the CompatibilityDataStorager type
type CompatibilityDataStorager[T any] struct {
	mock.Mock
}

type CompatibilityDataStorager_Expecter[T any] struct {
	mock *mock.Mock
}

func (_m *CompatibilityDataStorager[T]) EXPECT() *CompatibilityDataStorager_Expecter[T] {
	return &CompatibilityDataStorager_Expecter[T]{mock: &_m.Mock}
}

// GetCompatibilityData provides a mock function with given fields: ctx, tx
func (_m *CompatibilityDataStorager[T]) GetCompatibilityData(ctx context.Context, tx types.Querier) (bool, T, error) {
	ret := _m.Called(ctx, tx)

	if len(ret) == 0 {
		panic("no return value specified for GetCompatibilityData")
	}

	var r0 bool
	var r1 T
	var r2 error
	if rf, ok := ret.Get(0).(func(context.Context, types.Querier) (bool, T, error)); ok {
		return rf(ctx, tx)
	}
	if rf, ok := ret.Get(0).(func(context.Context, types.Querier) bool); ok {
		r0 = rf(ctx, tx)
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func(context.Context, types.Querier) T); ok {
		r1 = rf(ctx, tx)
	} else {
		if ret.Get(1) != nil {
			r1 = ret.Get(1).(T)
		}
	}

	if rf, ok := ret.Get(2).(func(context.Context, types.Querier) error); ok {
		r2 = rf(ctx, tx)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// CompatibilityDataStorager_GetCompatibilityData_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetCompatibilityData'
type CompatibilityDataStorager_GetCompatibilityData_Call[T any] struct {
	*mock.Call
}

// GetCompatibilityData is a helper method to define mock.On call
//   - ctx context.Context
//   - tx types.Querier
func (_e *CompatibilityDataStorager_Expecter[T]) GetCompatibilityData(ctx interface{}, tx interface{}) *CompatibilityDataStorager_GetCompatibilityData_Call[T] {
	return &CompatibilityDataStorager_GetCompatibilityData_Call[T]{Call: _e.mock.On("GetCompatibilityData", ctx, tx)}
}

func (_c *CompatibilityDataStorager_GetCompatibilityData_Call[T]) Run(run func(ctx context.Context, tx types.Querier)) *CompatibilityDataStorager_GetCompatibilityData_Call[T] {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(types.Querier))
	})
	return _c
}

func (_c *CompatibilityDataStorager_GetCompatibilityData_Call[T]) Return(_a0 bool, _a1 T, _a2 error) *CompatibilityDataStorager_GetCompatibilityData_Call[T] {
	_c.Call.Return(_a0, _a1, _a2)
	return _c
}

func (_c *CompatibilityDataStorager_GetCompatibilityData_Call[T]) RunAndReturn(run func(context.Context, types.Querier) (bool, T, error)) *CompatibilityDataStorager_GetCompatibilityData_Call[T] {
	_c.Call.Return(run)
	return _c
}

// SetCompatibilityData provides a mock function with given fields: ctx, tx, data
func (_m *CompatibilityDataStorager[T]) SetCompatibilityData(ctx context.Context, tx types.Querier, data T) error {
	ret := _m.Called(ctx, tx, data)

	if len(ret) == 0 {
		panic("no return value specified for SetCompatibilityData")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, types.Querier, T) error); ok {
		r0 = rf(ctx, tx, data)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// CompatibilityDataStorager_SetCompatibilityData_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SetCompatibilityData'
type CompatibilityDataStorager_SetCompatibilityData_Call[T any] struct {
	*mock.Call
}

// SetCompatibilityData is a helper method to define mock.On call
//   - ctx context.Context
//   - tx types.Querier
//   - data T
func (_e *CompatibilityDataStorager_Expecter[T]) SetCompatibilityData(ctx interface{}, tx interface{}, data interface{}) *CompatibilityDataStorager_SetCompatibilityData_Call[T] {
	return &CompatibilityDataStorager_SetCompatibilityData_Call[T]{Call: _e.mock.On("SetCompatibilityData", ctx, tx, data)}
}

func (_c *CompatibilityDataStorager_SetCompatibilityData_Call[T]) Run(run func(ctx context.Context, tx types.Querier, data T)) *CompatibilityDataStorager_SetCompatibilityData_Call[T] {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(types.Querier), args[2].(T))
	})
	return _c
}

func (_c *CompatibilityDataStorager_SetCompatibilityData_Call[T]) Return(_a0 error) *CompatibilityDataStorager_SetCompatibilityData_Call[T] {
	_c.Call.Return(_a0)
	return _c
}

func (_c *CompatibilityDataStorager_SetCompatibilityData_Call[T]) RunAndReturn(run func(context.Context, types.Querier, T) error) *CompatibilityDataStorager_SetCompatibilityData_Call[T] {
	_c.Call.Return(run)
	return _c
}

// NewCompatibilityDataStorager creates a new instance of CompatibilityDataStorager. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewCompatibilityDataStorager[T any](t interface {
	mock.TestingT
	Cleanup(func())
}) *CompatibilityDataStorager[T] {
	mock := &CompatibilityDataStorager[T]{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
