package common

import (
	"fmt"
	"time"

	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
)

var (
	TimeProvider = time.Now
)

// RateLimitConfig is the configuration for the rate limit, if NumRequests==0 or Interval==0, the rate limit is disabled
type RateLimitConfig struct {
	NumRequests int            `mapstructure:"NumRequests"`
	Interval    types.Duration `mapstructure:"Interval"`
}

// NewRateLimitConfig creates a new RateLimitConfig
func NewRateLimitConfig(numRequests int, period time.Duration) RateLimitConfig {
	return RateLimitConfig{
		NumRequests: numRequests,
		Interval:    types.Duration{Duration: period},
	}
}

// String returns a string representation of the RateLimitConfig
func (r RateLimitConfig) String() string {
	if !r.Enabled() {
		return "RateLimitConfig{Unlimited}"
	}
	return fmt.Sprintf("RateLimitConfig{NumRequests: %d, Period: %s}", r.NumRequests, r.Interval)
}

// Enabled returns true if the rate limit is enabled
func (r RateLimitConfig) Enabled() bool {
	return r.NumRequests > 0 && r.Interval.Duration > 0
}

// RateLimit is a rate limiter
type RateLimit struct {
	cfg RateLimitConfig
	// Calls realized in the current period
	calls []time.Time
}

// NewRateLimit creates a new RateLimit
func NewRateLimit(cfg RateLimitConfig) *RateLimit {
	return &RateLimit{
		cfg: cfg,
	}
}

// String returns a string representation of the RateLimit
func (r *RateLimit) String() string {
	if r == nil {
		return "RateLimit{nil}"
	}
	return fmt.Sprintf("RateLimit{cfg: %s, bucket len: %v}", r.cfg, len(r.calls))
}

// Call is used before making a call, it will sleep if the rate limit is reached if param allowToSleep is true
func (r *RateLimit) Call(msg string, allowToSleep bool) *time.Duration {
	if r == nil || !r.cfg.Enabled() {
		return nil
	}
	var returnSleepTime *time.Duration
	now := TimeProvider()
	r.cleanOutdatedCalls(now)
	if len(r.calls) >= r.cfg.NumRequests {
		sleepTime := r.cfg.Interval.Duration - TimeProvider().Sub(r.calls[0])
		if allowToSleep {
			if msg != "" {
				log.Debugf("Rate limit reached, sleeping for %s for %s", sleepTime, msg)
			}
			time.Sleep(sleepTime)
		} else {
			// If no sleep, ignore the call
			return &sleepTime
		}
		returnSleepTime = &sleepTime
	}
	r.calls = append(r.calls, now)
	return returnSleepTime
}

func (r *RateLimit) cleanOutdatedCalls(now time.Time) {
	for i, call := range r.calls {
		diff := now.Sub(call)
		if diff < r.cfg.Interval.Duration {
			r.calls = r.calls[i:]
			return
		}
	}
	r.calls = []time.Time{}
}
