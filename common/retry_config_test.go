package common

import (
	"testing"

	"github.com/agglayer/aggkit/common/types/mocks"
	"github.com/stretchr/testify/require"
)

func TestRetryPolicyGenericConfig_Validate(t *testing.T) {
	t.Run("validate ok", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{}
		mockCfg := mocks.NewRetryPolicyConfigurer(t)
		sut.SetCache(mockCfg)
		mockCfg.EXPECT().Validate().Return(nil)
		require.NoError(t, sut.Validate())
	})
	t.Run("validate error factory error", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{
			Mode: "no-exists",
		}
		require.ErrorContains(t, sut.Validate(), "unknown mode")
	})
}

func TestRetryPolicyGenericConfig_NewRetryHandler(t *testing.T) {
	t.Run("NewRetryHandler ok", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{}
		mockCfg := mocks.NewRetryPolicyConfigurer(t)
		sut.SetCache(mockCfg)
		mockCfg.EXPECT().NewRetryHandler().Return(nil, nil)
		handler, err := sut.NewRetryHandler()
		require.NoError(t, err)
		require.Nil(t, handler)
	})
	t.Run("NewRetryHandler factory error", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{
			Mode: "no-exists",
		}
		handler, err := sut.NewRetryHandler()
		require.ErrorContains(t, err, "unknown mode")
		require.Nil(t, handler)
	})
}

func TestRetryPolicyGenericConfig_Brief(t *testing.T) {
	t.Run("Brief ok", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{}
		mockCfg := mocks.NewRetryPolicyConfigurer(t)
		sut.SetCache(mockCfg)
		mockCfg.EXPECT().Brief().Return("mock brief")
		require.Equal(t, "/mock brief", sut.Brief())
	})
	t.Run("Brief factory error", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{
			Mode: "no-exists",
		}
		require.Equal(t,
			"RetryPolicyConfig{Error: invalid retry config mode: unknown mode no-exists}",
			sut.Brief())
	})
}

func TestRetryPolicyGenericConfig_String(t *testing.T) {
	t.Run("String ok", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{}
		mockCfg := mocks.NewRetryPolicyConfigurer(t)
		sut.SetCache(mockCfg)
		mockCfg.EXPECT().String().Return("mock String")
		require.Equal(t, "RetryPolicyConfig{Mode: , Config: mock String}", sut.String())
	})
	t.Run("String factory error", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{
			Mode: "no-exists",
		}
		require.Equal(t,
			"RetryPolicyConfig{Error: invalid retry config mode: unknown mode no-exists}",
			sut.String())
	})
}

func TestRetryPolicyGenericConfig_Cache(t *testing.T) {
	t.Run("CleanCache", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{
			Mode: "no-exists",
		}
		mockCfg := mocks.NewRetryPolicyConfigurer(t)
		sut.SetCache(mockCfg)
		i, err := sut.Factory()
		require.NoError(t, err)
		require.Equal(t, mockCfg, i)
		sut.CleanCache()
		_, err = sut.Factory()
		require.Error(t, err)
	})
}

func TestRetryPolicyGenericConfig_Factory(t *testing.T) {
	t.Run("factory mode=empty", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{}
		cfg, err := sut.Factory()
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.Equal(t, "RetryDelaysConfig{Delays: [], MaxRetries: 0}", cfg.String())
	})
	t.Run("factory mode=delays", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{
			Mode: RetryConfigModeDelays,
		}
		cfg, err := sut.Factory()
		require.NoError(t, err)
		require.NotNil(t, cfg)
		require.Equal(t, "RetryDelaysConfig{Delays: [], MaxRetries: 0}", cfg.String())
	})
	t.Run("factory mode=delays with errrors", func(t *testing.T) {
		sut := &RetryPolicyGenericConfig{
			Mode:       RetryConfigModeDelays,
			MaxRetries: -2,
		}
		_, err := sut.Factory()
		require.Error(t, err)
	})
}
