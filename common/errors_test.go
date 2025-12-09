package common

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseMaxRangeFromError(t *testing.T) {
	tests := []struct {
		name        string
		errorMsg    string
		expected    uint64
		shouldError bool
	}{
		{
			name:        "standard error format",
			errorMsg:    "block range too large, max range: 1000",
			expected:    1000,
			shouldError: false,
		},
		{
			name:        "error with extra spaces",
			errorMsg:    "block range too large, max range:   5000",
			expected:    5000,
			shouldError: false,
		},
		{
			name:        "error with no spaces",
			errorMsg:    "block range too large, max range:10000",
			expected:    10000,
			shouldError: false,
		},
		{
			name:        "error with large number",
			errorMsg:    "block range too large, max range: 999999",
			expected:    999999,
			shouldError: false,
		},
		{
			name:        "error with different text",
			errorMsg:    "some other error happened",
			expected:    0,
			shouldError: true,
		},
		{
			name:        "error with non-numeric range",
			errorMsg:    "block range too large, max range: abc",
			expected:    0,
			shouldError: true,
		},
		{
			name:        "empty error message",
			errorMsg:    "",
			expected:    0,
			shouldError: true,
		},
		{
			name:        "error with negative number (should fail)",
			errorMsg:    "block range too large, max range: -100",
			expected:    0,
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseMaxRangeFromError(tt.errorMsg)
			if tt.shouldError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestParseMaxRangeFromError_WrappedErrors(t *testing.T) {
	t.Run("wrapped error with fmt.Errorf", func(t *testing.T) {
		baseErr := errors.New("block range too large, max range: 2000")
		wrappedErr := fmt.Errorf("RPC error: %w", baseErr)

		result, err := ParseMaxRangeFromError(wrappedErr.Error())
		require.NoError(t, err)
		require.Equal(t, uint64(2000), result)
	})

	t.Run("multiple levels of wrapping", func(t *testing.T) {
		baseErr := errors.New("block range too large, max range: 3000")
		wrappedErr1 := fmt.Errorf("contract call failed: %w", baseErr)
		wrappedErr2 := fmt.Errorf("query failed: %w", wrappedErr1)

		result, err := ParseMaxRangeFromError(wrappedErr2.Error())
		require.NoError(t, err)
		require.Equal(t, uint64(3000), result)
	})

	t.Run("error with prefix", func(t *testing.T) {
		errMsg := "execution reverted: block range too large, max range: 1500"

		result, err := ParseMaxRangeFromError(errMsg)
		require.NoError(t, err)
		require.Equal(t, uint64(1500), result)
	})

	t.Run("error with suffix", func(t *testing.T) {
		errMsg := "block range too large, max range: 2500, please reduce range"

		result, err := ParseMaxRangeFromError(errMsg)
		require.NoError(t, err)
		require.Equal(t, uint64(2500), result)
	})
}
