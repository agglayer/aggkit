package claimsync

import (
	"testing"

	configtypes "github.com/agglayer/aggkit/config/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func TestConfigEmbedded_Validate(t *testing.T) {
	c := ConfigEmbedded{}
	require.NoError(t, c.Validate())
}

func TestConfigStandalone_Validate(t *testing.T) {
	tests := []struct {
		name          string
		config        ConfigStandalone
		expectedError string
	}{
		{
			name: "valid config",
			config: ConfigStandalone{
				ConfigEmbedded: ConfigEmbedded{},
				BlockFinality:  aggkittypes.SafeBlock,
			},
			expectedError: "",
		},
		{
			name: "valid config with AutoStart true",
			config: ConfigStandalone{
				BlockFinality: aggkittypes.SafeBlock,
				AutoStart:     configtypes.TrueMode,
			},
			expectedError: "",
		},
		{
			name: "valid config with AutoStart false",
			config: ConfigStandalone{
				BlockFinality: aggkittypes.SafeBlock,
				AutoStart:     configtypes.FalseMode,
			},
			expectedError: "",
		},
		{
			name: "valid config with AutoStart auto",
			config: ConfigStandalone{
				BlockFinality: aggkittypes.SafeBlock,
				AutoStart:     configtypes.AutoMode,
			},
			expectedError: "",
		},
		{
			name: "valid config with empty AutoStart",
			config: ConfigStandalone{
				BlockFinality: aggkittypes.SafeBlock,
				AutoStart:     configtypes.TrueFalseAutoMode{},
			},
			expectedError: "",
		},
		{
			name: "invalid BlockFinality",
			config: ConfigStandalone{
				BlockFinality: aggkittypes.BlockNumberFinality{
					Block:  aggkittypes.Latest,
					Offset: 1, // Invalid: LatestBlock cannot have positive offset
				},
			},
			expectedError: "invalid BlockFinality configuration:",
		},
		{
			name: "invalid AutoStart value",
			config: ConfigStandalone{
				BlockFinality: aggkittypes.SafeBlock,
				AutoStart:     configtypes.TrueFalseAutoMode{Mode: "invalid_value"},
			},
			expectedError: "invalid AutoStart configuration:",
		},
		{
			name: "invalid AutoStart numeric value",
			config: ConfigStandalone{
				BlockFinality: aggkittypes.SafeBlock,
				AutoStart:     configtypes.TrueFalseAutoMode{Mode: "123"},
			},
			expectedError: "invalid AutoStart configuration:",
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
