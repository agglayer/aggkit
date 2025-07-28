package common

import (
	"fmt"

	commontypes "github.com/agglayer/aggkit/common/types"
	"github.com/agglayer/aggkit/config/types"
)

var _ commontypes.RetryPolicyConfigurer = (*RetryDelaysConfig)(nil)

// RetryDelaysConfig defines the configuration for retry delays.
type RetryDelaysConfig struct {
	// MaxRetries is the maximum number of retries to attempt.
	// if MaxRetries is -1, it means infinite retries.
	// if MaxRetries is 0, it means no retries will be attempted.
	MaxRetries int
	// Delays is a list of durations to wait before each retry.
	// If there are more retry attempts than items in the list, the last item
	// in the list is reused for all subsequent attempts.
	Delays []types.Duration
}

// New creates a new instance of RetryDelaysConfig based on the generic retry policy configuration.
func NewRetryDelaysConfig(cfg *RetryPolicyGenericConfig) (commontypes.RetryPolicyConfigurer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: cannot create RetryDelaysConfig from nil", ErrInvalidConfig)
	}
	res := &RetryDelaysConfig{
		Delays:     cfg.Delays,
		MaxRetries: cfg.MaxRetries,
	}
	return res, res.Validate()
}

// RetryHandler returns a object that implements the logic
func (r *RetryDelaysConfig) NewRetryHandler() (commontypes.RetryHandler, error) {
	return NewRetryHandler(r.Delays, r.MaxRetries), nil
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
		return fmt.Errorf("%w: max retries %d cannot be less than %d",
			ErrInvalidConfig, r.MaxRetries, MaxAttemptsInfinite)
	}
	return nil
}

func (r *RetryDelaysConfig) String() string {
	if r == nil {
		return "RetryDelaysConfig{nil}"
	}
	var maxRetriesStr string
	switch r.MaxRetries {
	case MaxAttemptsInfinite:
		maxRetriesStr = "INFINITE"
	case 0:
		maxRetriesStr = "NO RETRIES"
	default:
		maxRetriesStr = fmt.Sprintf("%d", r.MaxRetries)
	}
	return fmt.Sprintf("RetryDelaysConfig{Delays: %v, MaxRetries: %s}", r.Delays, maxRetriesStr)
}

func (r *RetryDelaysConfig) Brief() string {
	return "RetryDelaysConfig"
}
