package common

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// ExponentialBackoffConfig configures an ExponentialBackoff.
type ExponentialBackoffConfig struct {
	// Initial is the delay of the first attempt; must be > 0
	Initial time.Duration
	// Max is the hard upper bound of any returned delay; must be >= Initial
	Max time.Duration
	// Factor is the growth per attempt; must be > 1
	Factor float64
	// Jitter is the symmetric relative jitter in [0, 1); 0.2 means +-20%
	Jitter float64
}

// Validate reports whether the configuration is usable, wrapping ErrInvalidConfig.
func (c ExponentialBackoffConfig) Validate() error {
	if c.Initial <= 0 {
		return fmt.Errorf("%w: backoff Initial must be > 0 (got %s)", ErrInvalidConfig, c.Initial)
	}
	if c.Max < c.Initial {
		return fmt.Errorf("%w: backoff Max (%s) must be >= Initial (%s)", ErrInvalidConfig, c.Max, c.Initial)
	}
	if !(c.Factor > 1) || math.IsInf(c.Factor, 0) {
		return fmt.Errorf("%w: backoff Factor must be > 1 (got %v)", ErrInvalidConfig, c.Factor)
	}
	if !(c.Jitter >= 0 && c.Jitter < 1) {
		return fmt.Errorf("%w: backoff Jitter must be in [0, 1) (got %v)", ErrInvalidConfig, c.Jitter)
	}
	return nil
}

// SleepFunc sleeps for d or until ctx is done, returning ctx.Err() in the latter case.
type SleepFunc func(ctx context.Context, d time.Duration) error

// ExponentialBackoff yields capped, jittered exponential delays. Not safe for concurrent use.
type ExponentialBackoff struct {
	cfg       ExponentialBackoffConfig
	attempt   int
	randFloat func() float64
	sleep     SleepFunc
}

// ExponentialBackoffOption customises an ExponentialBackoff (mainly for tests).
type ExponentialBackoffOption func(*ExponentialBackoff)

// WithBackoffRandFloat replaces the jitter source; fn must return values in [0, 1).
func WithBackoffRandFloat(fn func() float64) ExponentialBackoffOption {
	return func(b *ExponentialBackoff) {
		if fn != nil {
			b.randFloat = fn
		}
	}
}

// WithBackoffSleeper replaces the function used by Sleep to wait.
func WithBackoffSleeper(fn SleepFunc) ExponentialBackoffOption {
	return func(b *ExponentialBackoff) {
		if fn != nil {
			b.sleep = fn
		}
	}
}

// NewExponentialBackoff creates an ExponentialBackoff after validating cfg.
func NewExponentialBackoff(
	cfg ExponentialBackoffConfig, opts ...ExponentialBackoffOption,
) (*ExponentialBackoff, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	b := &ExponentialBackoff{
		cfg:       cfg,
		randFloat: rand.Float64,
		sleep:     SleepContext,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b, nil
}

// Next returns the delay for the current attempt and advances the attempt counter.
//
//	base  = min(Initial * Factor^attempt, Max)
//	delay = base * (1 + Jitter*(2*r-1)), r in [0,1)
//	delay = min(delay, Max)
func (b *ExponentialBackoff) Next() time.Duration {
	maxF := float64(b.cfg.Max)
	base := float64(b.cfg.Initial) * math.Pow(b.cfg.Factor, float64(b.attempt))
	if base > maxF || math.IsInf(base, 0) || math.IsNaN(base) {
		base = maxF
	}
	if b.attempt < math.MaxInt32 {
		b.attempt++
	}
	delay := base * (1 + b.cfg.Jitter*(2*b.randFloat()-1))
	if delay > maxF {
		delay = maxF
	}
	return time.Duration(delay)
}

// Sleep calls Next and sleeps that long; it returns the delay and ctx.Err() if interrupted.
func (b *ExponentialBackoff) Sleep(ctx context.Context) (time.Duration, error) {
	d := b.Next()
	return d, b.sleep(ctx, d)
}

// Reset sets the attempt counter back to 0.
func (b *ExponentialBackoff) Reset() {
	b.attempt = 0
}

// Attempts returns the number of Next calls since construction or the last Reset.
func (b *ExponentialBackoff) Attempts() int {
	return b.attempt
}

// SleepContext sleeps for d using a timer, returning early with ctx.Err() when ctx is done.
func SleepContext(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
