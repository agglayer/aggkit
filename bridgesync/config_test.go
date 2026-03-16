package bridgesync

import (
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func TestSyncFromInBridgesMode_UnmarshalText(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expected      TrueFalseAutoMode
		expectedError string
	}{
		{
			name:          "true lowercase",
			input:         "true",
			expected:      TrueMode,
			expectedError: "",
		},
		{
			name:          "true uppercase",
			input:         "TRUE",
			expected:      TrueMode,
			expectedError: "",
		},
		{
			name:          "true mixed case",
			input:         "TrUe",
			expected:      TrueMode,
			expectedError: "",
		},
		{
			name:          "true with whitespace",
			input:         "  true  ",
			expected:      TrueMode,
			expectedError: "",
		},
		{
			name:          "false lowercase",
			input:         "false",
			expected:      FalseMode,
			expectedError: "",
		},
		{
			name:          "false uppercase",
			input:         "FALSE",
			expected:      FalseMode,
			expectedError: "",
		},
		{
			name:          "false mixed case",
			input:         "FaLsE",
			expected:      FalseMode,
			expectedError: "",
		},
		{
			name:          "false with whitespace",
			input:         "  false  ",
			expected:      FalseMode,
			expectedError: "",
		},
		{
			name:          "auto lowercase",
			input:         "auto",
			expected:      AutoValue,
			expectedError: "",
		},
		{
			name:          "auto uppercase",
			input:         "AUTO",
			expected:      AutoValue,
			expectedError: "",
		},
		{
			name:          "auto mixed case",
			input:         "AuTo",
			expected:      AutoValue,
			expectedError: "",
		},
		{
			name:          "auto with whitespace",
			input:         "  auto  ",
			expected:      AutoValue,
			expectedError: "",
		},
		{
			name:          "invalid value",
			input:         "invalid",
			expected:      "",
			expectedError: "invalid SyncFromInBridgesMode: invalid (valid values: true, false, auto)",
		},
		{
			name:          "empty string",
			input:         "",
			expected:      "",
			expectedError: "invalid SyncFromInBridgesMode:  (valid values: true, false, auto)",
		},
		{
			name:          "numeric value",
			input:         "1",
			expected:      "",
			expectedError: "invalid SyncFromInBridgesMode: 1 (valid values: true, false, auto)",
		},
		{
			name:          "yes value",
			input:         "yes",
			expected:      "",
			expectedError: "invalid SyncFromInBridgesMode: yes (valid values: true, false, auto)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mode TrueFalseAutoMode
			err := mode.UnmarshalText([]byte(tt.input))

			if tt.expectedError == "" {
				require.NoError(t, err)
				require.Equal(t, tt.expected, mode)
			} else {
				require.Error(t, err)
				require.Equal(t, tt.expectedError, err.Error())
			}
		})
	}
}

func TestSyncFromInBridgesMode_String(t *testing.T) {
	tests := []struct {
		name     string
		mode     TrueFalseAutoMode
		expected string
	}{
		{
			name:     "true mode",
			mode:     TrueMode,
			expected: "true",
		},
		{
			name:     "false mode",
			mode:     FalseMode,
			expected: "false",
		},
		{
			name:     "auto mode",
			mode:     AutoValue,
			expected: "auto",
		},
		{
			name:     "empty mode",
			mode:     TrueFalseAutoMode(""),
			expected: "",
		},
		{
			name:     "invalid mode",
			mode:     TrueFalseAutoMode("invalid"),
			expected: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.mode.String()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestSyncFromInBridgesMode_Resolve(t *testing.T) {
	tests := []struct {
		name               string
		mode               TrueFalseAutoMode
		hasBridgeComponent bool
		expected           bool
	}{
		{
			name:               "true mode with bridge component",
			mode:               TrueMode,
			hasBridgeComponent: true,
			expected:           true,
		},
		{
			name:               "true mode without bridge component",
			mode:               TrueMode,
			hasBridgeComponent: false,
			expected:           true,
		},
		{
			name:               "false mode with bridge component",
			mode:               FalseMode,
			hasBridgeComponent: true,
			expected:           false,
		},
		{
			name:               "false mode without bridge component",
			mode:               FalseMode,
			hasBridgeComponent: false,
			expected:           false,
		},
		{
			name:               "auto mode with bridge component",
			mode:               AutoValue,
			hasBridgeComponent: true,
			expected:           true,
		},
		{
			name:               "auto mode without bridge component",
			mode:               AutoValue,
			hasBridgeComponent: false,
			expected:           false,
		},
		{
			name:               "invalid mode with bridge component",
			mode:               TrueFalseAutoMode("invalid"),
			hasBridgeComponent: true,
			expected:           false,
		},
		{
			name:               "invalid mode without bridge component",
			mode:               TrueFalseAutoMode("invalid"),
			hasBridgeComponent: false,
			expected:           false,
		},
		{
			name:               "empty mode with bridge component",
			mode:               TrueFalseAutoMode(""),
			hasBridgeComponent: true,
			expected:           false,
		},
		{
			name:               "empty mode without bridge component",
			mode:               TrueFalseAutoMode(""),
			hasBridgeComponent: false,
			expected:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.mode.Resolve(tt.hasBridgeComponent)
			require.Equal(t, tt.expected, result)
		})
	}
}

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
				SyncFromInBridges: AutoValue,
			},
			expectedError: "",
		},
		{
			name: "valid config with empty SyncFromInBridges",
			config: Config{
				BlockFinality:     aggkittypes.SafeBlock,
				SyncFromInBridges: "",
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
				SyncFromInBridges: "invalid_value",
			},
			expectedError: "invalid SyncFromInBridges value:",
		},
		{
			name: "invalid config with numeric SyncFromInBridges",
			config: Config{
				BlockFinality:     aggkittypes.SafeBlock,
				SyncFromInBridges: "123",
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
