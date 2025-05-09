// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	types "github.com/agglayer/aggkit/aggsender/types"
	mock "github.com/stretchr/testify/mock"
)

// BlockNotifier is an autogenerated mock type for the BlockNotifier type
type BlockNotifier struct {
	mock.Mock
}

type BlockNotifier_Expecter struct {
	mock *mock.Mock
}

func (_m *BlockNotifier) EXPECT() *BlockNotifier_Expecter {
	return &BlockNotifier_Expecter{mock: &_m.Mock}
}

// GetCurrentBlockNumber provides a mock function with no fields
func (_m *BlockNotifier) GetCurrentBlockNumber() uint64 {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetCurrentBlockNumber")
	}

	var r0 uint64
	if rf, ok := ret.Get(0).(func() uint64); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint64)
	}

	return r0
}

// BlockNotifier_GetCurrentBlockNumber_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetCurrentBlockNumber'
type BlockNotifier_GetCurrentBlockNumber_Call struct {
	*mock.Call
}

// GetCurrentBlockNumber is a helper method to define mock.On call
func (_e *BlockNotifier_Expecter) GetCurrentBlockNumber() *BlockNotifier_GetCurrentBlockNumber_Call {
	return &BlockNotifier_GetCurrentBlockNumber_Call{Call: _e.mock.On("GetCurrentBlockNumber")}
}

func (_c *BlockNotifier_GetCurrentBlockNumber_Call) Run(run func()) *BlockNotifier_GetCurrentBlockNumber_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *BlockNotifier_GetCurrentBlockNumber_Call) Return(_a0 uint64) *BlockNotifier_GetCurrentBlockNumber_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *BlockNotifier_GetCurrentBlockNumber_Call) RunAndReturn(run func() uint64) *BlockNotifier_GetCurrentBlockNumber_Call {
	_c.Call.Return(run)
	return _c
}

// String provides a mock function with no fields
func (_m *BlockNotifier) String() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for String")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// BlockNotifier_String_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'String'
type BlockNotifier_String_Call struct {
	*mock.Call
}

// String is a helper method to define mock.On call
func (_e *BlockNotifier_Expecter) String() *BlockNotifier_String_Call {
	return &BlockNotifier_String_Call{Call: _e.mock.On("String")}
}

func (_c *BlockNotifier_String_Call) Run(run func()) *BlockNotifier_String_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *BlockNotifier_String_Call) Return(_a0 string) *BlockNotifier_String_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *BlockNotifier_String_Call) RunAndReturn(run func() string) *BlockNotifier_String_Call {
	_c.Call.Return(run)
	return _c
}

// Subscribe provides a mock function with given fields: id
func (_m *BlockNotifier) Subscribe(id string) <-chan types.EventNewBlock {
	ret := _m.Called(id)

	if len(ret) == 0 {
		panic("no return value specified for Subscribe")
	}

	var r0 <-chan types.EventNewBlock
	if rf, ok := ret.Get(0).(func(string) <-chan types.EventNewBlock); ok {
		r0 = rf(id)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(<-chan types.EventNewBlock)
		}
	}

	return r0
}

// BlockNotifier_Subscribe_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Subscribe'
type BlockNotifier_Subscribe_Call struct {
	*mock.Call
}

// Subscribe is a helper method to define mock.On call
//   - id string
func (_e *BlockNotifier_Expecter) Subscribe(id interface{}) *BlockNotifier_Subscribe_Call {
	return &BlockNotifier_Subscribe_Call{Call: _e.mock.On("Subscribe", id)}
}

func (_c *BlockNotifier_Subscribe_Call) Run(run func(id string)) *BlockNotifier_Subscribe_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(string))
	})
	return _c
}

func (_c *BlockNotifier_Subscribe_Call) Return(_a0 <-chan types.EventNewBlock) *BlockNotifier_Subscribe_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *BlockNotifier_Subscribe_Call) RunAndReturn(run func(string) <-chan types.EventNewBlock) *BlockNotifier_Subscribe_Call {
	_c.Call.Return(run)
	return _c
}

// NewBlockNotifier creates a new instance of BlockNotifier. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewBlockNotifier(t interface {
	mock.TestingT
	Cleanup(func())
}) *BlockNotifier {
	mock := &BlockNotifier{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
