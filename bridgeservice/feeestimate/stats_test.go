package feeestimate

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStatisticOf(t *testing.T) {
	tests := []struct {
		name       string
		samples    []uint64
		percentile float64
		want       uint64
	}{
		{
			name:       "empty",
			samples:    nil,
			percentile: 90,
			want:       0,
		},
		{
			name:       "single sample falls back to the maximum",
			samples:    []uint64{123456},
			percentile: 90,
			want:       123456,
		},
		{
			name:       "below the small-bucket threshold falls back to the maximum",
			samples:    []uint64{100, 900, 300, 500},
			percentile: 90,
			want:       900,
		},
		{
			name:       "threshold bucket uses the percentile (9th of 10)",
			samples:    []uint64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
			percentile: 90,
			want:       90,
		},
		{
			name:       "large unsorted bucket sorts before ranking",
			samples:    shuffledRange(1, 200),
			percentile: 90,
			want:       180,
		},
		{
			name:       "percentile 100 is the maximum",
			samples:    shuffledRange(1, 100),
			percentile: 100,
			want:       100,
		},
		{
			name:       "percentile 50 is the median",
			samples:    shuffledRange(1, 100),
			percentile: 50,
			want:       50,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, statisticOf(tt.samples, tt.percentile))
		})
	}
}

// shuffledRange returns the integers [from, to] in a deliberately unsorted order.
func shuffledRange(from, to uint64) []uint64 {
	values := make([]uint64, 0, to-from+1)
	for v := from; v <= to; v++ {
		values = append(values, v)
	}
	// A fixed odd-even permutation: deterministic, and not sorted for n > 2.
	shuffled := make([]uint64, 0, len(values))
	for i := 1; i < len(values); i += 2 {
		shuffled = append(shuffled, values[i])
	}
	for i := 0; i < len(values); i += 2 {
		shuffled = append(shuffled, values[i])
	}
	return shuffled
}

func TestTTLResult_CachesWithinTTLAndRecomputesAfter(t *testing.T) {
	clock := newFakeClock()
	result := newTTLResult[int](2*time.Second, time.Minute, clock.now)

	calls := 0
	compute := func(context.Context) (int, error) { calls++; return 42, nil }

	first, err := result.get(context.Background(), compute)
	require.NoError(t, err)
	require.Equal(t, 42, first)
	require.Equal(t, 1, calls)

	// Still fresh: served from the cache.
	second, err := result.get(context.Background(), compute)
	require.NoError(t, err)
	require.Equal(t, 42, second)
	require.Equal(t, 1, calls)

	// Past the TTL: recomputed exactly once more.
	clock.advance(2 * time.Second)
	third, err := result.get(context.Background(), compute)
	require.NoError(t, err)
	require.Equal(t, 42, third)
	require.Equal(t, 2, calls)
}

func TestTTLResult_DoesNotCacheErrors(t *testing.T) {
	clock := newFakeClock()
	result := newTTLResult[int](time.Hour, time.Minute, clock.now)

	calls := 0
	boom := errors.New("boom")
	_, err := result.get(context.Background(), func(context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, boom
		}
		return 7, nil
	})
	require.ErrorIs(t, err, boom)

	// The failed compute was not cached: the next caller retries and succeeds.
	value, err := result.get(context.Background(), func(context.Context) (int, error) {
		calls++
		return 7, nil
	})
	require.NoError(t, err)
	require.Equal(t, 7, value)
	require.Equal(t, 2, calls)
}

func TestTTLResult_SingleRecomputeUnderConcurrentCallers(t *testing.T) {
	clock := newFakeClock()
	result := newTTLResult[int](time.Hour, time.Minute, clock.now)

	const callers = 20
	var calls int64
	compute := func(context.Context) (int, error) {
		calls++
		time.Sleep(20 * time.Millisecond) // hold the flight open so callers pile up
		return 5, nil
	}

	// Release every goroutine at once so they genuinely race for the same empty cache.
	start := make(chan struct{})
	values := make([]int, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			values[i], errs[i] = result.get(context.Background(), compute)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := range errs {
		require.NoError(t, errs[i])
		require.Equal(t, 5, values[i])
	}
	// The singleflight collapsed all concurrent callers into one shared compute.
	require.Equal(t, int64(1), calls)
}

// TestTTLResult_CancelledCallerDoesNotAbortSharedCompute pins the detachment of the shared
// computation from any single caller's request context: a caller that cancels mid-compute
// returns immediately with its own context error, while a concurrent waiter on the same
// flight still receives the computed result, and the result is cached afterwards.
func TestTTLResult_CancelledCallerDoesNotAbortSharedCompute(t *testing.T) {
	clock := newFakeClock()
	result := newTTLResult[int](time.Hour, time.Minute, clock.now)

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	compute := func(context.Context) (int, error) {
		calls.Add(1)
		close(started)
		<-release
		return 42, nil
	}

	// Caller 1 starts the shared compute; caller 2 joins the same in-flight compute.
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	type outcome struct {
		value int
		err   error
	}
	first := make(chan outcome, 1)
	go func() {
		value, err := result.get(ctx1, compute)
		first <- outcome{value: value, err: err}
	}()
	<-started
	second := make(chan outcome, 1)
	go func() {
		value, err := result.get(context.Background(), compute)
		second <- outcome{value: value, err: err}
	}()
	time.Sleep(50 * time.Millisecond) // let caller 2 join the in-flight compute

	// Caller 1 disconnects mid-compute: it returns its own context error at once, without
	// aborting the compute caller 2 is still waiting on.
	cancel1()
	got := <-first
	require.ErrorIs(t, got.err, context.Canceled)
	require.Equal(t, 0, got.value)

	// Caller 2, still connected, receives the result of the flight it shared with the
	// caller that already left.
	close(release)
	got = <-second
	require.NoError(t, got.err)
	require.Equal(t, 42, got.value)
	require.Equal(t, int64(1), calls.Load(), "the cancelled caller must not trigger a recompute")

	// The completed computation is cached: a later caller is served without another compute.
	value, err := result.get(context.Background(), compute)
	require.NoError(t, err)
	require.Equal(t, 42, value)
	require.Equal(t, int64(1), calls.Load())
}

// TestTTLResult_ComputeOutlivingCallersDeadlineIsStillCached pins the other half of the
// detachment: a compute that outlives a caller's own request deadline keeps running under
// its own compute timeout, and its result is cached for the next caller.
func TestTTLResult_ComputeOutlivingCallersDeadlineIsStillCached(t *testing.T) {
	clock := newFakeClock()
	result := newTTLResult[int](time.Hour, 10*time.Second, clock.now)

	release := make(chan struct{})
	var calls atomic.Int64
	compute := func(context.Context) (int, error) {
		calls.Add(1)
		<-release
		return 42, nil
	}

	// The caller's own deadline expires far before the shared compute's bound: the caller
	// returns its deadline error instead of pinning the shared compute to it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := result.get(ctx, compute)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// The shared computation runs to its own bound and caches its result: the next caller
	// is served the value, with no second compute.
	close(release)
	value, err := result.get(context.Background(), compute)
	require.NoError(t, err)
	require.Equal(t, 42, value)
	require.Equal(t, int64(1), calls.Load())
}

func TestConfigWithDefaults(t *testing.T) {
	t.Run("zero value takes every default", func(t *testing.T) {
		cfg := Config{}.withDefaults()
		require.Equal(t, uint32(0), cfg.NetworkID)
		require.Equal(t, defaultWindow, cfg.Window)
		require.Equal(t, defaultMinSamples, cfg.MinSamples)
		require.Equal(t, defaultPerTokenMinSamples, cfg.PerTokenMinSamples)
		require.Equal(t, defaultRPCConcurrency, cfg.RPCConcurrency)
		require.Equal(t, defaultTxCacheSize, cfg.TxCacheSize)
		require.Equal(t, defaultPercentile, cfg.Percentile)
		require.Equal(t, defaultStatsTTL, cfg.StatsTTL)
		require.Equal(t, defaultGasPriceTTL, cfg.GasPriceTTL)
		require.Equal(t, defaultComputeTimeout, cfg.ComputeTimeout)
		require.Equal(t, defaultTokenFactsTTL, cfg.TokenFactsTTL)
	})

	t.Run("explicit values are kept", func(t *testing.T) {
		cfg := Config{
			NetworkID:          7,
			Window:             50,
			MinSamples:         2,
			PerTokenMinSamples: 5,
			RPCConcurrency:     3,
			TxCacheSize:        64,
			Percentile:         95,
			StatsTTL:           time.Minute,
			GasPriceTTL:        time.Second,
			ComputeTimeout:     2 * time.Minute,
			TokenFactsTTL:      time.Second,
		}.withDefaults()
		require.Equal(t, uint32(7), cfg.NetworkID)
		require.Equal(t, 50, cfg.Window)
		require.Equal(t, 2, cfg.MinSamples)
		require.Equal(t, 5, cfg.PerTokenMinSamples)
		require.Equal(t, 3, cfg.RPCConcurrency)
		require.Equal(t, 64, cfg.TxCacheSize)
		require.Equal(t, 95.0, cfg.Percentile)
		require.Equal(t, time.Minute, cfg.StatsTTL)
		require.Equal(t, time.Second, cfg.GasPriceTTL)
		require.Equal(t, 2*time.Minute, cfg.ComputeTimeout)
		require.Equal(t, time.Second, cfg.TokenFactsTTL)
	})

	t.Run("invalid values fall back", func(t *testing.T) {
		cfg := Config{Window: -1, Percentile: 250, StatsTTL: -time.Second, TokenFactsTTL: -time.Second}.
			withDefaults()
		require.Equal(t, defaultWindow, cfg.Window)
		require.Equal(t, maxPercentile, cfg.Percentile)
		require.Equal(t, defaultStatsTTL, cfg.StatsTTL)
		require.Equal(t, defaultTokenFactsTTL, cfg.TokenFactsTTL)
	})
}
