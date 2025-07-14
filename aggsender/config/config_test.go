package config

import (
	"testing"
	"time"

	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/grpc"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		config      Config
		expectedErr string
	}{
		{
			name: "RequireValidatorCall ",
		},
		{
			name: "RequireValidatorCall is true with ValidatorClient URL set",
			config: Config{
				RequireValidatorCall: true,
				ValidatorClient: &grpc.ClientConfig{
					URL: "http://localhost:8080",
				},
				AgglayerClient: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				},
			},
		},
		{
			name: "RequireValidatorCall is true with ValidatorClient URL not set",
			config: Config{
				RequireValidatorCall: true,
				ValidatorClient: &grpc.ClientConfig{
					URL: "",
				},
				AgglayerClient: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				},
			},
			expectedErr: "ValidatorClient URL must be set when RequireValidatorCall is true",
		},
		{
			name: "Invalid AgglayerClient configuration",
			config: Config{
				AgglayerClient: &grpc.ClientConfig{
					URL: "",
				},
			},
			expectedErr: "invalid agglayer client config",
		},
		{
			name: "AggchainProof mode with AggkitProverClient not set",
			config: Config{
				Mode: aggsendertypes.AggchainProofMode.String(),
				AgglayerClient: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				},
				AggkitProverClient: &grpc.ClientConfig{
					URL: "",
				},
			},
			expectedErr: "invalid aggkit prover client config",
		},
		{
			name: "PessimisticProof mode with AggkitProverClient not set",
			config: Config{
				Mode: aggsendertypes.PessimisticProofMode.String(),
				AgglayerClient: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				},
				AggkitProverClient: &grpc.ClientConfig{
					URL: "",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.config.Validate()
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
