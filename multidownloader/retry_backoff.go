package multidownloader

import (
	"context"
	"math/rand/v2"
	"time"
)

// logEveryNFailures is how often a persistent error is logged again, besides the first occurrence and every
// power of two of consecutive failures
const logEveryNFailures = 20

// retryBackoff spaces the retries of an operation that keeps failing: the delay starts at initial, doubles
// on each consecutive failure up to max and is jittered (between half and the full delay) so several
// instances don't retry in lockstep. A success resets it.
// It also decides when a repeated failure is worth logging, so a persistently failing endpoint doesn't
// produce one log line per retry.
// It is not safe for concurrent use.
type retryBackoff struct {
	initial time.Duration
	max     time.Duration
	// failures is the number of consecutive failures since the last success
	failures int
	// jitter returns a random duration in [0, d]; it can be replaced in tests
	jitter func(d time.Duration) time.Duration
}

// newRetryBackoff returns a backoff from initial up to maxDelay. It trusts its inputs (0 < initial <= maxDelay):
// the defaults and limits are resolved by Config (see Config.newRetryBackoff)
func newRetryBackoff(initial, maxDelay time.Duration) *retryBackoff {
	return &retryBackoff{
		initial: initial,
		max:     maxDelay,
		jitter: func(d time.Duration) time.Duration {
			if d <= 0 {
				return 0
			}
			return rand.N(d + 1) //nolint:gosec // jitter doesn't need a cryptographic source
		},
	}
}

// Failed records a failure and returns how long to wait before retrying and if this failure must be logged:
// the first one, every power of two and every logEveryNFailures consecutive failures. The error text is not
// taken into account on purpose: errors that embed changing details (request ids, block numbers) would
// otherwise bypass the limit
func (b *retryBackoff) Failed() (time.Duration, bool) {
	b.failures++
	mustLog := b.failures&(b.failures-1) == 0 || b.failures%logEveryNFailures == 0
	return b.delay(), mustLog
}

// delay returns the jittered delay for the current number of consecutive failures
func (b *retryBackoff) delay() time.Duration {
	d := b.initial
	for i := 1; i < b.failures && d < b.max; i++ {
		if d > b.max/2 {
			// doubling would reach the cap (and could overflow with a huge max)
			d = b.max
			break
		}
		d *= 2
	}
	if d > b.max {
		d = b.max
	}
	half := d / 2 //nolint:mnd
	return half + b.jitter(d-half)
}

// Succeeded resets the backoff and returns the number of consecutive failures before this success
func (b *retryBackoff) Succeeded() int {
	failures := b.failures
	b.failures = 0
	return failures
}

// Failures returns the number of consecutive failures since the last success
func (b *retryBackoff) Failures() int {
	return b.failures
}

// waitOrDone waits for d or until ctx is done, whichever happens first. It returns ctx.Err() if ctx is done
func waitOrDone(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
