package common

import (
	"context"
	"errors"
	"fmt"
	"time"

	commontypes "github.com/agglayer/aggkit/common/types"
	"github.com/agglayer/aggkit/config/types"
)

var (
	// If this error is in the returned error, it means that the function should be aborted
	// and no more retries should be attempted.
	ErrAbort          = fmt.Errorf("abort")
	ErrInvalidConfig  = fmt.Errorf("invalid retry delays config")
	ErrExecutionFails = fmt.Errorf("execution fails after retries exceeded")
)

const (
	MaxAttemptsInfinite = -1 // MaxAttemptsInfinite means infinite retries are allowed
)

// RetryHandlerDelays is a struct that holds the retry delays and the maximum number of retries.
// It implements the RetryDelayer interface, which allows executing a function with retry logic.
// The delays are specified as a slice of types.Duration, and the maximum number of retries can be set.
// If maxRetries is set to 0, it means infinite retries are allowed.
//
// You can also abort the retrying wrapping the error ErrAbort into the result
// this is useful if there are some conditions that should not be retried
type RetryHandlerDelays struct {
	RetryDelaysConfig
}

// NewRetryHandler creates a new RetryHandler with the specified delays and maximum retries.
func NewRetryHandler(delays []types.Duration, maxRetries int) *RetryHandlerDelays {
	return &RetryHandlerDelays{
		RetryDelaysConfig: RetryDelaysConfig{
			Delays:     delays,
			MaxRetries: maxRetries,
		},
	}
}

// Validate checks if the RetryDelays configuration is valid.
func (r *RetryHandlerDelays) Validate() error {
	return r.RetryDelaysConfig.Validate()
}

// String returns a string representation of the RetryDelays struct.
func (r *RetryHandlerDelays) String() string {
	if r == nil {
		return "RetryDelays{nil}"
	}
	return fmt.Sprintf("RetryDelays{%s}", r.RetryDelaysConfig.String())
}

// InfiniteRetries return true if the configuration allows infinite retries.
func (r *RetryHandlerDelays) InfiniteRetries() bool {
	// Infinite retries are allowed if MaxAttempts is 0.
	return r != nil && r.MaxRetries == MaxAttemptsInfinite
}

func (r *RetryHandlerDelays) NoRetries() bool {
	// No Retries is MaxAttempts is 0 (just 1 first try)
	return r == nil || r.MaxRetries == 0
}

// Delay returns the delay for the given attempt.
func (r *RetryHandlerDelays) Delay(attempt int) time.Duration {
	if r == nil || len(r.Delays) == 0 {
		return 0
	}
	return r.Delays[min(attempt, len(r.Delays)-1)].Duration
}

// MustExecuteAttempt returns true if must execute `attempt`
func (r *RetryHandlerDelays) MustExecuteAttempt(attempt int) bool {
	if r.InfiniteRetries() {
		return true
	}
	if r == nil {
		return attempt == 0
	}
	return attempt <= r.MaxRetries
}

// StringAttempt returns the string representation of the number of attempts.
func (r *RetryHandlerDelays) StringAttempt(attempt int) string {
	if r.InfiniteRetries() {
		return fmt.Sprintf("%d/INFINITE", attempt+1)
	}
	if r.NoRetries() {
		return fmt.Sprintf("%d/NO RETRIES", attempt+1)
	}
	return fmt.Sprintf("%d/%d", attempt+1, r.MaxRetries)
}

func silentLog(format string, args ...interface{}) {
}

// Execute executes the provided function with retry logic.
// retryDelaysConfig: it's the RetryDelays struct that holds the retry delays and the maximum number of retries.
// ctx: the context to use for cancellation.
// logFunc: a function to log messages, if nil a silent logger will be used.
// name: the name of the operation, used for logging.
// fn: the function to execute, it should return a result of type T and an error.
// If the function returns an error that is wrapped with ErrAbort,
// the execution will be aborted and no more retries will be attempted.
func Execute[T any](retryHandler commontypes.RetryHandler,
	ctx context.Context,
	logFunc func(format string, args ...interface{}),
	name string,
	payloadFunc func() (T, error)) (T, error) {
	var zero T
	if retryHandler == nil {
		retryHandler = NewRetryHandler(nil, 0) // no retries
	}
	if err := retryHandler.Validate(); err != nil {
		return zero, err
	}
	if logFunc == nil {
		// if logger is nil, we create a silent logger
		logFunc = silentLog
	}
	var err error
	var attempt int
	for attempt = 0; retryHandler.MustExecuteAttempt(attempt); attempt++ {
		delay := retryHandler.Delay(attempt)
		logFunc("executing %s try %s (next delay: %s)",
			name, retryHandler.StringAttempt(attempt), delay.String())
		// Execute payload
		var result T
		result, err = payloadFunc()
		if err == nil {
			logFunc("successful run %s in try %s",
				name, retryHandler.StringAttempt(attempt))
			return result, nil
		}
		if errors.Is(err, ErrAbort) {
			logFunc("aborting execution of %s, try %s due to error: %v",
				name,
				retryHandler.StringAttempt(attempt),
				err)
			return result, err
		}

		logFunc("fails execution of %s try %s. delay %s.  due to error: %v",
			name, retryHandler.StringAttempt(attempt),
			delay, err)

		select {
		case <-ctx.Done():
			logFunc("executing %s try %d/%d was canceled",
				name, retryHandler.StringAttempt(attempt))
			return zero, ctx.Err()
		case <-time.After(delay):
			continue
		}
	}
	logFunc("fails to execute %s after %d retries, LastError: %v",
		name, attempt, err)
	return zero, fmt.Errorf("%w: fails to execute %s after %d retries. LastError: %w",
		ErrExecutionFails, name, attempt, err)
}
