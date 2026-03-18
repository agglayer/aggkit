package bridgesync

import (
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name          string
		config        Config
		expectedError string
	}{
		{
			name: "valid config",
			config: Config{
				BlockFinality: aggkittypes.SafeBlock,
			},
			expectedError: "",
		},
		{
			name: "valid config with SyncFromInBridges true",
			config: Config{
				BlockFinality:     aggkittypes.SafeBlock,
				SyncFromInBridges: TrueMode,
			},
			expectedError: "",
		},
		{
			name: "valid config with SyncFromInBridges false",
			config: Config{
				BlockFinality:     aggkittypes.SafeBlock,
				SyncFromInBridges: FalseMode,
			},
			expectedError: "",
		},
		{
			name: "valid config with SyncFromInBridges auto",
			config: Config{
				BlockFinality:     aggkittypes.SafeBlock,
				SyncFromInBridges: AutoMode,
			},
			expectedError: "",
		},
		{
			name: "valid config with empty SyncFromInBridges",
			config: Config{
				BlockFinality:     aggkittypes.SafeBlock,
				SyncFromInBridges: TrueFalseAutoMode{},
			},
			expectedError: "",
		},
		{
			name: "invalid config with invalid BlockFinality",
			config: Config{
				BlockFinality: aggkittypes.BlockNumberFinality{
					Block:  aggkittypes.Latest,
					Offset: 1, // Invalid: LatestBlock cannot have positive offset
				},
			},
			expectedError: "invalid BlockFinality configuration:",
		},
		{
			name: "invalid config with invalid SyncFromInBridges",
			config: Config{
				BlockFinality:     aggkittypes.SafeBlock,
				SyncFromInBridges: TrueFalseAutoMode{Mode: "invalid_value"},
			},
			expectedError: "invalid SyncFromInBridges value:",
		},
		{
			name: "invalid config with numeric SyncFromInBridges",
			config: Config{
				BlockFinality:     aggkittypes.SafeBlock,
				SyncFromInBridges: TrueFalseAutoMode{Mode: "123"},
			},
			expectedError: "invalid SyncFromInBridges value:",
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
