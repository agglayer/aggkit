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
				GERValidateConfig: GERValidateConfig{
					GlobalExitRootL1Addr: common.HexToAddress("0x2"),
					BlockFinality:        aggkittypes.FinalizedBlock,
				},
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
			},
		},
		{
			name: "Valid AggchainProof mode",
			config: Config{
				Mode: aggsendertypes.AggchainProofMode,
				FEPConfig: FEPConfig{
					SovereignRollupAddr: common.HexToAddress("0x1"),
				},
				GERValidateConfig: GERValidateConfig{
					GlobalExitRootL1Addr: common.HexToAddress("0x2"),
					BlockFinality:        aggkittypes.FinalizedBlock,
				},
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
			},
		},
		{
			name: "Invalid GERValidateConfig - zero address",
			config: Config{
				Mode: aggsendertypes.PessimisticProofMode,
				GERValidateConfig: GERValidateConfig{
					GlobalExitRootL1Addr: common.HexToAddress("0x0"), // Zero address
					BlockFinality:        aggkittypes.FinalizedBlock,
				},
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
			},
			expectedErr: "GlobalExitRootL1Addr must be set",
		},
		{
			name: "Invalid GERValidateConfig - block finality non valid",
			config: Config{
				Mode: aggsendertypes.PessimisticProofMode,
				GERValidateConfig: GERValidateConfig{
					GlobalExitRootL1Addr: common.HexToAddress("0x2"),
					BlockFinality: aggkittypes.BlockNumberFinality{
						Block:  aggkittypes.Finalized,
						Offset: aggkittypes.MaxPositiveOffsetFinalized + 1, // Invalid offset
					},
				},
				AgglayerClient: agglayer.ClientConfig{GRPC: &grpc.ClientConfig{
					URL:               "http://localhost:9090",
					MinConnectTimeout: types.NewDuration(5 * time.Second),
				}},
			},
			expectedErr: "invalid BlockFinality configuration",
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
				Mode: aggsendertypes.PessimisticProofMode,
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

			err := tc.config.Validate()
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
