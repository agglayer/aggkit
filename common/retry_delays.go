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

const (
	MaxAttemptsInfinite = -1 // MaxAttemptsInfinite means infinite retries are allowed
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
	// if MaxAttempts is -1, it means infinite retries.
	// if MaxAttempts is 0, it means no retries will be attempted.
	MaxAttempts int `mapstructure:"MaxAttempts"`
}

// Validate checks if the RetryDelays configuration is valid.
func (r *RetryDelays) Validate() error {
	// nil means no retries at all
	if r == nil {
		return nil
	}
	if len(r.Delays) == 0 && r.MaxAttempts == 0 {
		return nil
	}
	if len(r.Delays) == 0 {
		return fmt.Errorf("%w: retry delays cannot be empty if there are retries", ErrInvalidConfig)
	}
	for _, delay := range r.Delays {
		if delay.Duration <= 0 {
			return fmt.Errorf("%w: retry delay must be greater than zero, got %s",
				ErrInvalidConfig, delay.Duration)
		}
	}
	if r.MaxAttempts < MaxAttemptsInfinite {
		return fmt.Errorf("%w: max retries cannot %d",
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
	return r != nil && r.MaxAttempts == MaxAttemptsInfinite
}

func (r *RetryDelays) NoRetries() bool {
	// No Retries is MaxAttempts is 0 (just 1 first try)
	return r == nil || r.MaxAttempts == 0
}

// Delay returns the delay for the given attempt.
func (r *RetryDelays) Delay(attempt int) time.Duration {
	if r == nil || len(r.Delays) == 0 {
		return 0
	}
	return r.Delays[min(attempt, len(r.Delays)-1)].Duration
}

// MustExecuteAttempt returns true if must execute `attempt`
func (r *RetryDelays) MustExecuteAttempt(attempt int) bool {
	if r.InfiniteRetries() {
		return true
	}
	if r == nil {
		return attempt == 0
	}
	return attempt <= r.MaxAttempts
}

// StringAttemp returns the string representation of the number of attempts.
func (r *RetryDelays) StringAttemp(attempt int) string {
	if r.InfiniteRetries() {
		return fmt.Sprintf("%d/INFINITE", attempt+1)
	}
	if r.NoRetries() {
		return fmt.Sprintf("%d/NO RETRIES", attempt+1)
	}
	return fmt.Sprintf("%d/%d", attempt+1, r.MaxAttempts)
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
	var err error
	attempt := 0
	for attempt := 0; retryDelaysConfig.MustExecuteAttempt(attempt); attempt++ {
		delay := retryDelaysConfig.Delay(attempt)
		logFunc("executing %s try %s (next delay: %s)",
			name, retryDelaysConfig.StringAttemp(attempt), delay.String())
		// Execute payload
		var result T
		result, err = payloadFunc()
		if err == nil {
			logFunc("successful run %s in try %s",
				name, retryDelaysConfig.StringAttemp(attempt))
			return result, nil
		}
		if errors.Is(err, ErrAbort) {
			logFunc("aborting execution of %s, try %s due to error: %v",
				name,
				retryDelaysConfig.StringAttemp(attempt),
				err)
			return result, err
		}

		logFunc("fails execution of %s try %s. delay %s.  due to error: %v",
			name, retryDelaysConfig.StringAttemp(attempt),
			delay, err)

		select {
		case <-ctx.Done():
			logFunc("executing %s try %d/%d was canceled",
				name, retryDelaysConfig.StringAttemp(attempt))
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
