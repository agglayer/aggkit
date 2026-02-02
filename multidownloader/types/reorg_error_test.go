package types

import (
	"errors"
	"fmt"
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestReorgDetectionReason_String(t *testing.T) {
	tests := []struct {
		name     string
		reason   ReorgDetectionReason
		expected string
	}{
		{
			name:     "BlockHashMismatch",
			reason:   ReorgDetectionReason_BlockHashMismatch,
			expected: "BlockHashMismatch",
		},
		{
			name:     "ParentHashMismatch",
			reason:   ReorgDetectionReason_ParentHashMismatch,
			expected: "ParentHashMismatch",
		},
		{
			name:     "MissingBlock",
			reason:   ReorgDetectionReason_MissingBlock,
			expected: "MissingBlock",
		},
		{
			name:     "Forced",
			reason:   ReorgDetectionReason_Forced,
			expected: "Forced",
		},
		{
			name:     "Unknown reason",
			reason:   ReorgDetectionReason(99),
			expected: "ReorgDetectionReason(99)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.reason.String()
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestNewDetectedReorgError(t *testing.T) {
	blockNum := uint64(100)
	reason := ReorgDetectionReason_BlockHashMismatch
	oldHash := common.HexToHash("0x1234")
	newHash := common.HexToHash("0x5678")
	msg := "test message"

	err := NewDetectedReorgError(blockNum, reason, oldHash, newHash, msg)

	require.NotNil(t, err)
	require.Equal(t, blockNum, err.OffendingBlockNumber)
	require.Equal(t, reason, err.ReorgDetectionReason)
	require.Equal(t, oldHash, err.OldHash)
	require.Equal(t, newHash, err.NewHash)
	require.Equal(t, msg, err.Message)
}

func TestDetectedReorgError_Error(t *testing.T) {
	blockNum := uint64(100)
	oldHash := common.HexToHash("0x1234")
	newHash := common.HexToHash("0x5678")
	msg := "test message"

	tests := []struct {
		name           string
		reason         ReorgDetectionReason
		expectedPrefix string
	}{
		{
			name:           "MissingBlock error message",
			reason:         ReorgDetectionReason_MissingBlock,
			expectedPrefix: "reorgError: block number 100 is missing: test message",
		},
		{
			name:           "BlockHashMismatch error message",
			reason:         ReorgDetectionReason_BlockHashMismatch,
			expectedPrefix: fmt.Sprintf("reorgError: block number 100: old hash %s != new hash %s: test message", oldHash.String(), newHash.String()),
		},
		{
			name:           "ParentHashMismatch error message",
			reason:         ReorgDetectionReason_ParentHashMismatch,
			expectedPrefix: fmt.Sprintf("reorgError: block number 100: old parent hash %s != new parent hash %s: test message", oldHash.String(), newHash.String()),
		},
		{
			name:           "Forced error message",
			reason:         ReorgDetectionReason_Forced,
			expectedPrefix: "reorgError: block number 100: forced reason: test message",
		},
		{
			name:           "Unknown reason error message",
			reason:         ReorgDetectionReason(99),
			expectedPrefix: "reorgError: block number 100: reason 99: test message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewDetectedReorgError(blockNum, tt.reason, oldHash, newHash, msg)
			result := err.Error()
			require.Equal(t, tt.expectedPrefix, result)
		})
	}
}

func TestIsDetectedReorgError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Valid DetectedReorgError",
			err:      NewDetectedReorgError(100, ReorgDetectionReason_BlockHashMismatch, common.Hash{}, common.Hash{}, "test"),
			expected: true,
		},
		{
			name:     "Wrapped DetectedReorgError",
			err:      fmt.Errorf("wrapped: %w", NewDetectedReorgError(100, ReorgDetectionReason_BlockHashMismatch, common.Hash{}, common.Hash{}, "test")),
			expected: true,
		},
		{
			name:     "Regular error",
			err:      errors.New("regular error"),
			expected: false,
		},
		{
			name:     "Nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsDetectedReorgError(tt.err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestCastDetectedReorgError(t *testing.T) {
	originalErr := NewDetectedReorgError(100, ReorgDetectionReason_BlockHashMismatch, common.HexToHash("0x1234"), common.HexToHash("0x5678"), "test")

	tests := []struct {
		name        string
		err         error
		expectNil   bool
		expectEqual *DetectedReorgError
	}{
		{
			name:        "Valid DetectedReorgError",
			err:         originalErr,
			expectNil:   false,
			expectEqual: originalErr,
		},
		{
			name:        "Wrapped DetectedReorgError",
			err:         fmt.Errorf("wrapped: %w", originalErr),
			expectNil:   false,
			expectEqual: originalErr,
		},
		{
			name:        "Regular error",
			err:         errors.New("regular error"),
			expectNil:   true,
			expectEqual: nil,
		},
		{
			name:        "Nil error",
			err:         nil,
			expectNil:   true,
			expectEqual: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CastDetectedReorgError(tt.err)
			if tt.expectNil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				require.Equal(t, tt.expectEqual, result)
			}
		})
	}
}

func TestNewReorgedError(t *testing.T) {
	blockRange := aggkitcommon.NewBlockRange(100, 200)
	chainID := uint64(1)
	msg := "test message"

	err := NewReorgedError(blockRange, chainID, msg)

	require.NotNil(t, err)
	require.Equal(t, blockRange, err.BlockRangeReorged)
	require.Equal(t, chainID, err.ReorgedChainID)
	require.Equal(t, msg, err.Message)
}

func TestReorgedError_Error(t *testing.T) {
	blockRange := aggkitcommon.NewBlockRange(100, 200)
	chainID := uint64(1)
	msg := "test message"

	err := NewReorgedError(blockRange, chainID, msg)
	result := err.Error()

	expected := fmt.Sprintf("reorgedError: chainID=%d blockRangeReorged=%s: %s", chainID, blockRange.String(), msg)
	require.Equal(t, expected, result)
}

func TestIsReorgedError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Valid ReorgedError",
			err:      NewReorgedError(aggkitcommon.NewBlockRange(100, 200), 1, "test"),
			expected: true,
		},
		{
			name:     "Wrapped ReorgedError",
			err:      fmt.Errorf("wrapped: %w", NewReorgedError(aggkitcommon.NewBlockRange(100, 200), 1, "test")),
			expected: true,
		},
		{
			name:     "Regular error",
			err:      errors.New("regular error"),
			expected: false,
		},
		{
			name:     "Nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsReorgedError(tt.err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestCastReorgedError(t *testing.T) {
	originalErr := NewReorgedError(aggkitcommon.NewBlockRange(100, 200), 1, "test")

	tests := []struct {
		name        string
		err         error
		expectNil   bool
		expectEqual *ReorgedError
	}{
		{
			name:        "Valid ReorgedError",
			err:         originalErr,
			expectNil:   false,
			expectEqual: originalErr,
		},
		{
			name:        "Wrapped ReorgedError",
			err:         fmt.Errorf("wrapped: %w", originalErr),
			expectNil:   false,
			expectEqual: originalErr,
		},
		{
			name:        "Regular error",
			err:         errors.New("regular error"),
			expectNil:   true,
			expectEqual: nil,
		},
		{
			name:        "Nil error",
			err:         nil,
			expectNil:   true,
			expectEqual: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CastReorgedError(tt.err)
			if tt.expectNil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				require.Equal(t, tt.expectEqual, result)
			}
		})
	}
}
