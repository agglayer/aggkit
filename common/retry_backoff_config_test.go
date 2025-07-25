package common

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/config/types"
	"github.com/stretchr/testify/require"
)

var (
	retryBackoffConfigExample = &RetryBackoffConfig{
		InitialBackoff:    types.Duration{Duration: 10 * time.Millisecond},
		MaxBackoff:        types.Duration{Duration: 100 * time.Millisecond},
		BackoffMultiplier: 2.0,
		MaxRetries:        3,
	}
)

func TestRetryBackoffConfig_NewRetryBackoffConfig(t *testing.T) {
	t.Run("cfg=nil", func(t *testing.T) {
		cfg, err := NewRetryBackoffConfig(nil)
		require.Error(t, err)
		require.Nil(t, cfg)
	})
	t.Run("cfg!=nil", func(t *testing.T) {
		cfg, err := NewRetryBackoffConfig(&RetryPolicyGenericConfig{
			InitialBackoff:    types.Duration{Duration: 10 * time.Millisecond},
			MaxBackoff:        types.Duration{Duration: 100 * time.Millisecond},
			BackoffMultiplier: 2.0,
			MaxRetries:        3,
		})
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.IsType(t, &RetryBackoffConfig{}, cfg)
		require.Equal(t, retryBackoffConfigExample.String(), cfg.String())
	})
}

func TestRetryBackoffConfig_NewRetryHandler(t *testing.T) {
	t.Run("NewRetryHandler ok", func(t *testing.T) {
		handler, err := retryBackoffConfigExample.NewRetryHandler()
		require.NoError(t, err)
		require.NotNil(t, handler)
		require.Equal(t, "RetryHandlerDelays{RetryDelaysConfig{Delays: [10ms 20ms 40ms], MaxRetries: 3}}", handler.String())
	})

	t.Run("NewRetryHandler reach MaxBackoff", func(t *testing.T) {
		sut := newRetryBackoffConfigForTest(t)
		sut.MaxRetries = 200
		handler, err := sut.NewRetryHandler()
		require.NoError(t, err)
		require.NotNil(t, handler)
		require.Equal(t, "RetryHandlerDelays{RetryDelaysConfig{Delays: [10ms 20ms 40ms 80ms 100ms], MaxRetries: 200}}", handler.String())
	})

	t.Run("NewRetryHandler MaxRetries=0", func(t *testing.T) {
		sut := newRetryBackoffConfigForTest(t)
		sut.MaxRetries = 0
		handler, err := sut.NewRetryHandler()
		require.NoError(t, err)
		require.NotNil(t, handler)
		require.Equal(t, "RetryHandlerDelays{RetryDelaysConfig{Delays: [], MaxRetries: NO RETRIES}}", handler.String())
	})

	t.Run("NewRetryHandler MaxRetries=-1", func(t *testing.T) {
		sut := newRetryBackoffConfigForTest(t)
		sut.MaxRetries = MaxAttemptsInfinite
		handler, err := sut.NewRetryHandler()
		require.NoError(t, err)
		require.NotNil(t, handler)
		require.Equal(t, "RetryHandlerDelays{RetryDelaysConfig{Delays: [10ms 20ms 40ms 80ms 100ms], MaxRetries: INFINITE}}", handler.String())
	})
}

func TestRetryBackoffConfig_String(t *testing.T) {
	require.Equal(t, "RetryBackoffConfig{InitialBackoff: 10ms, MaxBackoff: 100ms, BackoffMultiplier: 2.000000, MaxRetries: 3}",
		retryBackoffConfigExample.String())
}

func TestRetryBackoffConfig_Brief(t *testing.T) {
	require.Equal(t, "RetryBackoffConfig", retryBackoffConfigExample.Brief())
}

func TestRetryBackoffConfig_Validate(t *testing.T) {
	require.NoError(t, retryBackoffConfigExample.Validate())
	cfg2 := newRetryBackoffConfigForTest(t)
	cfg2.MaxRetries = -2
	require.ErrorContains(t, cfg2.Validate(), "max retries -2 cannot be less than -1")
	cfg2 = newRetryBackoffConfigForTest(t)
	cfg2.BackoffMultiplier = 0.0
	require.ErrorContains(t, cfg2.Validate(), "backoff multiplier must be greater than zero")
}

func newRetryBackoffConfigForTest(t *testing.T) *RetryBackoffConfig {
	t.Helper()
	return &RetryBackoffConfig{
		InitialBackoff:    types.Duration{Duration: 10 * time.Millisecond},
		MaxBackoff:        types.Duration{Duration: 100 * time.Millisecond},
		BackoffMultiplier: 2.0,
		MaxRetries:        3,
	}
}
