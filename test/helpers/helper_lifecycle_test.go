package helpers

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestHelperLifecycle_ShutdownJoinsWorkerBeforeTempDirRemoval pins down the LIFO contract the
// production helpers rely on: a Shutdown cleanup registered after t.TempDir runs first, so every
// tracked service has observed cancellation and returned while the temporary directory backing
// its database is still present.
func TestHelperLifecycle_ShutdownJoinsWorkerBeforeTempDirRemoval(t *testing.T) {
	t.Parallel()

	t.Run("shutdown before tempdir removal", func(t *testing.T) {
		t.Parallel()

		// cleanup A: removes the directory. Registered first, so it runs last.
		dir := t.TempDir()

		lifecycle := newHelperLifecycle(t)

		workerExited := make(chan struct{})
		var dirPresentWhenCanceled atomic.Bool
		lifecycle.startBlocking(func(ctx context.Context) {
			<-ctx.Done()
			_, err := os.Stat(dir)
			dirPresentWhenCanceled.Store(err == nil)
			close(workerExited)
		})

		// cleanup B: runs after Shutdown (cleanup C) and before the directory is removed
		// (cleanup A). By then the worker must be joined and the directory must still exist,
		// which is exactly what stops the DB-gone fatal.
		t.Cleanup(func() {
			select {
			case <-workerExited:
			default:
				t.Error("worker was not joined before the temp dir was removed")
			}
			if !dirPresentWhenCanceled.Load() {
				t.Error("worker observed cancellation only after the temp dir was gone")
			}
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("temp dir removed before services were shut down: %v", err)
			}
		})

		// cleanup C: registered last, so it runs first.
		t.Cleanup(func() { lifecycle.Shutdown(t) })
	})
}

// TestHelperLifecycle_WaitShutdownIsBounded checks that waitShutdown reports a worker which
// ignores cancellation instead of blocking forever, and that such a worker can still be released
// and joined afterwards without leaking.
func TestHelperLifecycle_WaitShutdownIsBounded(t *testing.T) {
	t.Parallel()

	lifecycle := newHelperLifecycle(t)

	release := make(chan struct{})
	lifecycle.startBlocking(func(ctx context.Context) {
		// Deliberately ignore ctx cancellation: simulate a Start that does not observe the
		// context and therefore cannot be joined on cancel alone.
		<-release
	})

	lifecycle.cancel()

	const bound = 50 * time.Millisecond
	start := time.Now()
	if lifecycle.waitShutdown(bound) {
		t.Fatal("waitShutdown reported success while the worker was still running")
	}
	require.GreaterOrEqual(t, time.Since(start), bound,
		"waitShutdown returned without waiting for its bound")

	// Release the worker and join it so the test leaves no background goroutine.
	close(release)
	require.True(t, lifecycle.waitShutdown(5*time.Second),
		"worker was not joined after it was released")
}
