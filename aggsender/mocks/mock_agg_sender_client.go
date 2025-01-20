// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	grpc "google.golang.org/grpc"

	mock "github.com/stretchr/testify/mock"

	types "github.com/agglayer/aggkit/aggsender/types"
)

// AggSenderClient is an autogenerated mock type for the AggSenderClient type
type AggSenderClient struct {
	mock.Mock
}

type AggSenderClient_Expecter struct {
	mock *mock.Mock
}

func (_m *AggSenderClient) EXPECT() *AggSenderClient_Expecter {
	return &AggSenderClient_Expecter{mock: &_m.Mock}
}

// ReceiveAuthProof provides a mock function with given fields: ctx, in, opts
func (_m *AggSenderClient) ReceiveAuthProof(ctx context.Context, in *types.ProofRequest, opts ...grpc.CallOption) (*types.ProofResponse, error) {
	_va := make([]interface{}, len(opts))
	for _i := range opts {
		_va[_i] = opts[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, ctx, in)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	if len(ret) == 0 {
		panic("no return value specified for ReceiveAuthProof")
	}

	var r0 *types.ProofResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *types.ProofRequest, ...grpc.CallOption) (*types.ProofResponse, error)); ok {
		return rf(ctx, in, opts...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *types.ProofRequest, ...grpc.CallOption) *types.ProofResponse); ok {
		r0 = rf(ctx, in, opts...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.ProofResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *types.ProofRequest, ...grpc.CallOption) error); ok {
		r1 = rf(ctx, in, opts...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// AggSenderClient_ReceiveAuthProof_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'ReceiveAuthProof'
type AggSenderClient_ReceiveAuthProof_Call struct {
	*mock.Call
}

// ReceiveAuthProof is a helper method to define mock.On call
//   - ctx context.Context
//   - in *types.ProofRequest
//   - opts ...grpc.CallOption
func (_e *AggSenderClient_Expecter) ReceiveAuthProof(ctx interface{}, in interface{}, opts ...interface{}) *AggSenderClient_ReceiveAuthProof_Call {
	return &AggSenderClient_ReceiveAuthProof_Call{Call: _e.mock.On("ReceiveAuthProof",
		append([]interface{}{ctx, in}, opts...)...)}
}

func (_c *AggSenderClient_ReceiveAuthProof_Call) Run(run func(ctx context.Context, in *types.ProofRequest, opts ...grpc.CallOption)) *AggSenderClient_ReceiveAuthProof_Call {
	_c.Call.Run(func(args mock.Arguments) {
		variadicArgs := make([]grpc.CallOption, len(args)-2)
		for i, a := range args[2:] {
			if a != nil {
				variadicArgs[i] = a.(grpc.CallOption)
			}
		}
		run(args[0].(context.Context), args[1].(*types.ProofRequest), variadicArgs...)
	})
	return _c
}

func (_c *AggSenderClient_ReceiveAuthProof_Call) Return(_a0 *types.ProofResponse, _a1 error) *AggSenderClient_ReceiveAuthProof_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *AggSenderClient_ReceiveAuthProof_Call) RunAndReturn(run func(context.Context, *types.ProofRequest, ...grpc.CallOption) (*types.ProofResponse, error)) *AggSenderClient_ReceiveAuthProof_Call {
	_c.Call.Return(run)
	return _c
}

// NewAggSenderClient creates a new instance of AggSenderClient. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewAggSenderClient(t interface {
	mock.TestingT
	Cleanup(func())
}) *AggSenderClient {
	mock := &AggSenderClient{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
