package common

import (
	"fmt"

	"github.com/agglayer/aggkit/config/types"
)

var _ RetryPolicyConfigurer = (*RetryDelaysConfig)(nil)

// RetryDelaysConfig defines the configuration for retry delays.
type RetryDelaysConfig struct {
	// MaxRetries is the maximum number of retries to attempt.
	// if MaxRetries is -1, it means infinite retries.
	// if MaxRetries is 0, it means no retries will be attempted.
	MaxRetries int `mapstructure:"MaxRetries"`
	// Delays is a list of durations to wait before each retry.
	// if there are more attempts that item o the list is used the last one
	Delays []types.Duration `mapstructure:"Delays"`
}

// RetryHandler returns a object that implements the logic
func (r *RetryDelaysConfig) NewRetryHandler() *RetryHandler {
	return NewRetryHandler(r.Delays, r.MaxRetries)
}

func (r *RetryDelaysConfig) Validate() error {
	// nil means no retries at all
	if r == nil {
		return nil
	}
	if len(r.Delays) == 0 && r.MaxRetries == 0 {
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
	if r.MaxRetries < MaxAttemptsInfinite {
		return fmt.Errorf("%w: max retries cannot %d be less than %d",
			ErrInvalidConfig, r.MaxRetries, MaxAttemptsInfinite)
	}
	return nil
}

func (r *RetryDelaysConfig) String() string {
	if r == nil {
		return "RetryDelaysConfig{nil}"
	}
	return fmt.Sprintf("RetryDelaysConfig{Delays: %v, MaxRetries: %d}", r.Delays, r.MaxRetries)
}

func (r *RetryDelaysConfig) Brief() string {
	return "RetryDelaysConfig"
}
