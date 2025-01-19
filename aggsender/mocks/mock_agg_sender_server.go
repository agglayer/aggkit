// Code generated by mockery v2.51.0. DO NOT EDIT.

package mocks

import (
	context "context"

	types "github.com/agglayer/aggkit/aggsender/types"
	mock "github.com/stretchr/testify/mock"
)

// AggSenderServer is an autogenerated mock type for the AggSenderServer type
type AggSenderServer struct {
	mock.Mock
}

type AggSenderServer_Expecter struct {
	mock *mock.Mock
}

func (_m *AggSenderServer) EXPECT() *AggSenderServer_Expecter {
	return &AggSenderServer_Expecter{mock: &_m.Mock}
}

// ReceiveAuthProof provides a mock function with given fields: _a0, _a1
func (_m *AggSenderServer) ReceiveAuthProof(_a0 context.Context, _a1 *types.ProofRequest) (*types.ProofResponse, error) {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for ReceiveAuthProof")
	}

	var r0 *types.ProofResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *types.ProofRequest) (*types.ProofResponse, error)); ok {
		return rf(_a0, _a1)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *types.ProofRequest) *types.ProofResponse); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ProofResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *types.ProofRequest) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggSenderServer_ReceiveAuthProof_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ReceiveAuthProof'
type AggSenderServer_ReceiveAuthProof_Call struct {
	*mock.Call
}

// ReceiveAuthProof is a helper method to define mock.On call
//   - _a0 context.Context
//   - _a1 *types.ProofRequest
func (_e *AggSenderServer_Expecter) ReceiveAuthProof(_a0 interface{}, _a1 interface{}) *AggSenderServer_ReceiveAuthProof_Call {
	return &AggSenderServer_ReceiveAuthProof_Call{Call: _e.mock.On("ReceiveAuthProof", _a0, _a1)}
}

func (_c *AggSenderServer_ReceiveAuthProof_Call) Run(run func(_a0 context.Context, _a1 *types.ProofRequest)) *AggSenderServer_ReceiveAuthProof_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*types.ProofRequest))
	})
	return _c
}

func (_c *AggSenderServer_ReceiveAuthProof_Call) Return(_a0 *types.ProofResponse, _a1 error) *AggSenderServer_ReceiveAuthProof_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggSenderServer_ReceiveAuthProof_Call) RunAndReturn(run func(context.Context, *types.ProofRequest) (*types.ProofResponse, error)) *AggSenderServer_ReceiveAuthProof_Call {
	_c.Call.Return(run)
	return _c
}

// mustEmbedUnimplementedAggSenderServer provides a mock function with no fields
func (_m *AggSenderServer) mustEmbedUnimplementedAggSenderServer() {
	_m.Called()
}

// AggSenderServer_mustEmbedUnimplementedAggSenderServer_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'mustEmbedUnimplementedAggSenderServer'
type AggSenderServer_mustEmbedUnimplementedAggSenderServer_Call struct {
	*mock.Call
}

// mustEmbedUnimplementedAggSenderServer is a helper method to define mock.On call
func (_e *AggSenderServer_Expecter) mustEmbedUnimplementedAggSenderServer() *AggSenderServer_mustEmbedUnimplementedAggSenderServer_Call {
	return &AggSenderServer_mustEmbedUnimplementedAggSenderServer_Call{Call: _e.mock.On("mustEmbedUnimplementedAggSenderServer")}
}

func (_c *AggSenderServer_mustEmbedUnimplementedAggSenderServer_Call) Run(run func()) *AggSenderServer_mustEmbedUnimplementedAggSenderServer_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *AggSenderServer_mustEmbedUnimplementedAggSenderServer_Call) Return() *AggSenderServer_mustEmbedUnimplementedAggSenderServer_Call {
	_c.Call.Return()
	return _c
}

func (_c *AggSenderServer_mustEmbedUnimplementedAggSenderServer_Call) RunAndReturn(run func()) *AggSenderServer_mustEmbedUnimplementedAggSenderServer_Call {
	_c.Run(run)
	return _c
}

// NewAggSenderServer creates a new instance of AggSenderServer. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewAggSenderServer(t interface {
	mock.TestingT
	Cleanup(func())
}) *AggSenderServer {
	mock := &AggSenderServer{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
