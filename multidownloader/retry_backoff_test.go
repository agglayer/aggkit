package multidownloader

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agglayer/aggkit/config/types"
	"github.com/stretchr/testify/require"
)

// fullDelay makes the jitter deterministic: it always returns the upper bound
func fullDelay(d time.Duration) time.Duration { return d }

func TestRetryBackoff_GrowsUpToTheCapAndResets(t *testing.T) {
	b := newRetryBackoff(100*time.Millisecond, time.Second)
	b.jitter = fullDelay
	errRPC := errors.New("429 Too Many Requests")

	expected := []time.Duration{
		100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond,
		time.Second, time.Second, time.Second,
	}
	for i, want := range expected {
		delay, _ := b.Failed(errRPC)
		require.Equal(t, want, delay, "failure %d", i+1)
	}
	require.Equal(t, len(expected), b.Failures())

	require.Equal(t, len(expected), b.Succeeded())
	require.Equal(t, 0, b.Failures())
	delay, mustLog := b.Failed(errRPC)
	require.Equal(t, 100*time.Millisecond, delay, "the delay must start again after a success")
	require.True(t, mustLog, "the first failure after a success must be logged")
}

func TestRetryBackoff_JitterKeepsTheDelayBetweenHalfAndFull(t *testing.T) {
	b := newRetryBackoff(time.Second, 8*time.Second)
	for range 4 {
		delay, _ := b.Failed(errors.New("boom"))
		full := b.initial << (b.Failures() - 1)
		require.GreaterOrEqual(t, delay, full/2)
		require.LessOrEqual(t, delay, full)
	}
}

func TestRetryBackoff_DefaultsAndInvalidValues(t *testing.T) {
	b := newRetryBackoff(0, 0)
	require.Equal(t, defaultRetryAfterErrorInitialDelay, b.initial)
	require.Equal(t, defaultRetryAfterErrorInitialDelay, b.max, "max can't be lower than initial")

	cfg := Config{}
	b = cfg.newRetryBackoff()
	require.Equal(t, defaultRetryAfterErrorInitialDelay, b.initial)
	require.Equal(t, defaultRetryAfterErrorMaxDelay, b.max)
}

func TestRetryBackoff_LogsAreBounded(t *testing.T) {
	b := newRetryBackoff(time.Millisecond, time.Millisecond)
	errA := errors.New("error A")
	logged := []int{}
	for i := 1; i <= 100; i++ {
		if _, mustLog := b.Failed(errA); mustLog {
			logged = append(logged, i)
		}
	}
	// first, powers of two and every logEveryNFailures
	require.Equal(t, []int{1, 2, 4, 8, 16, 20, 32, 40, 60, 64, 80, 100}, logged)

	_, mustLog := b.Failed(errors.New("error B"))
	require.True(t, mustLog, "a different error must be logged")
	_, mustLog = b.Failed(errors.New("error B"))
	require.False(t, mustLog, "the same error again must not be logged")
}

func TestWaitOrDone(t *testing.T) {
	require.NoError(t, waitOrDone(t.Context(), time.Millisecond))
	require.NoError(t, waitOrDone(t.Context(), 0))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	start := time.Now()
	require.ErrorIs(t, waitOrDone(ctx, time.Hour), context.Canceled)
	require.Less(t, time.Since(start), time.Second)
	require.ErrorIs(t, waitOrDone(ctx, 0), context.Canceled)
}

func TestStartNumLoops_CancelInterruptsTheRetryBackoff(t *testing.T) {
	testData := newEVMMultidownloaderTestData(t, false)
	testData.mdr.cfg.RetryAfterErrorInitialDelay = types.NewDuration(time.Hour)
	testData.mdr.cfg.RetryAfterErrorMaxDelay = types.NewDuration(time.Hour)
	testData.mdr.state = NewEmptyState()
	testData.mdr.debug = NewEVMMultidownloaderDebug()
	testData.mdr.debug.debugStepForcedReturnError = errors.New("429 Too Many Requests")

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- testData.mdr.startNumLoops(ctx, 2)
	}()
	// Let the loop fail and start waiting the (1h) backoff
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context must interrupt the retry backoff")
	}
}
