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
	})
}

func TestRetryBackoffConfig_NewRetryHandler(t *testing.T) {
	t.Run("NewRetryHandler ok", func(t *testing.T) {
		sut := retryBackoffConfigExample
		handler, err := sut.NewRetryHandler()
		require.NoError(t, err)
		require.NotNil(t, handler)
	})
}

func TestRetryBackoffConfig_String(t *testing.T) {
	require.Equal(t, "RetryBackoffConfig{InitialBackoff: 10ms, MaxBackoff: 100ms, BackoffMultiplier: 2.000000, MaxRetries: 3}",
		retryBackoffConfigExample.String())
}

func TestRetryBackoffConfig_Brief(t *testing.T) {
	require.Equal(t, "RetryBackoffConfig", retryBackoffConfigExample.Brief())
}
