package submitter_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"

	"github.com/agglayer/aggkit/dvnworker/bindings"
	"github.com/agglayer/aggkit/dvnworker/proofbuilder"
	"github.com/agglayer/aggkit/dvnworker/submitter"
	"github.com/agglayer/aggkit/log"
)

// ─── mock coordinator ─────────────────────────────────────────────────────────

// mockCoordinator implements submitter.CoordinatorCaller.
// The fn field controls what ClaimAndVerify returns for each call.
type mockCoordinator struct {
	fn func() (*types.Transaction, error)
}

func (m *mockCoordinator) ClaimAndVerify(
	_ *bind.TransactOpts,
	_ bindings.Origin,
	_ [32]byte,
	_ []byte,
	_ bindings.AggLayerClaim,
	_ []byte,
	_ [32]byte,
	_ uint64,
) (*types.Transaction, error) {
	return m.fn()
}

// ─── mock receipt waiter ──────────────────────────────────────────────────────

// mockWaiter implements submitter.ReceiptWaiter.
type mockWaiter struct {
	receipt *types.Receipt
	err     error
}

func (m *mockWaiter) WaitMined(_ context.Context, _ *types.Transaction) (*types.Receipt, error) {
	return m.receipt, m.err
}

// ─── alreadyProcessedErr ─────────────────────────────────────────────────────

// alreadyProcessedErr simulates the go-ethereum DataError wrapping for
// AlreadyProcessed(bytes32) — selector 0x1a20d3e6 followed by a padded bytes32.
type alreadyProcessedErr struct {
	data []byte
}

func newAlreadyProcessedErr() *alreadyProcessedErr {
	selector, _ := hex.DecodeString("1a20d3e6")
	// Append a zero-padded bytes32 as the releaseKey argument.
	payload := make([]byte, 36)
	copy(payload, selector)
	return &alreadyProcessedErr{data: payload}
}

func (e *alreadyProcessedErr) Error() string {
	return fmt.Sprintf("execution reverted (0x%x)", e.data)
}

func (e *alreadyProcessedErr) ErrorData() interface{} {
	return e.data
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func dummyTx() *types.Transaction {
	return types.NewTx(&types.LegacyTx{
		Nonce:    1,
		GasPrice: big.NewInt(1),
		Gas:      21000,
		To:       nil,
		Value:    big.NewInt(0),
	})
}

func successWaiter() *mockWaiter {
	return &mockWaiter{
		receipt: &types.Receipt{Status: types.ReceiptStatusSuccessful},
	}
}

func newTestJob() submitter.Job {
	return submitter.Job{
		Origin: submitter.Origin{
			SrcEid: 101,
			Sender: [32]byte{0x01},
			Nonce:  42,
		},
		GUID:          [32]byte{0xde, 0xad},
		Message:       []byte("test-message"),
		PayloadHash:   [32]byte{0xca, 0xfe},
		PacketHeader:  make([]byte, 81),
		Confirmations: 15,
		Claim: proofbuilder.AggLayerClaim{
			SMTProofLocalExitRoot:  [32][32]byte{},
			SMTProofRollupExitRoot: [32][32]byte{},
			GlobalIndex:            big.NewInt(1),
			MainnetExitRoot:        [32]byte{},
			RollupExitRoot:         [32]byte{},
			OriginNetwork:          1,
			DestinationNetwork:     2,
			Amount:                 big.NewInt(1000),
			Metadata:               []byte{},
		},
	}
}

func newSubmitter(coord submitter.CoordinatorCaller, waiter submitter.ReceiptWaiter) *submitter.Submitter {
	opts := &bind.TransactOpts{
		From:  [20]byte{},
		Nonce: big.NewInt(0),
	}
	// Use a very short retry delay in tests to keep test runtime fast.
	return submitter.New(coord, opts, waiter, log.GetDefaultLogger()).WithRetryDelay(time.Millisecond)
}

// ─── tests ────────────────────────────────────────────────────────────────────

// TestSubmit_HappyPath verifies that a successful send + mined receipt returns nil.
func TestSubmit_HappyPath(t *testing.T) {
	coord := &mockCoordinator{fn: func() (*types.Transaction, error) {
		return dummyTx(), nil
	}}
	w := successWaiter()

	s := newSubmitter(coord, w)
	err := s.Submit(context.Background(), newTestJob())
	require.NoError(t, err)
}

// TestSubmit_IdempotentRevert verifies that an AlreadyProcessed revert is treated as success.
func TestSubmit_IdempotentRevert(t *testing.T) {
	coord := &mockCoordinator{fn: func() (*types.Transaction, error) {
		return nil, newAlreadyProcessedErr()
	}}

	s := newSubmitter(coord, nil) // no waiter needed — error is surfaced before broadcast
	err := s.Submit(context.Background(), newTestJob())
	require.NoError(t, err, "AlreadyProcessed should be treated as idempotent success")
}

// TestSubmit_HardRevert verifies that a non-idempotent error is propagated.
func TestSubmit_HardRevert(t *testing.T) {
	hardErr := errors.New("execution reverted: UnauthorizedWorker")
	coord := &mockCoordinator{fn: func() (*types.Transaction, error) {
		return nil, hardErr
	}}

	s := newSubmitter(coord, nil)
	err := s.Submit(context.Background(), newTestJob())
	require.Error(t, err)
	require.ErrorContains(t, err, "attempts exhausted")
}

// TestSubmit_ContextCancelledBeforeSubmit verifies that a cancelled context returns quickly.
func TestSubmit_ContextCancelledBeforeSubmit(t *testing.T) {
	coord := &mockCoordinator{fn: func() (*types.Transaction, error) {
		// This should not be called after context is cancelled.
		return nil, errors.New("should not be called")
	}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	s := newSubmitter(coord, nil)
	err := s.Submit(ctx, newTestJob())
	require.Error(t, err)
	require.ErrorContains(t, err, "context cancelled")
}

// TestSubmit_RetriesAndSucceeds verifies that transient errors are retried and
// the submission ultimately succeeds.
func TestSubmit_RetriesAndSucceeds(t *testing.T) {
	callCount := 0
	coord := &mockCoordinator{fn: func() (*types.Transaction, error) {
		callCount++
		if callCount < 3 {
			return nil, errors.New("transient rpc error")
		}
		return dummyTx(), nil
	}}
	w := successWaiter()

	s := newSubmitter(coord, w)
	err := s.Submit(context.Background(), newTestJob())
	require.NoError(t, err)
	require.Equal(t, 3, callCount, "should have retried 3 times")
}

// TestSubmit_NoWaiter verifies that passing nil waiter skips receipt waiting.
func TestSubmit_NoWaiter(t *testing.T) {
	coord := &mockCoordinator{fn: func() (*types.Transaction, error) {
		return dummyTx(), nil
	}}

	s := newSubmitter(coord, nil)
	err := s.Submit(context.Background(), newTestJob())
	require.NoError(t, err)
}
