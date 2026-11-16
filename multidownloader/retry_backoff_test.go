package multidownloader

import (
	"context"
	"errors"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/stretchr/testify/require"
)

func newTestRetryBackoff(t *testing.T, policy aggkitcommon.RetryPolicyGenericConfig) *retryBackoff {
	t.Helper()
	cfg := Config{RetryAfterError: policy}
	b, err := cfg.newRetryBackoff()
	require.NoError(t, err)
	return b
}

func TestRetryBackoff_DefaultDelaysAreExponentialAndReset(t *testing.T) {
	b := newTestRetryBackoff(t, newDefaultRetryAfterError())

	expected := []time.Duration{
		500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second, // the last one is reused
	}
	for i, want := range expected {
		delay, _ := b.Failed()
		require.Equal(t, want, delay, "failure %d", i+1)
	}
	require.Equal(t, len(expected), b.Failures())

	require.Equal(t, len(expected), b.Succeeded())
	require.Equal(t, 0, b.Failures())
	delay, mustLog := b.Failed()
	require.Equal(t, 500*time.Millisecond, delay, "the delays must start again after a success")
	require.True(t, mustLog, "the first failure after a success must be logged")
}

func TestRetryBackoff_UnsetPolicyUsesTheDefault(t *testing.T) {
	b := newTestRetryBackoff(t, aggkitcommon.RetryPolicyGenericConfig{})
	for _, want := range defaultRetryAfterErrorDelays {
		delay, _ := b.Failed()
		require.Equal(t, want, delay)
	}
}

func TestRetryBackoff_BackoffMode(t *testing.T) {
	b := newTestRetryBackoff(t, aggkitcommon.RetryPolicyGenericConfig{
		Mode:              aggkitcommon.RetryConfigModeBackoff,
		MaxRetries:        aggkitcommon.MaxAttemptsInfinite,
		InitialBackoff:    types.NewDuration(time.Second),
		MaxBackoff:        types.NewDuration(5 * time.Second),
		BackoffMultiplier: 2,
	})
	for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second,
		5 * time.Second} {
		delay, _ := b.Failed()
		require.Equal(t, want, delay, "failure %d", i+1)
	}
}

func TestRetryBackoff_LogsAreBounded(t *testing.T) {
	b := newTestRetryBackoff(t, newDefaultRetryAfterError())
	logged := []int{}
	for i := 1; i <= 100; i++ {
		if _, mustLog := b.Failed(); mustLog {
			logged = append(logged, i)
		}
	}
	// first, powers of two and every logEveryNFailures
	require.Equal(t, []int{1, 2, 4, 8, 16, 20, 32, 40, 60, 64, 80, 100}, logged)
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
	testData.mdr.cfg.RetryAfterError = aggkitcommon.RetryPolicyGenericConfig{
		Mode:       aggkitcommon.RetryConfigModeDelays,
		MaxRetries: aggkitcommon.MaxAttemptsInfinite,
		Delays:     []types.Duration{types.NewDuration(time.Hour)},
	}
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
