package multidownloader

import (
	"context"
	"math/rand/v2"
	"time"
)

// logEveryNFailures is how often a persistent error is logged again, besides the first occurrence,
// a change of error and every power of two of consecutive failures
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
	lastErr  string
	// jitter returns a random duration in [0, d]; it can be replaced in tests
	jitter func(d time.Duration) time.Duration
}

func newRetryBackoff(initial, maxDelay time.Duration) *retryBackoff {
	if initial <= 0 {
		initial = defaultRetryAfterErrorInitialDelay
	}
	if maxDelay < initial {
		maxDelay = initial
	}
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

// Failed records a failure with error err and returns how long to wait before retrying and if this
// failure must be logged (first failure, the error changed, or a power of two / every logEveryNFailures
// consecutive failures)
func (b *retryBackoff) Failed(err error) (time.Duration, bool) {
	b.failures++
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	mustLog := b.failures == 1 || errMsg != b.lastErr ||
		b.failures&(b.failures-1) == 0 || b.failures%logEveryNFailures == 0
	b.lastErr = errMsg
	return b.delay(), mustLog
}

// delay returns the jittered delay for the current number of consecutive failures
func (b *retryBackoff) delay() time.Duration {
	d := b.initial
	for i := 1; i < b.failures && d < b.max; i++ {
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
	b.lastErr = ""
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
