package types

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestSyncSegment_IsValid(t *testing.T) {
	addr := common.HexToAddress("0x123")

	tests := []struct {
		name     string
		segment  *SyncSegment
		expected bool
		reason   string
	}{
		{
			name:     "nil segment is valid",
			segment:  nil,
			expected: true,
			reason:   "nil segment is considered empty, so it's valid",
		},
		{
			name: "empty segment with BlockRangeZero is valid",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.BlockRangeZero,
				TargetToBlock: aggkittypes.FinalizedBlock,
			},
			expected: true,
			reason:   "empty BlockRange is valid",
		},
		{
			name: "segment with invalid range (from > to) is valid",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.NewBlockRange(10, 5),
				TargetToBlock: aggkittypes.FinalizedBlock,
			},
			expected: true,
			reason:   "invalid range is considered empty, so it's valid",
		},
		{
			name: "segment with {0,0} non-empty range is INVALID",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.NewBlockRange(0, 0),
				TargetToBlock: aggkittypes.FinalizedBlock,
			},
			expected: false,
			reason:   "{0,0} is reserved for DB empty representation, forbidden in multidownloader",
		},
		{
			name: "segment with valid range {1,10} is valid",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.NewBlockRange(1, 10),
				TargetToBlock: aggkittypes.FinalizedBlock,
			},
			expected: true,
			reason:   "normal valid range",
		},
		{
			name: "segment with valid range {0,5} is valid",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.NewBlockRange(0, 5),
				TargetToBlock: aggkittypes.FinalizedBlock,
			},
			expected: true,
			reason:   "range starting at 0 is valid as long as it's not {0,0}",
		},
		{
			name: "segment with single block {5,5} is valid",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.NewBlockRange(5, 5),
				TargetToBlock: aggkittypes.FinalizedBlock,
			},
			expected: true,
			reason:   "single block range is valid",
		},
		{
			name: "segment with large range is valid",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.NewBlockRange(1000, 999999),
				TargetToBlock: aggkittypes.LatestBlock,
			},
			expected: true,
			reason:   "large ranges are valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.segment.IsValid()
			require.Equal(t, tt.expected, got,
				"IsValid() for %s: expected %v, got %v. Reason: %s",
				tt.name, tt.expected, got, tt.reason)
		})
	}
}

func TestSyncSegment_IsEmpty(t *testing.T) {
	addr := common.HexToAddress("0x123")

	tests := []struct {
		name     string
		segment  *SyncSegment
		expected bool
	}{
		{
			name:     "nil segment is empty",
			segment:  nil,
			expected: true,
		},
		{
			name: "segment with BlockRangeZero is empty",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.BlockRangeZero,
				TargetToBlock: aggkittypes.FinalizedBlock,
			},
			expected: true,
		},
		{
			name: "segment with invalid range (from > to) is empty",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.NewBlockRange(10, 5),
				TargetToBlock: aggkittypes.FinalizedBlock,
			},
			expected: true,
		},
		{
			name: "segment with {0,0} is not empty",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.NewBlockRange(0, 0),
				TargetToBlock: aggkittypes.FinalizedBlock,
			},
			expected: false,
		},
		{
			name: "segment with valid range {1,10} is not empty",
			segment: &SyncSegment{
				ContractAddr:  addr,
				BlockRange:    aggkitcommon.NewBlockRange(1, 10),
				TargetToBlock: aggkittypes.FinalizedBlock,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.segment.IsEmpty()
			require.Equal(t, tt.expected, got,
				"IsEmpty() for %s: expected %v, got %v",
				tt.name, tt.expected, got)
		})
	}
}
