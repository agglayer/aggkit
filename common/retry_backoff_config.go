package common

import (
	"fmt"
	"math"
	"time"

	"github.com/agglayer/aggkit/config/types"
)

var _ RetryPolicyConfigurer = (*RetryBackoffConfig)(nil)

type RetryBackoffConfig struct {
	InitialBackoff    types.Duration
	MaxBackoff        types.Duration
	BackoffMultiplier float64
	MaxRetries        int
}

func (r *RetryBackoffConfig) NewRetryHandler() *RetryHandler {
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

	return NewRetryHandler(delays, r.MaxRetries)
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
