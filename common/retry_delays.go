package common

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/config/types"
)

var (
	// If this error in in the erturned error, it means that the function should be aborted
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
	Delays []types.Duration
	// MaxAttempts is the maximum number of retries to attempt.
	// if MaxAttempts is 0, it means infinite retries.
	// if MaxAttempts is 1, it means no retries will be attempted.
	MaxAttempts int
}

func (r *RetryDelays) Validate() error {
	if r == nil {
		return fmt.Errorf("retry delays cannot be nil. %w", ErrInvalidConfig)
	}
	if len(r.Delays) == 0 {
		return fmt.Errorf("retry delays cannot be empty. %w", ErrInvalidConfig)
	}
	for _, delay := range r.Delays {
		if delay.Duration <= 0 {
			return fmt.Errorf("retry delay must be greater than zero, got %s. %w",
				delay.Duration, ErrInvalidConfig)
		}
	}
	if r.MaxAttempts < 0 {
		return fmt.Errorf("max retries cannot be negative, got %d. %w",
			r.MaxAttempts, ErrInvalidConfig)
	}
	return nil
}

func (r *RetryDelays) String() string {
	if r == nil {
		return "RetryDelays is nil"
	}
	return fmt.Sprintf("RetryDelays{Delays: %v, MaxRetries: %d}", r.Delays, r.MaxAttempts)
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
	fn func() (T, error)) (T, error) {
	var zero T
	if err := retryDelaysConfig.Validate(); err != nil {
		return zero, err
	}
	if logFunc == nil {
		// if logger is nil, we create a silent logger
		logFunc = func(format string, args ...interface{}) {}
	}

	retries := 0
	for {
		if retryDelaysConfig.MaxAttempts > 0 && retries >= retryDelaysConfig.MaxAttempts {
			break
		}
		var delay types.Duration
		if retries < len(retryDelaysConfig.Delays) {
			delay = retryDelaysConfig.Delays[retries]
		} else {
			delay = retryDelaysConfig.Delays[len(retryDelaysConfig.Delays)-1]
		}
		logFunc("executing %s try %d/%d (next delay: %s)",
			name, retries+1, retryDelaysConfig.MaxAttempts, delay.String())
		result, err := fn()
		if err != nil && errors.Is(err, ErrAbort) {
			logFunc("aborting execution of %s due to error: %v",
				name, err)
			return result, err
		}
		if err != nil {
			// The function must log this error if it wants to
			retries++
			select {
			case <-ctx.Done():
				logFunc("executing %s try %d/%d was canceled",
					name, retries+1, retryDelaysConfig.MaxAttempts)
				return zero, ctx.Err()
			case <-time.After(delay.Duration):
				continue
			}
		} else {
			logFunc("successful run %s in try %d",
				name, retries+1)
			return result, nil
		}
	}
	logFunc("fails to execute %s after %d retries",
		name, retries)
	return zero, fmt.Errorf("fails to execute %s after %d retries. %w",
		name, retries, ErrExecutionFails)
}

// Execute executes the provided function with retry logic for a non return function
// check func Execute[T any] for more details
func (r *RetryDelays) Execute(ctx context.Context,
	logFunc func(format string, args ...interface{}),
	name string,
	fn func() error) error {
	_, err := Execute(r, ctx, logFunc, name,
		func() (struct{}, error) {
			err := fn()
			return struct{}{}, err
		})
	return err
}
