package waiter_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agglayer/aggkit/dvnworker/waiter"
	"github.com/agglayer/aggkit/log"
)

// ─── mock implementations ──────────────────────────────────────────────────

// mockCertChecker is a simple mock whose return value is controlled by an
// atomic flag so tests can flip it from a goroutine without a race.
type mockCertChecker struct {
	settled atomic.Bool
	err     error
}

func (m *mockCertChecker) IsLeafSettled(_ context.Context, _ uint32, _ uint32) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.settled.Load(), nil
}

// mockGERChecker mirrors mockCertChecker for the GER condition.
type mockGERChecker struct {
	injected atomic.Bool
	err      error
}

func (m *mockGERChecker) IsGERInjected(_ context.Context, _ uint32) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.injected.Load(), nil
}

// ─── helpers ───────────────────────────────────────────────────────────────

func newTestJob() waiter.Job {
	return waiter.Job{
		SourceBridgeNetwork: 2,
		DepositCount:        7,
		L1InfoTreeIndex:     3,
	}
}

func newTestWaiter(cert *mockCertChecker, ger *mockGERChecker) *waiter.Waiter {
	// Use a short poll interval so tests complete quickly.
	return waiter.New(cert, ger, 10*time.Millisecond, log.GetDefaultLogger())
}

// ─── test cases ────────────────────────────────────────────────────────────

func TestWaiter_BothImmediatelySatisfied(t *testing.T) {
	cert := &mockCertChecker{}
	cert.settled.Store(true)

	ger := &mockGERChecker{}
	ger.injected.Store(true)

	w := newTestWaiter(cert, ger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := w.Wait(ctx, newTestJob())
	require.NoError(t, err)
}

func TestWaiter_CertNotSettledThenSettles(t *testing.T) {
	cert := &mockCertChecker{}
	cert.settled.Store(false)

	ger := &mockGERChecker{}
	ger.injected.Store(true)

	w := newTestWaiter(cert, ger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Flip the cert flag after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cert.settled.Store(true)
	}()

	err := w.Wait(ctx, newTestJob())
	require.NoError(t, err)
}

func TestWaiter_GERNotInjectedThenInjected(t *testing.T) {
	cert := &mockCertChecker{}
	cert.settled.Store(true)

	ger := &mockGERChecker{}
	ger.injected.Store(false)

	w := newTestWaiter(cert, ger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Flip the GER flag after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		ger.injected.Store(true)
	}()

	err := w.Wait(ctx, newTestJob())
	require.NoError(t, err)
}

func TestWaiter_ContextCancelled(t *testing.T) {
	cert := &mockCertChecker{}
	cert.settled.Store(false)

	ger := &mockGERChecker{}
	ger.injected.Store(false)

	w := newTestWaiter(cert, ger)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel the context immediately.
	cancel()

	err := w.Wait(ctx, newTestJob())
	require.ErrorIs(t, err, context.Canceled)
}
