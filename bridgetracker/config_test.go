package bridgetracker

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
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
				L1GlobalExitRootAddress: common.HexToAddress("0x1f7ad7caA53e35b4f0D138dC5CBF91aC108a2674"),
			},
			expectedError: "",
		},
		{
			name:          "zero L1GlobalExitRootAddress",
			config:        Config{},
			expectedError: "[Tracker].L1GlobalExitRootAddress",
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
