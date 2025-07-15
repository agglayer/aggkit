package agglayer

import (
	"testing"

	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/stretchr/testify/require"
)

func TestClientConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		config      *ClientConfig
		expectedErr error
	}{
		{
			name: "valid config with cached false",
			config: &ClientConfig{
				GRPC: &aggkitgrpc.ClientConfig{
					URL: "localhost:8080",
				},
				Cached:             false,
				ConfigurationCache: nil,
			},
			expectedErr: nil,
		},
		{
			name: "valid config with cached true and valid cache",
			config: &ClientConfig{
				GRPC: &aggkitgrpc.ClientConfig{
					URL: "localhost:8080",
				},
				Cached: true,
				ConfigurationCache: &ConfigurationCache{
					Capacity: 100,
				},
			},
			expectedErr: nil,
		},
		{
			name: "invalid config - cached true but no cache",
			config: &ClientConfig{
				GRPC: &aggkitgrpc.ClientConfig{
					URL: "localhost:8080",
				},
				Cached:             true,
				ConfigurationCache: nil,
			},
			expectedErr: ErrConfigurationCacheRequired,
		},
		{
			name: "invalid config - GRPC validation fails",
			config: &ClientConfig{
				GRPC:               &aggkitgrpc.ClientConfig{},
				Cached:             false,
				ConfigurationCache: nil,
			},
			expectedErr: nil, // This would be the GRPC validation error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectedErr != nil {
				require.Error(t, err)
				if tt.expectedErr == ErrConfigurationCacheRequired {
					require.Equal(t, tt.expectedErr, err)
				}
			} else if tt.name == "invalid config - GRPC validation fails" {
				require.Error(t, err) // GRPC validation should fail
			} else {
				require.NoError(t, err)
			}
		})
	}
}
