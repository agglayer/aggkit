package sync

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	compmocks "github.com/agglayer/aggkit/db/compatibility/mocks"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/stretchr/testify/require"
)

// TestSync_ReturnsOnCanceledContext pins that Sync exits cleanly (without a fatal log) when its
// context is already canceled, instead of retrying the cancellation error until the fatal limit.
func TestSync_ReturnsOnCanceledContext(t *testing.T) {
	originalLogFatalf := LogFatalf
	t.Cleanup(func() { LogFatalf = originalLogFatalf })

	var fatalCalled atomic.Bool
	LogFatalf = func(format string, args ...any) { fatalCalled.Store(true) }

	rdm := NewReorgDetectorMock(t)
	rdm.EXPECT().Subscribe(reorgDetectorID).Return(&reorgdetector.Subscription{}, nil)

	driver, err := NewEVMDriver(
		rdm,
		NewProcessorMock(t),
		NewDownloaderMock(t),
		reorgDetectorID,
		10,
		&RetryHandler{MaxRetryAttemptsAfterError: 1, RetryAfterErrorPeriod: time.Millisecond},
		compmocks.NewCompatibilityChecker(t),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		driver.Sync(ctx, nil)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Sync did not return after its context was canceled")
	}
	require.False(t, fatalCalled.Load(), "Sync must not fatal on a canceled context")
}
