package validator

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/grpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestValidatorConfigValidate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		config      Config
		rollupAddr  common.Address
		expectedErr string
	}{
		{
			name: "Valid PessimisticProof mode",
			config: Config{
				Mode: aggsendertypes.PessimisticProofMode.String(),
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
			},
		},
		{
			name:       "Valid AggchainProof mode",
			rollupAddr: common.HexToAddress("0x1"),
			config: Config{
				Mode:      aggsendertypes.AggchainProofMode.String(),
				FEPConfig: FEPConfig{},
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
			},
		},
		{
			name:       "Invalid rollup address",
			rollupAddr: common.HexToAddress("0x0"),
			config: Config{
				Mode:      aggsendertypes.AggchainProofMode.String(),
				FEPConfig: FEPConfig{},
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
			},
			expectedErr: errInvalidSovereignRollupAddr.Error(),
		},
		{
			name: "Invalid mode",
			config: Config{
				Mode: "invalid-mode",
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
			},
			expectedErr: "invalid mode invalid-mode, must be one of",
		},
		{
			name: "Empty mode",
			config: Config{
				Mode: "",
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
			},
			expectedErr: "invalid mode , must be one of",
		},
		{
			name: "Invalid AgglayerClient configuration",
			config: Config{
				Mode: aggsendertypes.PessimisticProofMode.String(),
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL: "",
				}},
			},
			expectedErr: "invalid agglayer client config",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.config.Validate(tc.rollupAddr)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
