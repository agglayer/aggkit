package common

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/config/types"
	"github.com/stretchr/testify/require"
)

func TestRetryBackoffConfig_NewRetryBackoffConfig(t *testing.T) {
	t.Run("undefined config", func(t *testing.T) {
		cfg, err := NewRetryBackoffConfig(nil)
		require.Error(t, err)
		require.Nil(t, cfg)
	})

	t.Run("valid config", func(t *testing.T) {
		cfg, err := NewRetryBackoffConfig(
			&RetryPolicyGenericConfig{
				InitialBackoff:    types.Duration{Duration: 10 * time.Millisecond},
				MaxBackoff:        types.Duration{Duration: 100 * time.Millisecond},
				BackoffMultiplier: 2.0,
				MaxRetries:        3,
			})
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.IsType(t, &RetryBackoffConfig{}, cfg)
		expectedCfg := newRetryBackoffConfigForTest(t)
		require.Equal(t, expectedCfg.String(), cfg.String())
	})
}

func TestRetryBackoffConfig_NewRetryHandler(t *testing.T) {
	t.Run("NewRetryHandler ok", func(t *testing.T) {
		handler, err := newRetryBackoffConfigForTest(t).NewRetryHandler()
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
		newRetryBackoffConfigForTest(t).String())
}

func TestRetryBackoffConfig_Brief(t *testing.T) {
	require.Equal(t, "RetryBackoffConfig", newRetryBackoffConfigForTest(t).Brief())
}

func TestRetryBackoffConfig_Validate(t *testing.T) {
	tests := []struct {
		name        string
		modifyFn    func(cfg *RetryBackoffConfig)
		expectError string
	}{
		{
			name:        "valid config",
			modifyFn:    nil,
			expectError: "",
		},
		{
			name: "negative max retries",
			modifyFn: func(cfg *RetryBackoffConfig) {
				cfg.MaxRetries = -2
			},
			expectError: "max retries -2 cannot be less than -1",
		},
		{
			name: "invalid initial backoff",
			modifyFn: func(cfg *RetryBackoffConfig) {
				cfg.InitialBackoff = types.NewDuration(0)
			},
			expectError: "initial backoff must be greater than 0",
		},
		{
			name: "invalid max backoff",
			modifyFn: func(cfg *RetryBackoffConfig) {
				cfg.MaxBackoff = types.NewDuration(0)
			},
			expectError: "max backoff must be greater than 0",
		},
		{
			name: "invalid max backoff smaller than initial backoff",
			modifyFn: func(cfg *RetryBackoffConfig) {
				cfg.InitialBackoff = types.NewDuration(2 * time.Second)
				cfg.MaxBackoff = types.NewDuration(time.Second)
			},
			expectError: "max backoff 1s must be greater than or equal to initial backoff 2s",
		},
		{
			name: "zero backoff multiplier",
			modifyFn: func(cfg *RetryBackoffConfig) {
				cfg.BackoffMultiplier = 0.0
			},
			expectError: "backoff multiplier must be greater than 1.0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newRetryBackoffConfigForTest(t)
			if tc.modifyFn != nil {
				tc.modifyFn(cfg)
			}

			err := cfg.Validate()
			if tc.expectError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.expectError)
			}
		})
	}
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
