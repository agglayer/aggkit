package common

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/config/types"
)

// RetryDelays is a struct that holds the retry delays and the maximum number of retries.
// It implements the RetryDelayer interface, which allows executing a function with retry logic.
// The delays are specified as a slice of types.Duration, and the maximum number of retries can be set.
// If maxRetries is set to 0, it means infinite retries are allowed.
type RetryDelays struct {
	Delays []types.Duration
	// MaxAttempts is the maximum number of retries to attempt.
	// if MaxAttempts is 0, it means infinite retries.
	// if MaxAttempts is 1, it means no retries will be attempted.
	MaxAttempts int
}

func (r *RetryDelays) Validate() error {
	if r == nil {
		return fmt.Errorf("retry delays cannot be nil")
	}
	if len(r.Delays) == 0 {
		return fmt.Errorf("retry delays cannot be empty")
	}
	for _, delay := range r.Delays {
		if delay.Duration <= 0 {
			return fmt.Errorf("retry delay must be greater than zero, got %s", delay.Duration)
		}
	}
	if r.MaxAttempts < 0 {
		return fmt.Errorf("max retries cannot be negative, got %d", r.MaxAttempts)
	}
	return nil
}

func (r *RetryDelays) String() string {
	if r == nil {
		return "RetryDelays is nil"
	}
	return fmt.Sprintf("RetryDelays{Delays: %v, MaxRetries: %d}", r.Delays, r.MaxAttempts)
}

// Execute executes the provided function with retry logic for a non return function
func (r *RetryDelays) Execute(ctx context.Context,
	logger Logger, name string,
	fn func() error) error {
	_, err := Execute(r, ctx, logger, name,
		func() (struct{}, error) {
			err := fn()
			return struct{}{}, err
		})
	return err
}

func Execute[T any](r *RetryDelays, ctx context.Context,
	logger Logger, name string,
	fn func() (T, error)) (T, error) {
	var zero T
	if r == nil {
		return zero, fmt.Errorf("retry delays cannot be nil")
	}
	if logger == nil {
		// if logger is nil, we create a silent logger
		logger = NewSlientLogger()
	}

	retries := 0
	for {
		if r.MaxAttempts > 0 && retries >= r.MaxAttempts {
			break
		}
		var delay types.Duration
		if retries < len(r.Delays) {
			delay = r.Delays[retries]
		} else {
			delay = r.Delays[len(r.Delays)-1]
		}
		logger.Infof("executing %s try %d/%d (next delay: %s)",
			name, retries+1, r.MaxAttempts, delay.String())
		result, err := fn()
		if err != nil {
			retries++
			select {
			case <-ctx.Done():
				logger.Infof("executing %s try %d/%d was canceled",
					name, retries+1, r.MaxAttempts)
				return zero, ctx.Err()
			case <-time.After(delay.Duration):
				continue
			}
		} else {
			logger.Infof("sucessful run %s in try %d",
				name, retries+1)
			return result, nil
		}
	}
	return zero, fmt.Errorf("fails to execute %s after %d retries",
		name, retries)
}
