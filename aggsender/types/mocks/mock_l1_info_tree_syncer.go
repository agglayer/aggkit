// Code generated by mockery v2.50.2. DO NOT EDIT.

package mocks

import (
	context "context"

	common "github.com/ethereum/go-ethereum/common"

	l1infotreesync "github.com/agglayer/aggkit/l1infotreesync"

	mock "github.com/stretchr/testify/mock"

	treetypes "github.com/agglayer/aggkit/tree/types"
)

// MockL1InfoTreeSyncer is an autogenerated mock type for the L1InfoTreeSyncer type
type MockL1InfoTreeSyncer struct {
	mock.Mock
}

type MockL1InfoTreeSyncer_Expecter struct {
	mock *mock.Mock
}

func (_m *MockL1InfoTreeSyncer) EXPECT() *MockL1InfoTreeSyncer_Expecter {
	return &MockL1InfoTreeSyncer_Expecter{mock: &_m.Mock}
}

// GetInfoByGlobalExitRoot provides a mock function with given fields: globalExitRoot
func (_m *MockL1InfoTreeSyncer) GetInfoByGlobalExitRoot(globalExitRoot common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error) {
	ret := _m.Called(globalExitRoot)

	if len(ret) == 0 {
		panic("no return value specified for GetInfoByGlobalExitRoot")
	}

	var r0 *l1infotreesync.L1InfoTreeLeaf
	var r1 error
	if rf, ok := ret.Get(0).(func(common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error)); ok {
		return rf(globalExitRoot)
	}
	if rf, ok := ret.Get(0).(func(common.Hash) *l1infotreesync.L1InfoTreeLeaf); ok {
		r0 = rf(globalExitRoot)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*l1infotreesync.L1InfoTreeLeaf)
		}
	}

	if rf, ok := ret.Get(1).(func(common.Hash) error); ok {
		r1 = rf(globalExitRoot)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockL1InfoTreeSyncer_GetInfoByGlobalExitRoot_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetInfoByGlobalExitRoot'
type MockL1InfoTreeSyncer_GetInfoByGlobalExitRoot_Call struct {
	*mock.Call
}

// GetInfoByGlobalExitRoot is a helper method to define mock.On call
//   - globalExitRoot common.Hash
func (_e *MockL1InfoTreeSyncer_Expecter) GetInfoByGlobalExitRoot(globalExitRoot interface{}) *MockL1InfoTreeSyncer_GetInfoByGlobalExitRoot_Call {
	return &MockL1InfoTreeSyncer_GetInfoByGlobalExitRoot_Call{Call: _e.mock.On("GetInfoByGlobalExitRoot", globalExitRoot)}
}

func (_c *MockL1InfoTreeSyncer_GetInfoByGlobalExitRoot_Call) Run(run func(globalExitRoot common.Hash)) *MockL1InfoTreeSyncer_GetInfoByGlobalExitRoot_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(common.Hash))
	})
	return _c
}

func (_c *MockL1InfoTreeSyncer_GetInfoByGlobalExitRoot_Call) Return(_a0 *l1infotreesync.L1InfoTreeLeaf, _a1 error) *MockL1InfoTreeSyncer_GetInfoByGlobalExitRoot_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockL1InfoTreeSyncer_GetInfoByGlobalExitRoot_Call) RunAndReturn(run func(common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error)) *MockL1InfoTreeSyncer_GetInfoByGlobalExitRoot_Call {
	_c.Call.Return(run)
	return _c
}

// GetL1InfoTreeMerkleProofFromIndexToRoot provides a mock function with given fields: ctx, index, root
func (_m *MockL1InfoTreeSyncer) GetL1InfoTreeMerkleProofFromIndexToRoot(ctx context.Context, index uint32, root common.Hash) (treetypes.Proof, error) {
	ret := _m.Called(ctx, index, root)

	if len(ret) == 0 {
		panic("no return value specified for GetL1InfoTreeMerkleProofFromIndexToRoot")
	}

	var r0 treetypes.Proof
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, uint32, common.Hash) (treetypes.Proof, error)); ok {
		return rf(ctx, index, root)
	}
	if rf, ok := ret.Get(0).(func(context.Context, uint32, common.Hash) treetypes.Proof); ok {
		r0 = rf(ctx, index, root)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(treetypes.Proof)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, uint32, common.Hash) error); ok {
		r1 = rf(ctx, index, root)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockL1InfoTreeSyncer_GetL1InfoTreeMerkleProofFromIndexToRoot_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetL1InfoTreeMerkleProofFromIndexToRoot'
type MockL1InfoTreeSyncer_GetL1InfoTreeMerkleProofFromIndexToRoot_Call struct {
	*mock.Call
}

// GetL1InfoTreeMerkleProofFromIndexToRoot is a helper method to define mock.On call
//   - ctx context.Context
//   - index uint32
//   - root common.Hash
func (_e *MockL1InfoTreeSyncer_Expecter) GetL1InfoTreeMerkleProofFromIndexToRoot(ctx interface{}, index interface{}, root interface{}) *MockL1InfoTreeSyncer_GetL1InfoTreeMerkleProofFromIndexToRoot_Call {
	return &MockL1InfoTreeSyncer_GetL1InfoTreeMerkleProofFromIndexToRoot_Call{Call: _e.mock.On("GetL1InfoTreeMerkleProofFromIndexToRoot", ctx, index, root)}
}

func (_c *MockL1InfoTreeSyncer_GetL1InfoTreeMerkleProofFromIndexToRoot_Call) Run(run func(ctx context.Context, index uint32, root common.Hash)) *MockL1InfoTreeSyncer_GetL1InfoTreeMerkleProofFromIndexToRoot_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint32), args[2].(common.Hash))
	})
	return _c
}

func (_c *MockL1InfoTreeSyncer_GetL1InfoTreeMerkleProofFromIndexToRoot_Call) Return(_a0 treetypes.Proof, _a1 error) *MockL1InfoTreeSyncer_GetL1InfoTreeMerkleProofFromIndexToRoot_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockL1InfoTreeSyncer_GetL1InfoTreeMerkleProofFromIndexToRoot_Call) RunAndReturn(run func(context.Context, uint32, common.Hash) (treetypes.Proof, error)) *MockL1InfoTreeSyncer_GetL1InfoTreeMerkleProofFromIndexToRoot_Call {
	_c.Call.Return(run)
	return _c
}

// GetL1InfoTreeRootByIndex provides a mock function with given fields: ctx, index
func (_m *MockL1InfoTreeSyncer) GetL1InfoTreeRootByIndex(ctx context.Context, index uint32) (treetypes.Root, error) {
	ret := _m.Called(ctx, index)

	if len(ret) == 0 {
		panic("no return value specified for GetL1InfoTreeRootByIndex")
	}

	var r0 treetypes.Root
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, uint32) (treetypes.Root, error)); ok {
		return rf(ctx, index)
	}
	if rf, ok := ret.Get(0).(func(context.Context, uint32) treetypes.Root); ok {
		r0 = rf(ctx, index)
	} else {
		r0 = ret.Get(0).(treetypes.Root)
	}

	if rf, ok := ret.Get(1).(func(context.Context, uint32) error); ok {
		r1 = rf(ctx, index)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// MockL1InfoTreeSyncer_GetL1InfoTreeRootByIndex_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetL1InfoTreeRootByIndex'
type MockL1InfoTreeSyncer_GetL1InfoTreeRootByIndex_Call struct {
	*mock.Call
}

// GetL1InfoTreeRootByIndex is a helper method to define mock.On call
//   - ctx context.Context
//   - index uint32
func (_e *MockL1InfoTreeSyncer_Expecter) GetL1InfoTreeRootByIndex(ctx interface{}, index interface{}) *MockL1InfoTreeSyncer_GetL1InfoTreeRootByIndex_Call {
	return &MockL1InfoTreeSyncer_GetL1InfoTreeRootByIndex_Call{Call: _e.mock.On("GetL1InfoTreeRootByIndex", ctx, index)}
}

func (_c *MockL1InfoTreeSyncer_GetL1InfoTreeRootByIndex_Call) Run(run func(ctx context.Context, index uint32)) *MockL1InfoTreeSyncer_GetL1InfoTreeRootByIndex_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint32))
	})
	return _c
}

func (_c *MockL1InfoTreeSyncer_GetL1InfoTreeRootByIndex_Call) Return(_a0 treetypes.Root, _a1 error) *MockL1InfoTreeSyncer_GetL1InfoTreeRootByIndex_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *MockL1InfoTreeSyncer_GetL1InfoTreeRootByIndex_Call) RunAndReturn(run func(context.Context, uint32) (treetypes.Root, error)) *MockL1InfoTreeSyncer_GetL1InfoTreeRootByIndex_Call {
	_c.Call.Return(run)
	return _c
}

// NewMockL1InfoTreeSyncer creates a new instance of MockL1InfoTreeSyncer. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewMockL1InfoTreeSyncer(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockL1InfoTreeSyncer {
	mock := &MockL1InfoTreeSyncer{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
