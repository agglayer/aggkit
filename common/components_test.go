package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateComponents(t *testing.T) {
	tests := []struct {
		name        string
		components  []string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid single component",
			components:  []string{AGGORACLE},
			expectError: false,
		},
		{
			name:        "valid autoclaim component",
			components:  []string{AUTOCLAIM},
			expectError: false,
		},
		{
			name:        "valid multiple components",
			components:  []string{AGGORACLE, BRIDGE, AGGSENDER, AUTOCLAIM},
			expectError: false,
		},
		{
			name:        "empty list",
			components:  []string{},
			expectError: false,
		},
		{
			name:        "invalid component",
			components:  []string{"invalid_component"},
			expectError: true,
			errorMsg:    "unknown component: invalid_component",
		},
		{
			name:        "mixed valid and invalid",
			components:  []string{AGGORACLE, "invalid_component"},
			expectError: true,
			errorMsg:    "unknown component: invalid_component",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateComponents(tt.components)

			if tt.expectError {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
