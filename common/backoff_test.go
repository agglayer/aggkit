package common

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testBackoffConfig(jitter float64) ExponentialBackoffConfig {
	return ExponentialBackoffConfig{Initial: 100 * time.Millisecond, Max: time.Second, Factor: 2, Jitter: jitter}
}

func TestExponentialBackoffConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ExponentialBackoffConfig
		wantErr bool
	}{
		{"valid", testBackoffConfig(0.2), false},
		{"zero jitter", testBackoffConfig(0), false},
		{"initial zero", ExponentialBackoffConfig{Initial: 0, Max: time.Second, Factor: 2}, true},
		{"max below initial", ExponentialBackoffConfig{Initial: time.Second, Max: time.Millisecond, Factor: 2}, true},
		{"factor one", ExponentialBackoffConfig{Initial: time.Millisecond, Max: time.Second, Factor: 1}, true},
		{"jitter one", ExponentialBackoffConfig{Initial: time.Millisecond, Max: time.Second, Factor: 2, Jitter: 1}, true},
		{"jitter negative", ExponentialBackoffConfig{Initial: time.Millisecond, Max: time.Second, Factor: 2, Jitter: -0.1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			_, ctorErr := NewExponentialBackoff(tt.cfg)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalidConfig)
				require.ErrorIs(t, ctorErr, ErrInvalidConfig)
				return
			}
			require.NoError(t, err)
			require.NoError(t, ctorErr)
		})
	}
}

func TestExponentialBackoff_GrowthCapReset(t *testing.T) {
	b, err := NewExponentialBackoff(testBackoffConfig(0), WithBackoffRandFloat(func() float64 { return 0.5 }))
	require.NoError(t, err)

	expected := []time.Duration{100, 200, 400, 800, 1000, 1000, 1000}
	for i, e := range expected {
		require.Equal(t, e*time.Millisecond, b.Next(), "attempt %d", i)
		require.Equal(t, i+1, b.Attempts())
	}

	b.Reset()
	require.Zero(t, b.Attempts())
	require.Equal(t, 100*time.Millisecond, b.Next())
}

func TestExponentialBackoff_HugeAttemptDoesNotOverflow(t *testing.T) {
	b, err := NewExponentialBackoff(testBackoffConfig(0), WithBackoffRandFloat(func() float64 { return 0.5 }))
	require.NoError(t, err)
	b.attempt = 1 << 20
	require.Equal(t, time.Second, b.Next())
}

func TestExponentialBackoff_JitterBounds(t *testing.T) {
	const jitter = 0.2
	tests := []struct {
		name string
		r    float64
	}{
		{"lowest", 0},
		{"middle", 0.5},
		{"highest", 0.999999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := NewExponentialBackoff(testBackoffConfig(jitter), WithBackoffRandFloat(func() float64 { return tt.r }))
			require.NoError(t, err)
			for i := 0; i < 8; i++ {
				base := float64(100*time.Millisecond) * float64(int(1)<<i)
				if base > float64(time.Second) {
					base = float64(time.Second)
				}
				lo := time.Duration(base * (1 - jitter))
				hi := time.Duration(base * (1 + jitter))
				if hi > time.Second {
					hi = time.Second
				}
				got := b.Next()
				require.GreaterOrEqual(t, got, lo-time.Nanosecond, "attempt %d", i)
				require.LessOrEqual(t, got, hi, "attempt %d", i)
			}
		})
	}
}

func TestExponentialBackoff_SleepUsesInjectedSleeper(t *testing.T) {
	var slept []time.Duration
	b, err := NewExponentialBackoff(testBackoffConfig(0),
		WithBackoffRandFloat(func() float64 { return 0.5 }),
		WithBackoffSleeper(func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		}))
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		d, err := b.Sleep(context.Background())
		require.NoError(t, err)
		require.Equal(t, slept[i], d)
	}
	require.Equal(t, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}, slept)
}

func TestExponentialBackoff_SleepCtxCancelled(t *testing.T) {
	b, err := NewExponentialBackoff(ExponentialBackoffConfig{Initial: time.Hour, Max: time.Hour, Factor: 2})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err = b.Sleep(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), time.Second)
}

func TestSleepContext(t *testing.T) {
	require.NoError(t, SleepContext(context.Background(), time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, SleepContext(ctx, time.Hour), context.Canceled)
}
