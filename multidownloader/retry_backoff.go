package multidownloader

import (
	"context"
	"time"

	commontypes "github.com/agglayer/aggkit/common/types"
)

// logEveryNFailures is how often a persistent error is logged again, besides the first occurrence and every
// power of two of consecutive failures
const logEveryNFailures = 20

// retryBackoff spaces the retries of an operation that keeps failing, following a retry policy
// (aggkitcommon.RetryPolicyGenericConfig: a list of concrete delays or an exponential backoff). The n-th
// consecutive failure waits the policy delay for attempt n-1 (the last one is reused). A success resets it.
// It also decides when a repeated failure is worth logging, so a persistently failing endpoint doesn't
// produce one log line per retry.
// It is not safe for concurrent use.
type retryBackoff struct {
	handler commontypes.RetryHandler
	// failures is the number of consecutive failures since the last success
	failures int
}

// newRetryBackoff returns a backoff that takes the delays from handler
func newRetryBackoff(handler commontypes.RetryHandler) *retryBackoff {
	return &retryBackoff{handler: handler}
}

// Failed records a failure and returns how long to wait before retrying and if this failure must be logged:
// the first one, every power of two and every logEveryNFailures consecutive failures. The error text is not
// taken into account on purpose: errors that embed changing details (request ids, block numbers) would
// otherwise bypass the limit
func (b *retryBackoff) Failed() (time.Duration, bool) {
	b.failures++
	mustLog := b.failures&(b.failures-1) == 0 || b.failures%logEveryNFailures == 0
	return b.handler.Delay(b.failures - 1), mustLog
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
