package bridgetracker

import (
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestConfig_Validate(t *testing.T) {
	validL1GER := common.HexToAddress("0x1f7ad7caA53e35b4f0D138dC5CBF91aC108a2674")

	tests := []struct {
		name          string
		config        Config
		expectedError string
	}{
		{
			name: "valid config",
			config: Config{
				L1GlobalExitRootAddress: validL1GER,
			},
			expectedError: "",
		},
		{
			name:          "zero L1GlobalExitRootAddress",
			config:        Config{},
			expectedError: "[Tracker].L1GlobalExitRootAddress",
		},
		{
			name: "ActivitySourceRPC disabled: an otherwise-invalid empty range is ignored",
			config: Config{
				L1GlobalExitRootAddress: validL1GER,
				ActivitySourceRPC:       ActivitySourceRPCConfig{Enabled: false},
			},
			expectedError: "",
		},
		{
			name: "ActivitySourceRPC enabled with a valid range",
			config: Config{
				L1GlobalExitRootAddress: validL1GER,
				ActivitySourceRPC: ActivitySourceRPCConfig{
					Enabled:        true,
					RangeFromBlock: aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest, Offset: -90},
					RangeToBlock:   aggkittypes.LatestBlock,
				},
			},
			expectedError: "",
		},
		{
			name: "ActivitySourceRPC enabled with an empty RangeFromBlock",
			config: Config{
				L1GlobalExitRootAddress: validL1GER,
				ActivitySourceRPC: ActivitySourceRPCConfig{
					Enabled:      true,
					RangeToBlock: aggkittypes.LatestBlock,
				},
			},
			expectedError: "[Tracker.ActivitySourceRPC].RangeFromBlock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}
