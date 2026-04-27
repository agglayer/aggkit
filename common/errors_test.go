package common

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseMaxRangeFromError(t *testing.T) {
	tests := []struct {
		name               string
		errorMsg           string
		expectedMaxBlock   uint64
		expectedIsMaxRange bool
	}{
		{
			name:               "standard error format",
			errorMsg:           "block range too large, max range: 1000",
			expectedMaxBlock:   1000,
			expectedIsMaxRange: true,
		},
		{
			name:               "error with extra spaces",
			errorMsg:           "block range too large, max range:   5000",
			expectedMaxBlock:   5000,
			expectedIsMaxRange: true,
		},
		{
			name:               "error with no spaces",
			errorMsg:           "block range too large, max range:10000",
			expectedMaxBlock:   10000,
			expectedIsMaxRange: true,
		},
		{
			name:               "error with large number",
			errorMsg:           "block range too large, max range: 999999",
			expectedMaxBlock:   999999,
			expectedIsMaxRange: true,
		},
		{
			name:               "error with different text",
			errorMsg:           "some other error happened",
			expectedMaxBlock:   0,
			expectedIsMaxRange: false,
		},
		{
			name:               "error with non-numeric range",
			errorMsg:           "block range too large, max range: abc",
			expectedMaxBlock:   0,
			expectedIsMaxRange: false,
		},
		{
			name:               "empty error message",
			errorMsg:           "",
			expectedMaxBlock:   0,
			expectedIsMaxRange: false,
		},
		{
			name:               "error with negative number (should fail)",
			errorMsg:           "block range too large, max range: -100",
			expectedMaxBlock:   0,
			expectedIsMaxRange: false,
		},
		{
			name:               "exceeded maximum block range format",
			errorMsg:           "exceeded maximum block range: 5000",
			expectedMaxBlock:   5000,
			expectedIsMaxRange: true,
		},
		{
			name:               "exceeded maximum block range with no spaces",
			errorMsg:           "exceeded maximum block range:1000",
			expectedMaxBlock:   1000,
			expectedIsMaxRange: true,
		},
		{
			name:               "exceeded maximum block range with extra spaces",
			errorMsg:           "exceeded maximum block range:   2500",
			expectedMaxBlock:   2500,
			expectedIsMaxRange: true,
		},
		{
			name:               "eth_getLogs limited with comma-formatted number",
			errorMsg:           `eth_getLogs is limited to a 10,000 range`,
			expectedMaxBlock:   10000,
			expectedIsMaxRange: true,
		},
		{
			name:               "eth_getLogs limited without comma",
			errorMsg:           `eth_getLogs is limited to a 5000 range`,
			expectedMaxBlock:   5000,
			expectedIsMaxRange: true,
		},
		{
			name:               "eth_getLogs limited embedded in JSON error",
			errorMsg:           `413 Request Entity Too Large: {"jsonrpc":"2.0","id":1021041,"error":{"code":-32614,"message":"eth_getLogs is limited to a 10,000 range"}}`,
			expectedMaxBlock:   10000,
			expectedIsMaxRange: true,
		},
		{
			name:               "eth_getLogs limited with large comma-formatted number",
			errorMsg:           `eth_getLogs is limited to a 100,000 range`,
			expectedMaxBlock:   100000,
			expectedIsMaxRange: true,
		},
		{
			name:               "query exceeds max block range",
			errorMsg:           "query exceeds max block range 100000",
			expectedMaxBlock:   100000,
			expectedIsMaxRange: true,
		},
		{
			name:               "query exceeds max block range embedded in longer message",
			errorMsg:           "claimsync: FilterLogs error: query exceeds max block range 100000",
			expectedMaxBlock:   100000,
			expectedIsMaxRange: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, isMaxRangeErr := ParseMaxRangeFromError(tt.errorMsg)
			if tt.expectedIsMaxRange {
				require.True(t, isMaxRangeErr)
			} else {
				require.False(t, isMaxRangeErr)
				require.Equal(t, tt.expectedMaxBlock, result)
			}
		})
	}
}

func TestParseMaxRangeFromError_WrappedErrors(t *testing.T) {
	t.Run("wrapped error with fmt.Errorf", func(t *testing.T) {
		baseErr := errors.New("block range too large, max range: 2000")
		wrappedErr := fmt.Errorf("RPC error: %w", baseErr)

		result, isMaxRangeErr := ParseMaxRangeFromError(wrappedErr.Error())
		require.True(t, isMaxRangeErr)
		require.Equal(t, uint64(2000), result)
	})

	t.Run("multiple levels of wrapping", func(t *testing.T) {
		baseErr := errors.New("block range too large, max range: 3000")
		wrappedErr1 := fmt.Errorf("contract call failed: %w", baseErr)
		wrappedErr2 := fmt.Errorf("query failed: %w", wrappedErr1)

		result, isMaxRangeErr := ParseMaxRangeFromError(wrappedErr2.Error())
		require.True(t, isMaxRangeErr)
		require.Equal(t, uint64(3000), result)
	})

	t.Run("error with prefix", func(t *testing.T) {
		errMsg := "execution reverted: block range too large, max range: 1500"

		result, isMaxRangeErr := ParseMaxRangeFromError(errMsg)
		require.True(t, isMaxRangeErr)
		require.Equal(t, uint64(1500), result)
	})

	t.Run("error with suffix", func(t *testing.T) {
		errMsg := "block range too large, max range: 2500, please reduce range"

		result, isMaxRangeErr := ParseMaxRangeFromError(errMsg)
		require.True(t, isMaxRangeErr)
		require.Equal(t, uint64(2500), result)
	})
}
