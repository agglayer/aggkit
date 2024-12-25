// Code generated by mockery. DO NOT EDIT.

package mocks

import (
	context "context"

	db "github.com/agglayer/aggkit/db"
	mock "github.com/stretchr/testify/mock"

	sql "database/sql"

	state "github.com/agglayer/aggkit/state"
)

// StorageInterfaceMock is an autogenerated mock type for the StorageInterface type
type StorageInterfaceMock struct {
	mock.Mock
}

type StorageInterfaceMock_Expecter struct {
	mock *mock.Mock
}

func (_m *StorageInterfaceMock) EXPECT() *StorageInterfaceMock_Expecter {
	return &StorageInterfaceMock_Expecter{mock: &_m.Mock}
}

// AddGeneratedProof provides a mock function with given fields: ctx, proof, dbTx
func (_m *StorageInterfaceMock) AddGeneratedProof(ctx context.Context, proof *state.Proof, dbTx db.Txer) error {
	ret := _m.Called(ctx, proof, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for AddGeneratedProof")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *state.Proof, db.Txer) error); ok {
		r0 = rf(ctx, proof, dbTx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// StorageInterfaceMock_AddGeneratedProof_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'AddGeneratedProof'
type StorageInterfaceMock_AddGeneratedProof_Call struct {
	*mock.Call
}

// AddGeneratedProof is a helper method to define mock.On call
//   - ctx context.Context
//   - proof *state.Proof
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) AddGeneratedProof(ctx interface{}, proof interface{}, dbTx interface{}) *StorageInterfaceMock_AddGeneratedProof_Call {
	return &StorageInterfaceMock_AddGeneratedProof_Call{Call: _e.mock.On("AddGeneratedProof", ctx, proof, dbTx)}
}

func (_c *StorageInterfaceMock_AddGeneratedProof_Call) Run(run func(ctx context.Context, proof *state.Proof, dbTx db.Txer)) *StorageInterfaceMock_AddGeneratedProof_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*state.Proof), args[2].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_AddGeneratedProof_Call) Return(_a0 error) *StorageInterfaceMock_AddGeneratedProof_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *StorageInterfaceMock_AddGeneratedProof_Call) RunAndReturn(run func(context.Context, *state.Proof, db.Txer) error) *StorageInterfaceMock_AddGeneratedProof_Call {
	_c.Call.Return(run)
	return _c
}

// AddSequence provides a mock function with given fields: ctx, sequence, dbTx
func (_m *StorageInterfaceMock) AddSequence(ctx context.Context, sequence state.Sequence, dbTx db.Txer) error {
	ret := _m.Called(ctx, sequence, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for AddSequence")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, state.Sequence, db.Txer) error); ok {
		r0 = rf(ctx, sequence, dbTx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// StorageInterfaceMock_AddSequence_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'AddSequence'
type StorageInterfaceMock_AddSequence_Call struct {
	*mock.Call
}

// AddSequence is a helper method to define mock.On call
//   - ctx context.Context
//   - sequence state.Sequence
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) AddSequence(ctx interface{}, sequence interface{}, dbTx interface{}) *StorageInterfaceMock_AddSequence_Call {
	return &StorageInterfaceMock_AddSequence_Call{Call: _e.mock.On("AddSequence", ctx, sequence, dbTx)}
}

func (_c *StorageInterfaceMock_AddSequence_Call) Run(run func(ctx context.Context, sequence state.Sequence, dbTx db.Txer)) *StorageInterfaceMock_AddSequence_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(state.Sequence), args[2].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_AddSequence_Call) Return(_a0 error) *StorageInterfaceMock_AddSequence_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *StorageInterfaceMock_AddSequence_Call) RunAndReturn(run func(context.Context, state.Sequence, db.Txer) error) *StorageInterfaceMock_AddSequence_Call {
	_c.Call.Return(run)
	return _c
}

// BeginTx provides a mock function with given fields: ctx, options
func (_m *StorageInterfaceMock) BeginTx(ctx context.Context, options *sql.TxOptions) (db.Txer, error) {
	ret := _m.Called(ctx, options)

	if len(ret) == 0 {
		panic("no return value specified for BeginTx")
	}

	var r0 db.Txer
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *sql.TxOptions) (db.Txer, error)); ok {
		return rf(ctx, options)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *sql.TxOptions) db.Txer); ok {
		r0 = rf(ctx, options)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(db.Txer)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *sql.TxOptions) error); ok {
		r1 = rf(ctx, options)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// StorageInterfaceMock_BeginTx_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'BeginTx'
type StorageInterfaceMock_BeginTx_Call struct {
	*mock.Call
}

// BeginTx is a helper method to define mock.On call
//   - ctx context.Context
//   - options *sql.TxOptions
func (_e *StorageInterfaceMock_Expecter) BeginTx(ctx interface{}, options interface{}) *StorageInterfaceMock_BeginTx_Call {
	return &StorageInterfaceMock_BeginTx_Call{Call: _e.mock.On("BeginTx", ctx, options)}
}

func (_c *StorageInterfaceMock_BeginTx_Call) Run(run func(ctx context.Context, options *sql.TxOptions)) *StorageInterfaceMock_BeginTx_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*sql.TxOptions))
	})
	return _c
}

func (_c *StorageInterfaceMock_BeginTx_Call) Return(_a0 db.Txer, _a1 error) *StorageInterfaceMock_BeginTx_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *StorageInterfaceMock_BeginTx_Call) RunAndReturn(run func(context.Context, *sql.TxOptions) (db.Txer, error)) *StorageInterfaceMock_BeginTx_Call {
	_c.Call.Return(run)
	return _c
}

// CheckProofContainsCompleteSequences provides a mock function with given fields: ctx, proof, dbTx
func (_m *StorageInterfaceMock) CheckProofContainsCompleteSequences(ctx context.Context, proof *state.Proof, dbTx db.Txer) (bool, error) {
	ret := _m.Called(ctx, proof, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for CheckProofContainsCompleteSequences")
	}

	var r0 bool
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *state.Proof, db.Txer) (bool, error)); ok {
		return rf(ctx, proof, dbTx)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *state.Proof, db.Txer) bool); ok {
		r0 = rf(ctx, proof, dbTx)
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func(context.Context, *state.Proof, db.Txer) error); ok {
		r1 = rf(ctx, proof, dbTx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// StorageInterfaceMock_CheckProofContainsCompleteSequences_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'CheckProofContainsCompleteSequences'
type StorageInterfaceMock_CheckProofContainsCompleteSequences_Call struct {
	*mock.Call
}

// CheckProofContainsCompleteSequences is a helper method to define mock.On call
//   - ctx context.Context
//   - proof *state.Proof
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) CheckProofContainsCompleteSequences(ctx interface{}, proof interface{}, dbTx interface{}) *StorageInterfaceMock_CheckProofContainsCompleteSequences_Call {
	return &StorageInterfaceMock_CheckProofContainsCompleteSequences_Call{Call: _e.mock.On("CheckProofContainsCompleteSequences", ctx, proof, dbTx)}
}

func (_c *StorageInterfaceMock_CheckProofContainsCompleteSequences_Call) Run(run func(ctx context.Context, proof *state.Proof, dbTx db.Txer)) *StorageInterfaceMock_CheckProofContainsCompleteSequences_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*state.Proof), args[2].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_CheckProofContainsCompleteSequences_Call) Return(_a0 bool, _a1 error) *StorageInterfaceMock_CheckProofContainsCompleteSequences_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *StorageInterfaceMock_CheckProofContainsCompleteSequences_Call) RunAndReturn(run func(context.Context, *state.Proof, db.Txer) (bool, error)) *StorageInterfaceMock_CheckProofContainsCompleteSequences_Call {
	_c.Call.Return(run)
	return _c
}

// CheckProofExistsForBatch provides a mock function with given fields: ctx, batchNumber, dbTx
func (_m *StorageInterfaceMock) CheckProofExistsForBatch(ctx context.Context, batchNumber uint64, dbTx db.Txer) (bool, error) {
	ret := _m.Called(ctx, batchNumber, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for CheckProofExistsForBatch")
	}

	var r0 bool
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, uint64, db.Txer) (bool, error)); ok {
		return rf(ctx, batchNumber, dbTx)
	}
	if rf, ok := ret.Get(0).(func(context.Context, uint64, db.Txer) bool); ok {
		r0 = rf(ctx, batchNumber, dbTx)
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func(context.Context, uint64, db.Txer) error); ok {
		r1 = rf(ctx, batchNumber, dbTx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// StorageInterfaceMock_CheckProofExistsForBatch_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'CheckProofExistsForBatch'
type StorageInterfaceMock_CheckProofExistsForBatch_Call struct {
	*mock.Call
}

// CheckProofExistsForBatch is a helper method to define mock.On call
//   - ctx context.Context
//   - batchNumber uint64
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) CheckProofExistsForBatch(ctx interface{}, batchNumber interface{}, dbTx interface{}) *StorageInterfaceMock_CheckProofExistsForBatch_Call {
	return &StorageInterfaceMock_CheckProofExistsForBatch_Call{Call: _e.mock.On("CheckProofExistsForBatch", ctx, batchNumber, dbTx)}
}

func (_c *StorageInterfaceMock_CheckProofExistsForBatch_Call) Run(run func(ctx context.Context, batchNumber uint64, dbTx db.Txer)) *StorageInterfaceMock_CheckProofExistsForBatch_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint64), args[2].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_CheckProofExistsForBatch_Call) Return(_a0 bool, _a1 error) *StorageInterfaceMock_CheckProofExistsForBatch_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *StorageInterfaceMock_CheckProofExistsForBatch_Call) RunAndReturn(run func(context.Context, uint64, db.Txer) (bool, error)) *StorageInterfaceMock_CheckProofExistsForBatch_Call {
	_c.Call.Return(run)
	return _c
}

// CleanupGeneratedProofs provides a mock function with given fields: ctx, batchNumber, dbTx
func (_m *StorageInterfaceMock) CleanupGeneratedProofs(ctx context.Context, batchNumber uint64, dbTx db.Txer) error {
	ret := _m.Called(ctx, batchNumber, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for CleanupGeneratedProofs")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, uint64, db.Txer) error); ok {
		r0 = rf(ctx, batchNumber, dbTx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// StorageInterfaceMock_CleanupGeneratedProofs_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'CleanupGeneratedProofs'
type StorageInterfaceMock_CleanupGeneratedProofs_Call struct {
	*mock.Call
}

// CleanupGeneratedProofs is a helper method to define mock.On call
//   - ctx context.Context
//   - batchNumber uint64
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) CleanupGeneratedProofs(ctx interface{}, batchNumber interface{}, dbTx interface{}) *StorageInterfaceMock_CleanupGeneratedProofs_Call {
	return &StorageInterfaceMock_CleanupGeneratedProofs_Call{Call: _e.mock.On("CleanupGeneratedProofs", ctx, batchNumber, dbTx)}
}

func (_c *StorageInterfaceMock_CleanupGeneratedProofs_Call) Run(run func(ctx context.Context, batchNumber uint64, dbTx db.Txer)) *StorageInterfaceMock_CleanupGeneratedProofs_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint64), args[2].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_CleanupGeneratedProofs_Call) Return(_a0 error) *StorageInterfaceMock_CleanupGeneratedProofs_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *StorageInterfaceMock_CleanupGeneratedProofs_Call) RunAndReturn(run func(context.Context, uint64, db.Txer) error) *StorageInterfaceMock_CleanupGeneratedProofs_Call {
	_c.Call.Return(run)
	return _c
}

// CleanupLockedProofs provides a mock function with given fields: ctx, duration, dbTx
func (_m *StorageInterfaceMock) CleanupLockedProofs(ctx context.Context, duration string, dbTx db.Txer) (int64, error) {
	ret := _m.Called(ctx, duration, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for CleanupLockedProofs")
	}

	var r0 int64
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, db.Txer) (int64, error)); ok {
		return rf(ctx, duration, dbTx)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, db.Txer) int64); ok {
		r0 = rf(ctx, duration, dbTx)
	} else {
		r0 = ret.Get(0).(int64)
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, db.Txer) error); ok {
		r1 = rf(ctx, duration, dbTx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// StorageInterfaceMock_CleanupLockedProofs_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'CleanupLockedProofs'
type StorageInterfaceMock_CleanupLockedProofs_Call struct {
	*mock.Call
}

// CleanupLockedProofs is a helper method to define mock.On call
//   - ctx context.Context
//   - duration string
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) CleanupLockedProofs(ctx interface{}, duration interface{}, dbTx interface{}) *StorageInterfaceMock_CleanupLockedProofs_Call {
	return &StorageInterfaceMock_CleanupLockedProofs_Call{Call: _e.mock.On("CleanupLockedProofs", ctx, duration, dbTx)}
}

func (_c *StorageInterfaceMock_CleanupLockedProofs_Call) Run(run func(ctx context.Context, duration string, dbTx db.Txer)) *StorageInterfaceMock_CleanupLockedProofs_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(string), args[2].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_CleanupLockedProofs_Call) Return(_a0 int64, _a1 error) *StorageInterfaceMock_CleanupLockedProofs_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *StorageInterfaceMock_CleanupLockedProofs_Call) RunAndReturn(run func(context.Context, string, db.Txer) (int64, error)) *StorageInterfaceMock_CleanupLockedProofs_Call {
	_c.Call.Return(run)
	return _c
}

// DeleteGeneratedProofs provides a mock function with given fields: ctx, batchNumber, batchNumberFinal, dbTx
func (_m *StorageInterfaceMock) DeleteGeneratedProofs(ctx context.Context, batchNumber uint64, batchNumberFinal uint64, dbTx db.Txer) error {
	ret := _m.Called(ctx, batchNumber, batchNumberFinal, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for DeleteGeneratedProofs")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, uint64, uint64, db.Txer) error); ok {
		r0 = rf(ctx, batchNumber, batchNumberFinal, dbTx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// StorageInterfaceMock_DeleteGeneratedProofs_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'DeleteGeneratedProofs'
type StorageInterfaceMock_DeleteGeneratedProofs_Call struct {
	*mock.Call
}

// DeleteGeneratedProofs is a helper method to define mock.On call
//   - ctx context.Context
//   - batchNumber uint64
//   - batchNumberFinal uint64
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) DeleteGeneratedProofs(ctx interface{}, batchNumber interface{}, batchNumberFinal interface{}, dbTx interface{}) *StorageInterfaceMock_DeleteGeneratedProofs_Call {
	return &StorageInterfaceMock_DeleteGeneratedProofs_Call{Call: _e.mock.On("DeleteGeneratedProofs", ctx, batchNumber, batchNumberFinal, dbTx)}
}

func (_c *StorageInterfaceMock_DeleteGeneratedProofs_Call) Run(run func(ctx context.Context, batchNumber uint64, batchNumberFinal uint64, dbTx db.Txer)) *StorageInterfaceMock_DeleteGeneratedProofs_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint64), args[2].(uint64), args[3].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_DeleteGeneratedProofs_Call) Return(_a0 error) *StorageInterfaceMock_DeleteGeneratedProofs_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *StorageInterfaceMock_DeleteGeneratedProofs_Call) RunAndReturn(run func(context.Context, uint64, uint64, db.Txer) error) *StorageInterfaceMock_DeleteGeneratedProofs_Call {
	_c.Call.Return(run)
	return _c
}

// DeleteUngeneratedProofs provides a mock function with given fields: ctx, dbTx
func (_m *StorageInterfaceMock) DeleteUngeneratedProofs(ctx context.Context, dbTx db.Txer) error {
	ret := _m.Called(ctx, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for DeleteUngeneratedProofs")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, db.Txer) error); ok {
		r0 = rf(ctx, dbTx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// StorageInterfaceMock_DeleteUngeneratedProofs_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'DeleteUngeneratedProofs'
type StorageInterfaceMock_DeleteUngeneratedProofs_Call struct {
	*mock.Call
}

// DeleteUngeneratedProofs is a helper method to define mock.On call
//   - ctx context.Context
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) DeleteUngeneratedProofs(ctx interface{}, dbTx interface{}) *StorageInterfaceMock_DeleteUngeneratedProofs_Call {
	return &StorageInterfaceMock_DeleteUngeneratedProofs_Call{Call: _e.mock.On("DeleteUngeneratedProofs", ctx, dbTx)}
}

func (_c *StorageInterfaceMock_DeleteUngeneratedProofs_Call) Run(run func(ctx context.Context, dbTx db.Txer)) *StorageInterfaceMock_DeleteUngeneratedProofs_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_DeleteUngeneratedProofs_Call) Return(_a0 error) *StorageInterfaceMock_DeleteUngeneratedProofs_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *StorageInterfaceMock_DeleteUngeneratedProofs_Call) RunAndReturn(run func(context.Context, db.Txer) error) *StorageInterfaceMock_DeleteUngeneratedProofs_Call {
	_c.Call.Return(run)
	return _c
}

// GetProofReadyToVerify provides a mock function with given fields: ctx, lastVerfiedBatchNumber, dbTx
func (_m *StorageInterfaceMock) GetProofReadyToVerify(ctx context.Context, lastVerfiedBatchNumber uint64, dbTx db.Txer) (*state.Proof, error) {
	ret := _m.Called(ctx, lastVerfiedBatchNumber, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for GetProofReadyToVerify")
	}

	var r0 *state.Proof
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, uint64, db.Txer) (*state.Proof, error)); ok {
		return rf(ctx, lastVerfiedBatchNumber, dbTx)
	}
	if rf, ok := ret.Get(0).(func(context.Context, uint64, db.Txer) *state.Proof); ok {
		r0 = rf(ctx, lastVerfiedBatchNumber, dbTx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*state.Proof)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, uint64, db.Txer) error); ok {
		r1 = rf(ctx, lastVerfiedBatchNumber, dbTx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// StorageInterfaceMock_GetProofReadyToVerify_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetProofReadyToVerify'
type StorageInterfaceMock_GetProofReadyToVerify_Call struct {
	*mock.Call
}

// GetProofReadyToVerify is a helper method to define mock.On call
//   - ctx context.Context
//   - lastVerfiedBatchNumber uint64
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) GetProofReadyToVerify(ctx interface{}, lastVerfiedBatchNumber interface{}, dbTx interface{}) *StorageInterfaceMock_GetProofReadyToVerify_Call {
	return &StorageInterfaceMock_GetProofReadyToVerify_Call{Call: _e.mock.On("GetProofReadyToVerify", ctx, lastVerfiedBatchNumber, dbTx)}
}

func (_c *StorageInterfaceMock_GetProofReadyToVerify_Call) Run(run func(ctx context.Context, lastVerfiedBatchNumber uint64, dbTx db.Txer)) *StorageInterfaceMock_GetProofReadyToVerify_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(uint64), args[2].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_GetProofReadyToVerify_Call) Return(_a0 *state.Proof, _a1 error) *StorageInterfaceMock_GetProofReadyToVerify_Call {
	_c.Call.Return(_a0, _a1)
	return _c
}

func (_c *StorageInterfaceMock_GetProofReadyToVerify_Call) RunAndReturn(run func(context.Context, uint64, db.Txer) (*state.Proof, error)) *StorageInterfaceMock_GetProofReadyToVerify_Call {
	_c.Call.Return(run)
	return _c
}

// GetProofsToAggregate provides a mock function with given fields: ctx, dbTx
func (_m *StorageInterfaceMock) GetProofsToAggregate(ctx context.Context, dbTx db.Txer) (*state.Proof, *state.Proof, error) {
	ret := _m.Called(ctx, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for GetProofsToAggregate")
	}

	var r0 *state.Proof
	var r1 *state.Proof
	var r2 error
	if rf, ok := ret.Get(0).(func(context.Context, db.Txer) (*state.Proof, *state.Proof, error)); ok {
		return rf(ctx, dbTx)
	}
	if rf, ok := ret.Get(0).(func(context.Context, db.Txer) *state.Proof); ok {
		r0 = rf(ctx, dbTx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*state.Proof)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, db.Txer) *state.Proof); ok {
		r1 = rf(ctx, dbTx)
	} else {
		if ret.Get(1) != nil {
			r1 = ret.Get(1).(*state.Proof)
		}
	}

	if rf, ok := ret.Get(2).(func(context.Context, db.Txer) error); ok {
		r2 = rf(ctx, dbTx)
	} else {
		r2 = ret.Error(2)
	}

	return r0, r1, r2
}

// StorageInterfaceMock_GetProofsToAggregate_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'GetProofsToAggregate'
type StorageInterfaceMock_GetProofsToAggregate_Call struct {
	*mock.Call
}

// GetProofsToAggregate is a helper method to define mock.On call
//   - ctx context.Context
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) GetProofsToAggregate(ctx interface{}, dbTx interface{}) *StorageInterfaceMock_GetProofsToAggregate_Call {
	return &StorageInterfaceMock_GetProofsToAggregate_Call{Call: _e.mock.On("GetProofsToAggregate", ctx, dbTx)}
}

func (_c *StorageInterfaceMock_GetProofsToAggregate_Call) Run(run func(ctx context.Context, dbTx db.Txer)) *StorageInterfaceMock_GetProofsToAggregate_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_GetProofsToAggregate_Call) Return(_a0 *state.Proof, _a1 *state.Proof, _a2 error) *StorageInterfaceMock_GetProofsToAggregate_Call {
	_c.Call.Return(_a0, _a1, _a2)
	return _c
}

func (_c *StorageInterfaceMock_GetProofsToAggregate_Call) RunAndReturn(run func(context.Context, db.Txer) (*state.Proof, *state.Proof, error)) *StorageInterfaceMock_GetProofsToAggregate_Call {
	_c.Call.Return(run)
	return _c
}

// UpdateGeneratedProof provides a mock function with given fields: ctx, proof, dbTx
func (_m *StorageInterfaceMock) UpdateGeneratedProof(ctx context.Context, proof *state.Proof, dbTx db.Txer) error {
	ret := _m.Called(ctx, proof, dbTx)

	if len(ret) == 0 {
		panic("no return value specified for UpdateGeneratedProof")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *state.Proof, db.Txer) error); ok {
		r0 = rf(ctx, proof, dbTx)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// StorageInterfaceMock_UpdateGeneratedProof_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'UpdateGeneratedProof'
type StorageInterfaceMock_UpdateGeneratedProof_Call struct {
	*mock.Call
}

// UpdateGeneratedProof is a helper method to define mock.On call
//   - ctx context.Context
//   - proof *state.Proof
//   - dbTx db.Txer
func (_e *StorageInterfaceMock_Expecter) UpdateGeneratedProof(ctx interface{}, proof interface{}, dbTx interface{}) *StorageInterfaceMock_UpdateGeneratedProof_Call {
	return &StorageInterfaceMock_UpdateGeneratedProof_Call{Call: _e.mock.On("UpdateGeneratedProof", ctx, proof, dbTx)}
}

func (_c *StorageInterfaceMock_UpdateGeneratedProof_Call) Run(run func(ctx context.Context, proof *state.Proof, dbTx db.Txer)) *StorageInterfaceMock_UpdateGeneratedProof_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(context.Context), args[1].(*state.Proof), args[2].(db.Txer))
	})
	return _c
}

func (_c *StorageInterfaceMock_UpdateGeneratedProof_Call) Return(_a0 error) *StorageInterfaceMock_UpdateGeneratedProof_Call {
	_c.Call.Return(_a0)
	return _c
}

func (_c *StorageInterfaceMock_UpdateGeneratedProof_Call) RunAndReturn(run func(context.Context, *state.Proof, db.Txer) error) *StorageInterfaceMock_UpdateGeneratedProof_Call {
	_c.Call.Return(run)
	return _c
}

// NewStorageInterfaceMock creates a new instance of StorageInterfaceMock. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewStorageInterfaceMock(t interface {
	mock.TestingT
	Cleanup(func())
}) *StorageInterfaceMock {
	mock := &StorageInterfaceMock{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
