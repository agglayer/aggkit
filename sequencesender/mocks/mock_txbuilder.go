// Code generated by mockery v2.40.1. DO NOT EDIT.

package mocks

import (
	context "context"

	common "github.com/ethereum/go-ethereum/common"

	datastream "github.com/agglayer/aggkit/state/datastream"

	mock "github.com/stretchr/testify/mock"

	seqsendertypes "github.com/agglayer/aggkit/sequencesender/seqsendertypes"

	txbuilder "github.com/agglayer/aggkit/sequencesender/txbuilder"

	types "github.com/ethereum/go-ethereum/core/types"
)

// TxBuilderMock is an autogenerated mock type for the TxBuilder type
type TxBuilderMock struct {
	mock.Mock
}

type TxBuilderMock_Expecter struct {
	mock *mock.Mock
}

func (_m *TxBuilderMock) EXPECT() *TxBuilderMock_Expecter {
	return &TxBuilderMock_Expecter{mock: &_m.Mock}
}

// BuildSequenceBatchesTx provides a mock function with given fields: ctx, sequences
func (_m *TxBuilderMock) BuildSequenceBatchesTx(ctx context.Context, sequences seqsendertypes.Sequence) (*types.Transaction, error) {
	ret := _m.Called(ctx, sequences)

	if len(ret) == 0 {
		panic("no return value specified for BuildSequenceBatchesTx")
	}

	var r0 *types.Transaction
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, seqsendertypes.Sequence) (*types.Transaction, error)); ok {
		return rf(ctx, sequences)
	}
	if rf, ok := ret.Get(0).(func(context.Context, seqsendertypes.Sequence) *types.Transaction); ok {
		r0 = rf(ctx, sequences)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.Transaction)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, seqsendertypes.Sequence) error); ok {
		r1 = rf(ctx, sequences)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// TxBuilderMock_BuildSequenceBatchesTx_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'BuildSequenceBatchesTx'
type TxBuilderMock_BuildSequenceBatchesTx_Call struct {
	*mock.Call
}

// BuildSequenceBatchesTx is a helper method to define mock.On call
//   - ctx context.Context
//   - sequences seqsendertypes.Sequence
func (_e *TxBuilderMock_Expecter) BuildSequenceBatchesTx(ctx interface{}, sequences interface{}) *TxBuilderMock_BuildSequenceBatchesTx_Call {
	return &TxBuilderMock_BuildSequenceBatchesTx_Call{Call: _e.mock.On("BuildSequenceBatchesTx", ctx, sequences)}
}

func (_c *TxBuilderMock_BuildSequenceBatchesTx_Call) Run(run func(ctx context.Context, sequences seqsendertypes.Sequence)) *TxBuilderMock_BuildSequenceBatchesTx_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(seqsendertypes.Sequence))
	})
	return _c
}

func (_c *TxBuilderMock_BuildSequenceBatchesTx_Call) Return(_a0 *types.Transaction, _a1 error) *TxBuilderMock_BuildSequenceBatchesTx_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *TxBuilderMock_BuildSequenceBatchesTx_Call) RunAndReturn(run func(context.Context, seqsendertypes.Sequence) (*types.Transaction, error)) *TxBuilderMock_BuildSequenceBatchesTx_Call {
	_c.Call.Return(run)
	return _c
}

// NewBatchFromL2Block provides a mock function with given fields: l2Block
func (_m *TxBuilderMock) NewBatchFromL2Block(l2Block *datastream.L2Block) seqsendertypes.Batch {
	ret := _m.Called(l2Block)

	if len(ret) == 0 {
		panic("no return value specified for NewBatchFromL2Block")
	}

	var r0 seqsendertypes.Batch
	if rf, ok := ret.Get(0).(func(*datastream.L2Block) seqsendertypes.Batch); ok {
		r0 = rf(l2Block)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(seqsendertypes.Batch)
		}
	}

	return r0
}

// TxBuilderMock_NewBatchFromL2Block_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'NewBatchFromL2Block'
type TxBuilderMock_NewBatchFromL2Block_Call struct {
	*mock.Call
}

// NewBatchFromL2Block is a helper method to define mock.On call
//   - l2Block *datastream.L2Block
func (_e *TxBuilderMock_Expecter) NewBatchFromL2Block(l2Block interface{}) *TxBuilderMock_NewBatchFromL2Block_Call {
	return &TxBuilderMock_NewBatchFromL2Block_Call{Call: _e.mock.On("NewBatchFromL2Block", l2Block)}
}

func (_c *TxBuilderMock_NewBatchFromL2Block_Call) Run(run func(l2Block *datastream.L2Block)) *TxBuilderMock_NewBatchFromL2Block_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(*datastream.L2Block))
	})
	return _c
}

func (_c *TxBuilderMock_NewBatchFromL2Block_Call) Return(_a0 seqsendertypes.Batch) *TxBuilderMock_NewBatchFromL2Block_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *TxBuilderMock_NewBatchFromL2Block_Call) RunAndReturn(run func(*datastream.L2Block) seqsendertypes.Batch) *TxBuilderMock_NewBatchFromL2Block_Call {
	_c.Call.Return(run)
	return _c
}

// NewSequence provides a mock function with given fields: ctx, batches, coinbase
func (_m *TxBuilderMock) NewSequence(ctx context.Context, batches []seqsendertypes.Batch, coinbase common.Address) (seqsendertypes.Sequence, error) {
	ret := _m.Called(ctx, batches, coinbase)

	if len(ret) == 0 {
		panic("no return value specified for NewSequence")
	}

	var r0 seqsendertypes.Sequence
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, []seqsendertypes.Batch, common.Address) (seqsendertypes.Sequence, error)); ok {
		return rf(ctx, batches, coinbase)
	}
	if rf, ok := ret.Get(0).(func(context.Context, []seqsendertypes.Batch, common.Address) seqsendertypes.Sequence); ok {
		r0 = rf(ctx, batches, coinbase)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(seqsendertypes.Sequence)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, []seqsendertypes.Batch, common.Address) error); ok {
		r1 = rf(ctx, batches, coinbase)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// TxBuilderMock_NewSequence_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'NewSequence'
type TxBuilderMock_NewSequence_Call struct {
	*mock.Call
}

// NewSequence is a helper method to define mock.On call
//   - ctx context.Context
//   - batches []seqsendertypes.Batch
//   - coinbase common.Address
func (_e *TxBuilderMock_Expecter) NewSequence(ctx interface{}, batches interface{}, coinbase interface{}) *TxBuilderMock_NewSequence_Call {
	return &TxBuilderMock_NewSequence_Call{Call: _e.mock.On("NewSequence", ctx, batches, coinbase)}
}

func (_c *TxBuilderMock_NewSequence_Call) Run(run func(ctx context.Context, batches []seqsendertypes.Batch, coinbase common.Address)) *TxBuilderMock_NewSequence_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].([]seqsendertypes.Batch), args[2].(common.Address))
	})
	return _c
}

func (_c *TxBuilderMock_NewSequence_Call) Return(_a0 seqsendertypes.Sequence, _a1 error) *TxBuilderMock_NewSequence_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *TxBuilderMock_NewSequence_Call) RunAndReturn(run func(context.Context, []seqsendertypes.Batch, common.Address) (seqsendertypes.Sequence, error)) *TxBuilderMock_NewSequence_Call {
	_c.Call.Return(run)
	return _c
}

// NewSequenceIfWorthToSend provides a mock function with given fields: ctx, sequenceBatches, l2Coinbase, batchNumber
func (_m *TxBuilderMock) NewSequenceIfWorthToSend(ctx context.Context, sequenceBatches []seqsendertypes.Batch, l2Coinbase common.Address, batchNumber uint64) (seqsendertypes.Sequence, error) {
	ret := _m.Called(ctx, sequenceBatches, l2Coinbase, batchNumber)

	if len(ret) == 0 {
		panic("no return value specified for NewSequenceIfWorthToSend")
	}

	var r0 seqsendertypes.Sequence
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, []seqsendertypes.Batch, common.Address, uint64) (seqsendertypes.Sequence, error)); ok {
		return rf(ctx, sequenceBatches, l2Coinbase, batchNumber)
	}
	if rf, ok := ret.Get(0).(func(context.Context, []seqsendertypes.Batch, common.Address, uint64) seqsendertypes.Sequence); ok {
		r0 = rf(ctx, sequenceBatches, l2Coinbase, batchNumber)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(seqsendertypes.Sequence)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, []seqsendertypes.Batch, common.Address, uint64) error); ok {
		r1 = rf(ctx, sequenceBatches, l2Coinbase, batchNumber)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// TxBuilderMock_NewSequenceIfWorthToSend_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'NewSequenceIfWorthToSend'
type TxBuilderMock_NewSequenceIfWorthToSend_Call struct {
	*mock.Call
}

// NewSequenceIfWorthToSend is a helper method to define mock.On call
//   - ctx context.Context
//   - sequenceBatches []seqsendertypes.Batch
//   - l2Coinbase common.Address
//   - batchNumber uint64
func (_e *TxBuilderMock_Expecter) NewSequenceIfWorthToSend(ctx interface{}, sequenceBatches interface{}, l2Coinbase interface{}, batchNumber interface{}) *TxBuilderMock_NewSequenceIfWorthToSend_Call {
	return &TxBuilderMock_NewSequenceIfWorthToSend_Call{Call: _e.mock.On("NewSequenceIfWorthToSend", ctx, sequenceBatches, l2Coinbase, batchNumber)}
}

func (_c *TxBuilderMock_NewSequenceIfWorthToSend_Call) Run(run func(ctx context.Context, sequenceBatches []seqsendertypes.Batch, l2Coinbase common.Address, batchNumber uint64)) *TxBuilderMock_NewSequenceIfWorthToSend_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].([]seqsendertypes.Batch), args[2].(common.Address), args[3].(uint64))
	})
	return _c
}

func (_c *TxBuilderMock_NewSequenceIfWorthToSend_Call) Return(_a0 seqsendertypes.Sequence, _a1 error) *TxBuilderMock_NewSequenceIfWorthToSend_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *TxBuilderMock_NewSequenceIfWorthToSend_Call) RunAndReturn(run func(context.Context, []seqsendertypes.Batch, common.Address, uint64) (seqsendertypes.Sequence, error)) *TxBuilderMock_NewSequenceIfWorthToSend_Call {
	_c.Call.Return(run)
	return _c
}

// SetCondNewSeq provides a mock function with given fields: cond
func (_m *TxBuilderMock) SetCondNewSeq(cond txbuilder.CondNewSequence) txbuilder.CondNewSequence {
	ret := _m.Called(cond)

	if len(ret) == 0 {
		panic("no return value specified for SetCondNewSeq")
	}

	var r0 txbuilder.CondNewSequence
	if rf, ok := ret.Get(0).(func(txbuilder.CondNewSequence) txbuilder.CondNewSequence); ok {
		r0 = rf(cond)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(txbuilder.CondNewSequence)
		}
	}

	return r0
}

// TxBuilderMock_SetCondNewSeq_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'SetCondNewSeq'
type TxBuilderMock_SetCondNewSeq_Call struct {
	*mock.Call
}

// SetCondNewSeq is a helper method to define mock.On call
//   - cond txbuilder.CondNewSequence
func (_e *TxBuilderMock_Expecter) SetCondNewSeq(cond interface{}) *TxBuilderMock_SetCondNewSeq_Call {
	return &TxBuilderMock_SetCondNewSeq_Call{Call: _e.mock.On("SetCondNewSeq", cond)}
}

func (_c *TxBuilderMock_SetCondNewSeq_Call) Run(run func(cond txbuilder.CondNewSequence)) *TxBuilderMock_SetCondNewSeq_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(txbuilder.CondNewSequence))
	})
	return _c
}

func (_c *TxBuilderMock_SetCondNewSeq_Call) Return(_a0 txbuilder.CondNewSequence) *TxBuilderMock_SetCondNewSeq_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *TxBuilderMock_SetCondNewSeq_Call) RunAndReturn(run func(txbuilder.CondNewSequence) txbuilder.CondNewSequence) *TxBuilderMock_SetCondNewSeq_Call {
	_c.Call.Return(run)
	return _c
}

// String provides a mock function with given fields:
func (_m *TxBuilderMock) String() string {
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

// TxBuilderMock_String_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'String'
type TxBuilderMock_String_Call struct {
	*mock.Call
}

// String is a helper method to define mock.On call
func (_e *TxBuilderMock_Expecter) String() *TxBuilderMock_String_Call {
	return &TxBuilderMock_String_Call{Call: _e.mock.On("String")}
}

func (_c *TxBuilderMock_String_Call) Run(run func()) *TxBuilderMock_String_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run()
	})
	return _c
}

func (_c *TxBuilderMock_String_Call) Return(_a0 string) *TxBuilderMock_String_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *TxBuilderMock_String_Call) RunAndReturn(run func() string) *TxBuilderMock_String_Call {
	_c.Call.Return(run)
	return _c
}

// NewTxBuilderMock creates a new instance of TxBuilderMock. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewTxBuilderMock(t interface {
	mock.TestingT
	Cleanup(func())
}) *TxBuilderMock {
	mock := &TxBuilderMock{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
