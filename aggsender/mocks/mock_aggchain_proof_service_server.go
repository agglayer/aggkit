// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	types "github.com/agglayer/aggkit/aggsender/types"
	mock "github.com/stretchr/testify/mock"
)

// AggchainProofServiceServer is an autogenerated mock type for the AggchainProofServiceServer type
type AggchainProofServiceServer struct {
	mock.Mock
}

type AggchainProofServiceServer_Expecter struct {
	mock *mock.Mock
}

func (_m *AggchainProofServiceServer) EXPECT() *AggchainProofServiceServer_Expecter {
	return &AggchainProofServiceServer_Expecter{mock: &_m.Mock}
}

// GenerateAggchainProof provides a mock function with given fields: _a0, _a1
func (_m *AggchainProofServiceServer) GenerateAggchainProof(_a0 context.Context, _a1 *types.GenerateAggchainProofRequest) (*types.GenerateAggchainProofResponse, error) {
	ret := _m.Called(_a0, _a1)

	if len(ret) == 0 {
		panic("no return value specified for GenerateAggchainProof")
	}

	var r0 *types.GenerateAggchainProofResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *types.GenerateAggchainProofRequest) (*types.GenerateAggchainProofResponse, error)); ok {
		return rf(_a0, _a1)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *types.GenerateAggchainProofRequest) *types.GenerateAggchainProofResponse); ok {
		r0 = rf(_a0, _a1)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.GenerateAggchainProofResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *types.GenerateAggchainProofRequest) error); ok {
		r1 = rf(_a0, _a1)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggchainProofServiceServer_GenerateAggchainProof_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GenerateAggchainProof'
type AggchainProofServiceServer_GenerateAggchainProof_Call struct {
	*mock.Call
}

// GenerateAggchainProof is a helper method to define mock.On call
//   - _a0 context.Context
//   - _a1 *types.GenerateAggchainProofRequest
func (_e *AggchainProofServiceServer_Expecter) GenerateAggchainProof(_a0 interface{}, _a1 interface{}) *AggchainProofServiceServer_GenerateAggchainProof_Call {
	return &AggchainProofServiceServer_GenerateAggchainProof_Call{Call: _e.mock.On("GenerateAggchainProof", _a0, _a1)}
}

func (_c *AggchainProofServiceServer_GenerateAggchainProof_Call) Run(run func(_a0 context.Context, _a1 *types.GenerateAggchainProofRequest)) *AggchainProofServiceServer_GenerateAggchainProof_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*types.GenerateAggchainProofRequest))
	})
	return _c
}

func (_c *AggchainProofServiceServer_GenerateAggchainProof_Call) Return(_a0 *types.GenerateAggchainProofResponse, _a1 error) *AggchainProofServiceServer_GenerateAggchainProof_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggchainProofServiceServer_GenerateAggchainProof_Call) RunAndReturn(run func(context.Context, *types.GenerateAggchainProofRequest) (*types.GenerateAggchainProofResponse, error)) *AggchainProofServiceServer_GenerateAggchainProof_Call {
	_c.Call.Return(run)
	return _c
}

// mustEmbedUnimplementedAggchainProofServiceServer provides a mock function with no fields
func (_m *AggchainProofServiceServer) mustEmbedUnimplementedAggchainProofServiceServer() {
	_m.Called()
}

// AggchainProofServiceServer_mustEmbedUnimplementedAggchainProofServiceServer_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'mustEmbedUnimplementedAggchainProofServiceServer'
type AggchainProofServiceServer_mustEmbedUnimplementedAggchainProofServiceServer_Call struct {
	*mock.Call
}

// mustEmbedUnimplementedAggchainProofServiceServer is a helper method to define mock.On call
func (_e *AggchainProofServiceServer_Expecter) mustEmbedUnimplementedAggchainProofServiceServer() *AggchainProofServiceServer_mustEmbedUnimplementedAggchainProofServiceServer_Call {
	return &AggchainProofServiceServer_mustEmbedUnimplementedAggchainProofServiceServer_Call{Call: _e.mock.On("mustEmbedUnimplementedAggchainProofServiceServer")}
}

func (_c *AggchainProofServiceServer_mustEmbedUnimplementedAggchainProofServiceServer_Call) Run(run func()) *AggchainProofServiceServer_mustEmbedUnimplementedAggchainProofServiceServer_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *AggchainProofServiceServer_mustEmbedUnimplementedAggchainProofServiceServer_Call) Return() *AggchainProofServiceServer_mustEmbedUnimplementedAggchainProofServiceServer_Call {
	_c.Call.Return()
	return _c
}

func (_c *AggchainProofServiceServer_mustEmbedUnimplementedAggchainProofServiceServer_Call) RunAndReturn(run func()) *AggchainProofServiceServer_mustEmbedUnimplementedAggchainProofServiceServer_Call {
	_c.Run(run)
	return _c
}

// NewAggchainProofServiceServer creates a new instance of AggchainProofServiceServer. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewAggchainProofServiceServer(t interface {
	mock.TestingT
	Cleanup(func())
}) *AggchainProofServiceServer {
	mock := &AggchainProofServiceServer{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
