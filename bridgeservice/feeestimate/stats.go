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
// back from the percentile to the maximum: a percentile of a handful of samples is noise
// (D2).
const maxInsteadOfPercentileBelow = 10

// statisticOf quotes one bucket's gasUsed statistic: the percentile by nearest rank, or the
// maximum when the bucket is small (D2). samples must not be empty; percentile is in
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
// caller waits for and shares its result. The first caller's context governs the shared
// computation, and a failed compute is never cached (the next caller retries).
type ttlResult[T any] struct {
	ttl       time.Duration
	now       func() time.Time
	mu        sync.Mutex
	value     T
	expiresAt time.Time
	valid     bool
	sf        singleflight.Group
}

// newTTLResult builds a ttlResult for the given ttl and clock.
func newTTLResult[T any](ttl time.Duration, now func() time.Time) *ttlResult[T] {
	return &ttlResult[T]{ttl: ttl, now: now}
}

// get returns the cached value while it is fresh, or computes it once and caches it.
func (r *ttlResult[T]) get(ctx context.Context, compute func(context.Context) (T, error)) (T, error) {
	if value, ok := r.cached(); ok {
		return value, nil
	}

	result, err, _ := r.sf.Do("recompute", func() (any, error) {
		// Another goroutine may have refreshed the value while this caller waited for
		// the singleflight lock.
		if value, ok := r.cached(); ok {
			return value, nil
		}
		value, err := compute(ctx)
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
	if err != nil {
		var zero T
		return zero, err
	}

	value, ok := result.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("feeestimate: ttl cache holds %T, want %T", result, zero)
	}
	return value, nil
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
