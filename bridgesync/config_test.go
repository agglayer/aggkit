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
		expected      SyncFromInBridgesMode
		expectedError string
	}{
		{
			name:          "true lowercase",
			input:         "true",
			expected:      SyncFromInBridgesTrue,
			expectedError: "",
		},
		{
			name:          "true uppercase",
			input:         "TRUE",
			expected:      SyncFromInBridgesTrue,
			expectedError: "",
		},
		{
			name:          "true mixed case",
			input:         "TrUe",
			expected:      SyncFromInBridgesTrue,
			expectedError: "",
		},
		{
			name:          "true with whitespace",
			input:         "  true  ",
			expected:      SyncFromInBridgesTrue,
			expectedError: "",
		},
		{
			name:          "false lowercase",
			input:         "false",
			expected:      SyncFromInBridgesFalse,
			expectedError: "",
		},
		{
			name:          "false uppercase",
			input:         "FALSE",
			expected:      SyncFromInBridgesFalse,
			expectedError: "",
		},
		{
			name:          "false mixed case",
			input:         "FaLsE",
			expected:      SyncFromInBridgesFalse,
			expectedError: "",
		},
		{
			name:          "false with whitespace",
			input:         "  false  ",
			expected:      SyncFromInBridgesFalse,
			expectedError: "",
		},
		{
			name:          "auto lowercase",
			input:         "auto",
			expected:      SyncFromInBridgesAuto,
			expectedError: "",
		},
		{
			name:          "auto uppercase",
			input:         "AUTO",
			expected:      SyncFromInBridgesAuto,
			expectedError: "",
		},
		{
			name:          "auto mixed case",
			input:         "AuTo",
			expected:      SyncFromInBridgesAuto,
			expectedError: "",
		},
		{
			name:          "auto with whitespace",
			input:         "  auto  ",
			expected:      SyncFromInBridgesAuto,
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
			var mode SyncFromInBridgesMode
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
		mode     SyncFromInBridgesMode
		expected string
	}{
		{
			name:     "true mode",
			mode:     SyncFromInBridgesTrue,
			expected: "true",
		},
		{
			name:     "false mode",
			mode:     SyncFromInBridgesFalse,
			expected: "false",
		},
		{
			name:     "auto mode",
			mode:     SyncFromInBridgesAuto,
			expected: "auto",
		},
		{
			name:     "empty mode",
			mode:     SyncFromInBridgesMode(""),
			expected: "",
		},
		{
			name:     "invalid mode",
			mode:     SyncFromInBridgesMode("invalid"),
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
		mode               SyncFromInBridgesMode
		hasBridgeComponent bool
		expected           bool
	}{
		{
			name:               "true mode with bridge component",
			mode:               SyncFromInBridgesTrue,
			hasBridgeComponent: true,
			expected:           true,
		},
		{
			name:               "true mode without bridge component",
			mode:               SyncFromInBridgesTrue,
			hasBridgeComponent: false,
			expected:           true,
		},
		{
			name:               "false mode with bridge component",
			mode:               SyncFromInBridgesFalse,
			hasBridgeComponent: true,
			expected:           false,
		},
		{
			name:               "false mode without bridge component",
			mode:               SyncFromInBridgesFalse,
			hasBridgeComponent: false,
			expected:           false,
		},
		{
			name:               "auto mode with bridge component",
			mode:               SyncFromInBridgesAuto,
			hasBridgeComponent: true,
			expected:           true,
		},
		{
			name:               "auto mode without bridge component",
			mode:               SyncFromInBridgesAuto,
			hasBridgeComponent: false,
			expected:           false,
		},
		{
			name:               "invalid mode with bridge component",
			mode:               SyncFromInBridgesMode("invalid"),
			hasBridgeComponent: true,
			expected:           false,
		},
		{
			name:               "invalid mode without bridge component",
			mode:               SyncFromInBridgesMode("invalid"),
			hasBridgeComponent: false,
			expected:           false,
		},
		{
			name:               "empty mode with bridge component",
			mode:               SyncFromInBridgesMode(""),
			hasBridgeComponent: true,
			expected:           false,
		},
		{
			name:               "empty mode without bridge component",
			mode:               SyncFromInBridgesMode(""),
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
				SyncFromInBridges: SyncFromInBridgesTrue,
			},
			expectedError: "",
		},
		{
			name: "valid config with SyncFromInBridges false",
			config: Config{
				BlockFinality:     aggkittypes.SafeBlock,
				SyncFromInBridges: SyncFromInBridgesFalse,
			},
			expectedError: "",
		},
		{
			name: "valid config with SyncFromInBridges auto",
			config: Config{
				BlockFinality:     aggkittypes.SafeBlock,
				SyncFromInBridges: SyncFromInBridgesAuto,
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
