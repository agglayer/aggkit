package sync

import (
	"context"
	"fmt"
	"log"
	"time"
)

var LogFatalf = log.Fatalf

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
	fmt.Printf("Handle: %s, attempts: %d\n", funcName, attempts)
	if h.MaxRetryAttemptsAfterError > -1 && attempts >= h.MaxRetryAttemptsAfterError {
		LogFatalf(
			"%s failed too many times (%d)",
			funcName, h.MaxRetryAttemptsAfterError,
		)
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(h.RetryAfterErrorPeriod):
		return
	}
}
