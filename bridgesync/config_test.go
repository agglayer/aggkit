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
			name: "invalid config with invalid BlockFinality",
			config: Config{
				BlockFinality: aggkittypes.BlockNumberFinality{
					Block:  aggkittypes.Latest,
					Offset: 1, // Invalid: LatestBlock cannot have positive offset
				},
			},
			expectedError: "invalid BlockFinality configuration:",
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
