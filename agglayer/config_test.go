package agglayer

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/stretchr/testify/require"
)

var (
	testValidConfigExampleNoCache = ClientConfig{
		GRPC: &aggkitgrpc.ClientConfig{
			URL:               "localhost:8080",
			MinConnectTimeout: types.Duration{Duration: time.Second},
		},
		Cached:             false,
		ConfigurationCache: nil,
	}
	testValidConfigExampleCache = ClientConfig{
		GRPC: &aggkitgrpc.ClientConfig{
			URL:               "localhost:8080",
			MinConnectTimeout: types.Duration{Duration: time.Second},
		},
		Cached: true,
		ConfigurationCache: &CacheConfig{
			TTL:                  types.Duration{Duration: time.Second},
			Capacity:             100,
			GetCertificateHeader: MethodPolicyCached,
		},
	}
	testValidConfigWithRateLimits = ClientConfig{
		GRPC: &aggkitgrpc.ClientConfig{
			URL:               "localhost:8080",
			MinConnectTimeout: types.Duration{Duration: time.Second},
		},
		Cached:             false,
		ConfigurationCache: nil,
		APIRateLimits: []APIRateLimitConfig{
			{
				MethodName: "SendCertificate",
				RateLimit: common.RateLimitConfig{
					NumRequests: 10,
					Interval:    types.Duration{Duration: time.Minute},
				},
			},
		},
	}
)

func TestClientConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		config      *ClientConfig
		expectedErr string
	}{
		{
			name:        "valid config with cached false",
			config:      &testValidConfigExampleNoCache,
			expectedErr: "",
		},
		{
			name:        "valid config with cached true and valid cache",
			config:      &testValidConfigExampleCache,
			expectedErr: "",
		},
		{
			name: "invalid config - cached true but no cache",
			config: &ClientConfig{
				GRPC:               testValidConfigExampleNoCache.GRPC,
				Cached:             true,
				ConfigurationCache: nil,
			},
			expectedErr: "CacheConfig is nil",
		},
		{
			name: "invalid config - GRPC validation fails",
			config: &ClientConfig{
				GRPC:               &aggkitgrpc.ClientConfig{},
				Cached:             false,
				ConfigurationCache: nil,
			},
			expectedErr: "gRPC client URL cannot be empty", // This would be the GRPC validation error
		},
		{
			name:        "valid config with rate limits",
			config:      &testValidConfigWithRateLimits,
			expectedErr: "",
		},
		{
			name: "invalid config - unknown method policy",
			config: &ClientConfig{
				GRPC:   testValidConfigExampleNoCache.GRPC,
				Cached: true,
				ConfigurationCache: &CacheConfig{
					TTL:                  types.Duration{Duration: time.Second},
					Capacity:             100,
					GetCertificateHeader: MethodPolicy("bogus"),
				},
			},
			expectedErr: "invalid method policy",
		},
		{
			name: "invalid config - SendCertificate cannot be cached",
			config: &ClientConfig{
				GRPC:   testValidConfigExampleNoCache.GRPC,
				Cached: true,
				ConfigurationCache: &CacheConfig{
					TTL:             types.Duration{Duration: time.Second},
					Capacity:        100,
					SendCertificate: MethodPolicyCached,
				},
			},
			expectedErr: "SendCertificate cannot use",
		},
		{
			name: "valid config - SendCertificate forbidden, unlisted methods default to passthrough",
			config: &ClientConfig{
				GRPC:   testValidConfigExampleNoCache.GRPC,
				Cached: true,
				ConfigurationCache: &CacheConfig{
					TTL:             types.Duration{Duration: time.Second},
					Capacity:        100,
					SendCertificate: MethodPolicyForbidden,
				},
			},
			expectedErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectedErr != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.expectedErr, err)
			} else {
				require.NoError(t, err, "Expected no error for valid config")
			}
		})
	}
}

// TestMethodPolicyEffectiveDefaultsUnsetToPassthrough pins that the zero value MethodPolicy ("")
// is valid and resolves to MethodPolicyPassthrough -- a method simply left out of configuration
func TestMethodPolicyEffectiveDefaultsUnsetToPassthrough(t *testing.T) {
	var unset MethodPolicy
	require.NoError(t, unset.Validate())
	require.Equal(t, MethodPolicyPassthrough, unset.effective())

	require.NoError(t, MethodPolicyCached.Validate())
	require.NoError(t, MethodPolicyPassthrough.Validate())
	require.NoError(t, MethodPolicyForbidden.Validate())
	require.ErrorContains(t, MethodPolicy("bogus").Validate(), "invalid method policy")
}
