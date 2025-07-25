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
	// TODO: check that implementation
	delays := []types.Duration{}
	for attempt := range r.MaxRetries {
		backoff := float64(r.InitialBackoff.Duration) * math.Pow(r.BackoffMultiplier,
			float64(attempt))
		if backoff > float64(r.MaxBackoff.Duration) {
			delays = append(delays, r.MaxBackoff)
			break
		}
		delays = append(delays, types.Duration{Duration: time.Duration(backoff)})
	}

	return NewRetryHandler(delays, r.MaxRetries), r.Validate()
}

func (r *RetryBackoffConfig) Validate() error {
	// TODO: check config
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
