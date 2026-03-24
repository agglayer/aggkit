package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrueFalseAutoMode_UnmarshalText(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expected      TrueFalseAutoMode
		expectedError string
	}{
		{
			name:     "true lowercase",
			input:    "true",
			expected: TrueFalseAutoMode{Mode: "true"},
		},
		{
			name:     "true uppercase",
			input:    "TRUE",
			expected: TrueFalseAutoMode{Mode: "true"},
		},
		{
			name:     "true mixed case",
			input:    "TrUe",
			expected: TrueFalseAutoMode{Mode: "true"},
		},
		{
			name:     "true with whitespace",
			input:    "  true  ",
			expected: TrueFalseAutoMode{Mode: "true"},
		},
		{
			name:     "false lowercase",
			input:    "false",
			expected: TrueFalseAutoMode{Mode: "false"},
		},
		{
			name:     "false uppercase",
			input:    "FALSE",
			expected: TrueFalseAutoMode{Mode: "false"},
		},
		{
			name:     "false mixed case",
			input:    "FaLsE",
			expected: TrueFalseAutoMode{Mode: "false"},
		},
		{
			name:     "false with whitespace",
			input:    "  false  ",
			expected: TrueFalseAutoMode{Mode: "false"},
		},
		{
			name:     "auto lowercase",
			input:    "auto",
			expected: TrueFalseAutoMode{Mode: "auto"},
		},
		{
			name:     "auto uppercase",
			input:    "AUTO",
			expected: TrueFalseAutoMode{Mode: "auto"},
		},
		{
			name:     "auto mixed case",
			input:    "AuTo",
			expected: TrueFalseAutoMode{Mode: "auto"},
		},
		{
			name:     "auto with whitespace",
			input:    "  auto  ",
			expected: TrueFalseAutoMode{Mode: "auto"},
		},
		{
			name:          "invalid value",
			input:         "invalid",
			expectedError: "invalid TrueFalseAutoMode: invalid (valid values: true, false, auto)",
		},
		{
			name:          "empty string",
			input:         "",
			expectedError: "invalid TrueFalseAutoMode:  (valid values: true, false, auto)",
		},
		{
			name:          "numeric value",
			input:         "1",
			expectedError: "invalid TrueFalseAutoMode: 1 (valid values: true, false, auto)",
		},
		{
			name:          "yes value",
			input:         "yes",
			expectedError: "invalid TrueFalseAutoMode: yes (valid values: true, false, auto)",
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

func TestTrueFalseAutoMode_String(t *testing.T) {
	tests := []struct {
		name     string
		mode     TrueFalseAutoMode
		expected string
	}{
		{name: "true mode", mode: TrueMode, expected: "true"},
		{name: "false mode", mode: FalseMode, expected: "false"},
		{name: "auto mode", mode: AutoMode, expected: "auto"},
		{name: "empty mode", mode: TrueFalseAutoMode{}, expected: ""},
		{name: "invalid mode", mode: TrueFalseAutoMode{Mode: "invalid"}, expected: "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.mode.String())
		})
	}
}

func TestTrueFalseAutoMode_Resolve(t *testing.T) {
	tests := []struct {
		name               string
		mode               TrueFalseAutoMode
		hasBridgeComponent bool
		expected           bool
	}{
		{name: "true mode with bridge component", mode: TrueMode, hasBridgeComponent: true, expected: true},
		{name: "true mode without bridge component", mode: TrueMode, hasBridgeComponent: false, expected: true},
		{name: "false mode with bridge component", mode: FalseMode, hasBridgeComponent: true, expected: false},
		{name: "false mode without bridge component", mode: FalseMode, hasBridgeComponent: false, expected: false},
		{name: "auto mode with bridge component", mode: AutoMode, hasBridgeComponent: true, expected: true},
		{name: "auto mode without bridge component", mode: AutoMode, hasBridgeComponent: false, expected: false},
		{name: "invalid mode", mode: TrueFalseAutoMode{Mode: "invalid"}, hasBridgeComponent: true, expected: false},
		{name: "empty mode", mode: TrueFalseAutoMode{}, hasBridgeComponent: true, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := tt.mode
			result := mode.Resolve(tt.hasBridgeComponent)
			require.Equal(t, tt.expected, result)
			require.NotNil(t, mode.Resolved)
			require.Equal(t, tt.expected, *mode.Resolved)
		})
	}
}

func TestTrueFalseAutoMode_Validate(t *testing.T) {
	tests := []struct {
		name          string
		mode          TrueFalseAutoMode
		fieldName     string
		expectedError string
	}{
		{name: "true mode", mode: TrueMode, fieldName: "TestField"},
		{name: "false mode", mode: FalseMode, fieldName: "TestField"},
		{name: "auto mode", mode: AutoMode, fieldName: "TestField"},
		{name: "empty mode is allowed", mode: TrueFalseAutoMode{}, fieldName: "TestField"},
		{
			name:          "invalid mode",
			mode:          TrueFalseAutoMode{Mode: "invalid_value"},
			fieldName:     "TestField",
			expectedError: "invalid TestField configuration:",
		},
		{
			name:          "numeric mode",
			mode:          TrueFalseAutoMode{Mode: "123"},
			fieldName:     "MyField",
			expectedError: "invalid MyField configuration:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mode.Validate(tt.fieldName)
			if tt.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}
