package feeestimate

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// maxInsteadOfPercentileBelow is the bucket size under which the quoted statistic falls
// back from the percentile to the maximum: a percentile of a handful of samples is noise.
const maxInsteadOfPercentileBelow = 10

// statisticOf quotes one bucket's gasUsed statistic: the percentile by nearest rank, or the
// maximum when the bucket is small. samples must not be empty; percentile is in
// (0, 100].
func statisticOf(samples []uint64, percentile float64) uint64 {
	if len(samples) == 0 {
		return 0
	}
	if len(samples) < maxInsteadOfPercentileBelow {
		return slices.Max(samples)
	}

	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	// Nearest rank: the ceil(p/100 * n)-th smallest sample, 1-based.
	rank := int(math.Ceil(percentile / 100 * float64(len(sorted))))
	rank = min(max(rank, 1), len(sorted))
	return sorted[rank-1]
}

// ttlResult caches the single most recent result of a compute function for a TTL. Concurrent
// callers collapse into one recompute via singleflight: while a compute is in flight, every
// caller waits for and shares its result. The shared computation runs detached from every
// caller's request context, bounded by its own computeTimeout instead: each caller selects
// on its OWN context while the computation runs, so a caller that disconnects or times out
// returns immediately without aborting the computation the other waiters still share, and a
// cold start that outlives any single caller's read timeout still completes and is cached.
// A failed compute is never cached (the next caller retries).
type ttlResult[T any] struct {
	ttl            time.Duration
	computeTimeout time.Duration
	now            func() time.Time
	mu             sync.Mutex
	value          T
	expiresAt      time.Time
	valid          bool
	sf             singleflight.Group
}

// newTTLResult builds a ttlResult for the given ttl, shared-compute timeout and clock.
func newTTLResult[T any](ttl, computeTimeout time.Duration, now func() time.Time) *ttlResult[T] {
	return &ttlResult[T]{ttl: ttl, computeTimeout: computeTimeout, now: now}
}

// get returns the cached value while it is fresh, or computes it once and caches it.
func (r *ttlResult[T]) get(ctx context.Context, compute func(context.Context) (T, error)) (T, error) {
	if value, ok := r.cached(); ok {
		return value, nil
	}

	// DoChan rather than Do: every caller selects on its own context below, so a caller
	// that gives up returns immediately instead of blocking on a result it can no longer
	// deliver, while the shared computation runs on for the others.
	ch := r.sf.DoChan("recompute", func() (any, error) {
		// Another goroutine may have refreshed the value while this caller waited for
		// the singleflight lock.
		if value, ok := r.cached(); ok {
			return value, nil
		}
		// The shared computation takes the values but not the cancellation of the context
		// of whichever caller started it, and gets its own timeout: no single caller's
		// read timeout or disconnect may abort the compute every concurrent waiter
		// shares. Without this, the first caller's request context would govern the
		// shared flight: a cancelled first caller, or a cold start outlasting one
		// caller's read timeout, would fail every waiter with it.
		computeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.computeTimeout)
		defer cancel()
		value, err := compute(computeCtx)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.value = value
		r.valid = true
		r.expiresAt = r.now().Add(r.ttl)
		r.mu.Unlock()
		return value, nil
	})

	select {
	case <-ctx.Done():
		// This caller left; the shared computation keeps running, and its result is
		// cached for the next caller when it succeeds.
		var zero T
		return zero, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			var zero T
			return zero, result.Err
		}
		value, ok := result.Val.(T)
		if !ok {
			var zero T
			return zero, fmt.Errorf("feeestimate: ttl cache holds %T, want %T", result.Val, zero)
		}
		return value, nil
	}
}

// cached returns the cached value while it is still fresh.
func (r *ttlResult[T]) cached() (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.valid || !r.now().Before(r.expiresAt) {
		var zero T
		return zero, false
	}
	return r.value, true
}
