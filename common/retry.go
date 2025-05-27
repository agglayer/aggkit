package common

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrNonRetryable = errors.New("non-retryable error")
)

const (
	operationFailedTemplate = "operation failed after %d attempt(s): %w"
)

// RetryWithExponentialBackoff retries the given function up to maxRetries with exponential backoff.
// Use `context.Canceled` or `context.DeadlineExceeded` to cancel early.
// Wrap return with `fmt.Errorf("%w: your error", ErrNonRetryable)` to avoid retries.
func RetryWithExponentialBackoff(ctx context.Context, maxRetries uint,
	initialDelay time.Duration, callback func() error) error {
	if callback == nil {
		return errors.New("retry callback cannot be nil")
	}

	if ctx == nil {
		ctx = context.Background()
	}

	delay := initialDelay
	var lastErr error

	for attempt := uint(0); attempt < maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled after %d attempt(s): %w", attempt, ctx.Err())
		default:
		}

		err := callback()
		if err == nil {
			return nil
		}
		lastErr = err

		// Exit early if the error is marked non-retryable
		if errors.Is(err, ErrNonRetryable) {
			return fmt.Errorf(operationFailedTemplate, attempt+1, err)
		}

		if attempt < maxRetries-1 {
			time.Sleep(delay)
			delay *= 2
		}
	}

	return fmt.Errorf(operationFailedTemplate, maxRetries, lastErr)
}
