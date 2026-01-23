package config

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/grpc"
	aggkittypes "github.com/agglayer/aggkit/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	ethCommon "github.com/ethereum/go-ethereum/common"
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
			name: "Invalid AgglayerClient configuration",
			config: Config{
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL: "",
				},
				},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
				TriggerCertMode:            aggsendertypes.AutoTriggerMode,
			},
			expectedErr: "invalid agglayer client config",
		},
		{
			name: "AggchainProof mode with AggkitProverClient not set",
			config: Config{
				Mode: aggsendertypes.AggchainProofMode,
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				},
				},
				AggkitProverClient: &grpc.ClientConfig{
					URL: "",
				},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
				TriggerCertMode:            aggsendertypes.AutoTriggerMode,
			},
			expectedErr: "invalid aggkit prover client config",
		},
		{
			name: "PessimisticProof mode with AggkitProverClient not set",
			config: Config{
				Mode: aggsendertypes.PessimisticProofMode,
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				},
				},
				AggkitProverClient: &grpc.ClientConfig{
					URL: "",
				},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
				TriggerCertMode:            aggsendertypes.AutoTriggerMode,
			},
		},
		{
			name: "BlockFinalityForL1InfoTree not set",
			config: Config{
				Mode: aggsendertypes.PessimisticProofMode,
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				},
				},
				AggkitProverClient: &grpc.ClientConfig{
					URL: "",
				},
				TriggerCertMode: aggsendertypes.AutoTriggerMode,
			},
			expectedErr: "BlockFinalityForL1InfoTree",
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

func TestConfigString(t *testing.T) {
	t.Parallel()

	config := Config{
		StoragePath:     "/path/to/storage.sqlite",
		CertificatesDir: "/path/to/certificates/",
		TriggerEpochBased: TriggerEpochBasedConfig{
			EpochNotificationPercentage: 75,
		},
		DryRun:                         true,
		EnableRPC:                      false,
		Mode:                           aggsendertypes.PessimisticProofMode,
		RetryCertAfterInError:          true,
		RequireNoFEPBlockGap:           false,
		CheckStatusCertificateInterval: types.NewDuration(5 * time.Minute),
		AgglayerClient: agglayer.ClientConfig{
			GRPC: &grpc.ClientConfig{
				URL:               "http://agglayer.example.com",
				MinConnectTimeout: types.NewDuration(10 * time.Second),
				RequestTimeout:    types.NewDuration(30 * time.Second),
				UseTLS:            false,
			},
			APIRateLimits: []agglayer.APIRateLimitConfig{
				{
					MethodName: "SubmitCertificate",
					RateLimit: common.RateLimitConfig{
						NumRequests: 10,
						Interval:    types.NewDuration(1 * time.Hour),
					},
				},
			},
		},
		AggsenderPrivateKey: signertypes.SignerConfig{
			Method: signertypes.MethodLocal,
		},
		AggkitProverClient: &grpc.ClientConfig{
			URL:               "http://prover.example.com",
			MinConnectTimeout: types.NewDuration(5 * time.Second),
		},
		SovereignRollupAddr: ethCommon.HexToAddress("0x1234567890123456789012345678901234567890"),
		RetriesToBuildAndSendCertificate: common.RetryPolicyGenericConfig{
			Mode:       "delays",
			MaxRetries: 3,
			Delays:     []types.Duration{types.NewDuration(1 * time.Second), types.NewDuration(2 * time.Second)},
		},
	}

	result := config.String()

	// Verify that all fields are included in the string representation
	require.Contains(t, result, "StoragePath: /path/to/storage.sqlite")
	require.Contains(t, result, "CertificatesDir: /path/to/certificates/")
	require.Contains(t, result, "AgglayerClient: GRPC: GRPC Client Config: URL=http://agglayer.example.com")
	require.Contains(t, result, "AggsenderPrivateKey: local")
	require.Contains(t, result, "EpochNotificationPercentage: 75")
	require.Contains(t, result, "DryRun: true")
	require.Contains(t, result, "EnableRPC: false")
	require.Contains(t, result, "AggkitProverClient: GRPC Client Config: URL=http://prover.example.com")
	require.Contains(t, result, "Mode: PessimisticProof")
	require.Contains(t, result, "CheckStatusCertificateInterval: 5m0s")
	require.Contains(t, result, "RetryCertAfterInError: true")
	require.Contains(t, result, "APIRateLimits: [APIRateLimitConfig{Method: SubmitCertificate, RateLimit: RateLimitConfig{NumRequests: 10, Period: 1h0m0s}}]")
	require.Contains(t, result, "SovereignRollupAddr: 0x1234567890123456789012345678901234567890")
	require.Contains(t, result, "RequireNoFEPBlockGap: false")
	require.Contains(t, result, "RetriesToBuildAndSendCertificate: RetryPolicyConfig{Mode: delays")
}
