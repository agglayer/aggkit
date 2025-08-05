package common

import (
	"fmt"
	"math"
	"time"

	commontypes "github.com/agglayer/aggkit/common/types"
	"github.com/agglayer/aggkit/config/types"
)

var _ commontypes.RetryPolicyConfigurer = (*RetryBackoffConfig)(nil)

type RetryBackoffConfig struct {
	InitialBackoff    types.Duration
	MaxBackoff        types.Duration
	BackoffMultiplier float64
	MaxRetries        int
}

func NewRetryBackoffConfig(cfg *RetryPolicyGenericConfig) (commontypes.RetryPolicyConfigurer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: cannot create RetryBackoffConfig from nil", ErrInvalidConfig)
	}

	res := &RetryBackoffConfig{
		InitialBackoff:    cfg.InitialBackoff,
		MaxBackoff:        cfg.MaxBackoff,
		BackoffMultiplier: cfg.BackoffMultiplier,
		MaxRetries:        cfg.MaxRetries,
	}
	return res, res.Validate()
}

func (r *RetryBackoffConfig) NewRetryHandler() (commontypes.RetryHandler, error) {
	delays := []types.Duration{}
	for attempt := 0; r.MaxRetries == MaxAttemptsInfinite || attempt < r.MaxRetries; attempt++ {
		backoff := float64(r.InitialBackoff.Duration) * math.Pow(r.BackoffMultiplier,
			float64(attempt))
		delay := time.Duration(math.Min(backoff, float64(r.MaxBackoff.Duration)))
		delays = append(delays, types.Duration{Duration: delay})

		if backoff > float64(r.MaxBackoff.Duration) {
			break
		}
	}

	return NewRetryHandler(delays, r.MaxRetries), r.Validate()
}

func (r *RetryBackoffConfig) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: RetryBackoffConfig is nil", ErrInvalidConfig)
	}

	if r.MaxRetries < MaxAttemptsInfinite {
		return fmt.Errorf("%w: RetryBackoffConfig max retries %d cannot be less than %d",
			ErrInvalidConfig, r.MaxRetries, MaxAttemptsInfinite)
	}

	if r.BackoffMultiplier <= 0.0 {
		return fmt.Errorf("%w: RetryBackoffConfig backoff multiplier must be greater than zero, got %f",
			ErrInvalidConfig, r.BackoffMultiplier)
	}

	if r.InitialBackoff.Duration <= 0 {
		return fmt.Errorf("initial backoff must be positive, got %s", r.InitialBackoff.Duration)
	}

	if r.MaxBackoff.Duration <= 0 {
		return fmt.Errorf("max backoff must be positive, got %s", r.MaxBackoff.Duration)
	}

	if r.MaxBackoff.Duration < r.InitialBackoff.Duration {
		return fmt.Errorf("max backoff %s must be greater than or equal to initial backoff %s",
			r.MaxBackoff.Duration, r.InitialBackoff.Duration)
	}

	if r.BackoffMultiplier <= 1.0 {
		return fmt.Errorf("backoff multiplier must be greater than 1.0, got %f", r.BackoffMultiplier)
	}

	return nil
}

func (r *RetryBackoffConfig) String() string {
	if r == nil {
		return "RetryBackoffConfig{nil}"
	}
	return fmt.Sprintf("RetryBackoffConfig{InitialBackoff: %s, MaxBackoff: %s, BackoffMultiplier: %f, MaxRetries: %d}",
		r.InitialBackoff, r.MaxBackoff, r.BackoffMultiplier, r.MaxRetries)
}

func (r *RetryBackoffConfig) Brief() string {
	return "RetryBackoffConfig"
}
