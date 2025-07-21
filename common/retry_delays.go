package common

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/config/types"
)

var (
	// If this error is in the returned error, it means that the function should be aborted
	// and no more retries should be attempted.
	ErrAbort          = fmt.Errorf("abort")
	ErrInvalidConfig  = fmt.Errorf("invalid retry delays config")
	ErrExecutionFails = fmt.Errorf("execution fails after retries exceeded")
)

// RetryDelays is a struct that holds the retry delays and the maximum number of retries.
// It implements the RetryDelayer interface, which allows executing a function with retry logic.
// The delays are specified as a slice of types.Duration, and the maximum number of retries can be set.
// If maxRetries is set to 0, it means infinite retries are allowed.
//
// You can also abort the retrying wrapping the error ErrAbort into the result
// this is useful if there are some conditions that should not be retried
type RetryDelays struct {
	Delays []types.Duration `mapstructure:"Delays"`
	// MaxAttempts is the maximum number of retries to attempt.
	// if MaxAttempts is 0, it means infinite retries.
	// if MaxAttempts is 1, it means no retries will be attempted.
	MaxAttempts int `mapstructure:"MaxAttempts"`
}

// Validate checks if the RetryDelays configuration is valid.
func (r *RetryDelays) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: retry delays cannot be nil.", ErrInvalidConfig)
	}
	if len(r.Delays) == 0 {
		return fmt.Errorf("%w: retry delays cannot be empty.", ErrInvalidConfig)
	}
	for _, delay := range r.Delays {
		if delay.Duration <= 0 {
			return fmt.Errorf("%w: retry delay must be greater than zero, got %s.",
				ErrInvalidConfig, delay.Duration)
		}
	}
	if r.MaxAttempts < 0 {
		return fmt.Errorf("%w: max retries cannot be negative, got %d.",
			ErrInvalidConfig, r.MaxAttempts)
	}
	return nil
}

// String returns a string representation of the RetryDelays struct.
func (r *RetryDelays) String() string {
	if r == nil {
		return "RetryDelays{nil}"
	}
	return fmt.Sprintf("RetryDelays{Delays: %v, MaxRetries: %d}", r.Delays, r.MaxAttempts)
}

// InfiniteRetries return true if the configuration allows infinite retries.
func (r *RetryDelays) InfiniteRetries() bool {
	// Infinite retries are allowed if MaxAttempts is 0.
	return r.MaxAttempts == 0
}

// Delay returns the delay for the given attempt.
func (r *RetryDelays) Delay(attempt int) time.Duration {
	if r == nil || len(r.Delays) == 0 {
		return 0
	}
	return r.Delays[min(attempt, len(r.Delays)-1)].Duration
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
func Execute[T any](retryDelaysConfig *RetryDelays,
	ctx context.Context,
	logFunc func(format string, args ...interface{}),
	name string,
	payloadFunc func() (T, error)) (T, error) {
	var zero T
	if err := retryDelaysConfig.Validate(); err != nil {
		return zero, err
	}
	if logFunc == nil {
		// if logger is nil, we create a silent logger
		logFunc = silentLog
	}

	attempt := 0
	for attempt := 0; retryDelaysConfig.InfiniteRetries() || attempt < retryDelaysConfig.MaxAttempts; attempt++ {
		delay := retryDelaysConfig.Delay(attempt)
		logFunc("executing %s try %d/%d (next delay: %s)",
			name, attempt+1, retryDelaysConfig.MaxAttempts, delay.String())
		// Execute payload
		result, err := payloadFunc()
		if err == nil {
			logFunc("successful run %s in try %d",
				name, attempt+1)
			return result, nil
		}
		if errors.Is(err, ErrAbort) {
			logFunc("aborting execution of %s due to error: %v",
				name, err)
			return result, err
		}

		logFunc("fails execution of %s try %d/%d due to error: %v",
			name, attempt+1, retryDelaysConfig.MaxAttempts, err)
		attempt++
		select {
		case <-ctx.Done():
			logFunc("executing %s try %d/%d was canceled",
				name, attempt, retryDelaysConfig.MaxAttempts)
			return zero, ctx.Err()
		case <-time.After(delay):
			continue
		}
	}
	logFunc("fails to execute %s after %d retries",
		name, attempt)
	return zero, fmt.Errorf("fails to execute %s after %d retries. %w",
		name, attempt, ErrExecutionFails)
}
