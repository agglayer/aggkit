package validator

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/grpc"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestValidatorConfigValidate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		config      Config
		expectedErr string
	}{
		{
			name: "Valid PessimisticProof mode",
			config: Config{
				Mode: aggsendertypes.PessimisticProofMode,
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
		},
		{
			name: "Valid AggchainProof mode",
			config: Config{
				Mode: aggsendertypes.AggchainProofMode,
				FEPConfig: FEPConfig{
					SovereignRollupAddr: common.HexToAddress("0x1"),
				},
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
		},
		{
			name: "Invalid AggchainProof mode",
			config: Config{
				Mode: aggsendertypes.AggchainProofMode,
				FEPConfig: FEPConfig{
					SovereignRollupAddr: common.HexToAddress("0x0"), // Zero address
				},
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
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
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
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
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			expectedErr: "invalid mode , must be one of",
		},
		{
			name: "Invalid AgglayerClient configuration",
			config: Config{
				Mode: aggsendertypes.PessimisticProofMode,
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL: "",
				}},
				BlockFinalityForL1InfoTree: aggkittypes.FinalizedBlock,
			},
			expectedErr: "invalid agglayer client config",
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
