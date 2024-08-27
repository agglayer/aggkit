// Code generated by mockery v2.40.1. DO NOT EDIT.

package sync

import (
	context "context"

	mock "github.com/stretchr/testify/mock"
)

// ProcessorMock is an autogenerated mock type for the processorInterface type
type ProcessorMock struct {
	mock.Mock
}

// GetLastProcessedBlock provides a mock function with given fields: ctx
func (_m *ProcessorMock) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for GetLastProcessedBlock")
	}

	var r0 uint64
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (uint64, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) uint64); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Get(0).(uint64)
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ProcessBlock provides a mock function with given fields: ctx, block
func (_m *ProcessorMock) ProcessBlock(ctx context.Context, block Block) error {
	ret := _m.Called(ctx, block)

	if len(ret) == 0 {
		panic("no return value specified for ProcessBlock")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, Block) error); ok {
		r0 = rf(ctx, block)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Reorg provides a mock function with given fields: ctx, firstReorgedBlock
func (_m *ProcessorMock) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	ret := _m.Called(ctx, firstReorgedBlock)

	if len(ret) == 0 {
		panic("no return value specified for Reorg")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, uint64) error); ok {
		r0 = rf(ctx, firstReorgedBlock)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewProcessorMock creates a new instance of ProcessorMock. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewProcessorMock(t interface {
	mock.TestingT
	Cleanup(func())
}) *ProcessorMock {
	mock := &ProcessorMock{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
