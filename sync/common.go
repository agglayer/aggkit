package sync

import (
	"context"
	"errors"
	"log"
	"time"
)

var LogFatalf = log.Fatalf

// ErrRetryForeverNonFatal is a sentinel error class that must be retried indefinitely and must never
// trigger RetryHandler's fatal guard, regardless of the configured MaxRetryAttemptsAfterError. Callers
// that need this semantic should wrap their error with %w and this sentinel; the appender retry loop in
// evmdownloader.go checks errors.Is(err, ErrRetryForeverNonFatal) to route around Handle's fatal branch,
// sleeping via waitRetryPeriod instead.
var ErrRetryForeverNonFatal = errors.New("retry forever without fatal")

type RetryHandler struct {
	RetryAfterErrorPeriod      time.Duration
	MaxRetryAttemptsAfterError int
}

// Handle is a method that handles retries
// If reach max retry attempts, it will log.Fatalf
// Otherwise, it will sleep for RetryAfterErrorPeriod
// For be able to test it, the Fatalf function can be override
// with var LogFatalf and change it for a panic that be catched by the test
func (h *RetryHandler) Handle(ctx context.Context, funcName string, attempts int) {
	if h.MaxRetryAttemptsAfterError > -1 && attempts >= h.MaxRetryAttemptsAfterError {
		LogFatalf(
			"%s failed too many times (%d)",
			funcName, h.MaxRetryAttemptsAfterError,
		)
	}

	h.waitRetryPeriod(ctx)
}

// waitRetryPeriod blocks until ctx is done or RetryAfterErrorPeriod elapses. It is the non-fatal sleep
// tail of Handle, extracted so callers that must never hit the fatal guard (e.g. the
// ErrRetryForeverNonFatal error class) can still back off between retries.
func (h *RetryHandler) waitRetryPeriod(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(h.RetryAfterErrorPeriod):
		return
	}
}
