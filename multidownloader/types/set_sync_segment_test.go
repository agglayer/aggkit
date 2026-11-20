package types

import (
	"math"
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestNewSetSyncSegment(t *testing.T) {
	set := NewSetSyncSegment()
	require.NotNil(t, set.segments)
	require.Empty(t, set.segments)
}

func TestSetSyncSegment_String(t *testing.T) {
	set := NewSetSyncSegment()
	segment := &SyncSegment{
		ContractAddr: common.HexToAddress("0x123"),
		BlockRange:   aggkitcommon.NewBlockRange(1, 10),
	}
	set.segments = []*SyncSegment{segment}

	result := set.String()
	require.Contains(t, result, "SetSyncSegment:")
	require.Contains(t, result, "SyncSegment[0]=")
}

func TestSetSyncSegment_Add(t *testing.T) {
	t.Run("add new segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		segment := SyncSegment{
			ContractAddr: common.HexToAddress("0x123"),
			BlockRange:   aggkitcommon.NewBlockRange(1, 10),
		}

		set.Add(segment)
		require.Len(t, set.segments, 1)
		require.Equal(t, segment.ContractAddr, set.segments[0].ContractAddr)
	})

	t.Run("merge existing segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment1 := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 10),
		}
		segment2 := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(5, 15),
		}

		set.Add(segment1)
		set.Add(segment2)
		res, exists := set.GetByContract(addr)
		require.True(t, exists)
		require.Equal(t, uint64(1), res.BlockRange.FromBlock)
		require.Equal(t, uint64(15), res.BlockRange.ToBlock)
	})

	t.Run("merge from aggkitcommon.BlockRangeZero", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment1 := SyncSegment{
			ContractAddr: addr,
			// That means no sync
			BlockRange:    aggkitcommon.BlockRangeZero,
			TargetToBlock: aggkittypes.LatestBlock,
		}
		segment2 := SyncSegment{
			ContractAddr:  addr,
			BlockRange:    aggkitcommon.NewBlockRange(5, 15),
			TargetToBlock: aggkittypes.LatestBlock,
		}
		set.Add(segment1)
		set.Add(segment2)
		res, exists := set.GetByContract(addr)
		require.True(t, exists)
		require.Equal(t, uint64(5), res.BlockRange.FromBlock)
		require.Equal(t, uint64(15), res.BlockRange.ToBlock)
		segment3 := SyncSegment{
			ContractAddr:  addr,
			BlockRange:    aggkitcommon.NewBlockRange(2, 5),
			TargetToBlock: aggkittypes.LatestBlock,
		}

		set.Add(segment3)
		res, exists = set.GetByContract(addr)
		require.True(t, exists)
		require.Equal(t, uint64(2), res.BlockRange.FromBlock)
		require.Equal(t, uint64(15), res.BlockRange.ToBlock)
	})
}

func TestSetSyncSegment_GetByContract(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var set *SetSyncSegment
		_, exists := set.GetByContract(common.HexToAddress("0x123"))
		require.False(t, exists)
	})

	t.Run("segment found", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := NewSyncSegment(addr, aggkitcommon.NewBlockRange(1, 10),
			aggkittypes.LatestBlock, true)
		set.Add(segment)
		_, exists := set.GetByContract(addr)
		require.True(t, exists)
	})

	t.Run("segment not found", func(t *testing.T) {
		set := NewSetSyncSegment()
		_, exists := set.GetByContract(common.HexToAddress("0x123"))
		require.False(t, exists)
	})
}

func TestSetSyncSegment_Subtract(t *testing.T) {
	t.Run("nil segments", func(t *testing.T) {
		set := NewSetSyncSegment()
		setCopy := set.Clone()
		err := set.SubtractSegments(nil)
		require.NoError(t, err)
		require.Equal(t, *setCopy, set)
	})

	t.Run("subtract segments", func(t *testing.T) {
		set1 := NewSetSyncSegment()
		set2 := NewSetSyncSegment()

		addr := common.HexToAddress("0x123")
		segment1 := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(10, 20),
		}

		set1.Add(segment1)
		set2.Add(segment2)

		result := set1.SubtractSegments(&set2)
		require.NotNil(t, result)
	})

	t.Run("subtract from empty BlockRange", func(t *testing.T) {
		set1 := NewSetSyncSegment()
		set2 := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		set1.Add(SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.BlockRangeZero,
		})
		set2.Add(SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(0, 20),
		})
		emptySetStr := set1.String()
		Set2Str := set2.String()
		// {empty} - {0-20} = {empty}

		err := set1.SubtractSegments(&set2)
		require.NoError(t, err)
		require.Equal(t, emptySetStr, set1.String())

		// {0-20} - {empty} = {0-20}
		err = set2.SubtractSegments(&set1)
		require.NoError(t, err)
		require.Equal(t, Set2Str, set2.String())
	})
}

func TestSetSyncSegment_TotalBlocks(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var set *SetSyncSegment
		result := set.TotalBlocks()
		require.Equal(t, uint64(0), result)
	})

	t.Run("calculate total blocks", func(t *testing.T) {
		set := NewSetSyncSegment()
		segment1 := &SyncSegment{
			ContractAddr: common.HexToAddress("0x123"),
			BlockRange:   aggkitcommon.NewBlockRange(1, 10),
		}
		segment2 := &SyncSegment{
			ContractAddr: common.HexToAddress("0x456"),
			BlockRange:   aggkitcommon.NewBlockRange(5, 20),
		}
		segment3 := &SyncSegment{
			ContractAddr: common.HexToAddress("0x457"),
			BlockRange:   aggkitcommon.NewBlockRange(501, 510),
		}
		set.segments = []*SyncSegment{segment1, segment2, segment3}
		require.Equal(t, uint64(30), set.TotalBlocks())
		set.segments = []*SyncSegment{segment2, segment3, segment1}
		require.Equal(t, uint64(30), set.TotalBlocks())
		set.segments = []*SyncSegment{segment2, segment1, segment3}
		require.Equal(t, uint64(30), set.TotalBlocks())

		set.segments = []*SyncSegment{segment1}
		require.Equal(t, uint64(10), set.TotalBlocks())
		set.segments = []*SyncSegment{segment1, segment2}
		require.Equal(t, uint64(20), set.TotalBlocks())
	})
}

func TestSetSyncSegment_IsAvailable(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var set *SetSyncSegment
		query := LogQuery{
			Addrs:      []common.Address{common.HexToAddress("0x123")},
			BlockRange: aggkitcommon.NewBlockRange(1, 10),
		}
		result := set.IsAvailable(query)
		require.False(t, result)
	})

	t.Run("segment not available", func(t *testing.T) {
		set := NewSetSyncSegment()
		query := LogQuery{
			Addrs:      []common.Address{common.HexToAddress("0x123")},
			BlockRange: aggkitcommon.NewBlockRange(1, 10),
		}
		result := set.IsAvailable(query)
		require.False(t, result)
	})
}

func TestSetSyncSegment_IsPartiallyAvailable(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var set *SetSyncSegment
		query := LogQuery{
			Addrs:      []common.Address{common.HexToAddress("0x123")},
			BlockRange: aggkitcommon.NewBlockRange(1, 10),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.False(t, available)
		require.Nil(t, result)
	})

	t.Run("empty addresses in query", func(t *testing.T) {
		set := NewSetSyncSegment()
		query := LogQuery{
			Addrs:      []common.Address{},
			BlockRange: aggkitcommon.NewBlockRange(1, 10),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.False(t, available)
		require.Nil(t, result)
	})

	t.Run("address not synced at all", func(t *testing.T) {
		set := NewSetSyncSegment()
		query := LogQuery{
			Addrs:      []common.Address{common.HexToAddress("0x123")},
			BlockRange: aggkitcommon.NewBlockRange(1, 10),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.False(t, available)
		require.Nil(t, result)
	})

	t.Run("no overlap between query and segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(50, 100),
		}
		set.Add(segment)

		query := LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(1, 10),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.False(t, available)
		require.Nil(t, result)
	})

	t.Run("gap at the beginning - segment starts after FromBlock", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(5, 100),
		}
		set.Add(segment)

		query := LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(1, 50),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.False(t, available)
		require.Nil(t, result)
	})

	t.Run("partially available - segment covers beginning but not all", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 50),
		}
		set.Add(segment)

		query := LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(1, 100),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.True(t, available)
		require.NotNil(t, result)
		require.Equal(t, uint64(1), result.BlockRange.FromBlock)
		require.Equal(t, uint64(50), result.BlockRange.ToBlock)
		require.Equal(t, []common.Address{addr}, result.Addrs)
	})

	t.Run("fully available - segment covers entire query range", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment)

		query := LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(1, 50),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.True(t, available)
		require.NotNil(t, result)
		require.Equal(t, uint64(1), result.BlockRange.FromBlock)
		require.Equal(t, uint64(50), result.BlockRange.ToBlock)
		require.Equal(t, []common.Address{addr}, result.Addrs)
	})

	t.Run("multiple addresses - all have partial data, find bottleneck", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 70),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(1, 50), // Bottleneck
		}
		set.Add(segment1)
		set.Add(segment2)

		query := LogQuery{
			Addrs:      []common.Address{addr1, addr2},
			BlockRange: aggkitcommon.NewBlockRange(1, 100),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.True(t, available)
		require.NotNil(t, result)
		require.Equal(t, uint64(1), result.BlockRange.FromBlock)
		require.Equal(t, uint64(50), result.BlockRange.ToBlock)
		require.Equal(t, []common.Address{addr1, addr2}, result.Addrs)
	})

	t.Run("multiple addresses - one has gap at beginning", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(10, 100), // Gap at beginning
		}
		set.Add(segment1)
		set.Add(segment2)

		query := LogQuery{
			Addrs:      []common.Address{addr1, addr2},
			BlockRange: aggkitcommon.NewBlockRange(1, 100),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.False(t, available)
		require.Nil(t, result)
	})

	t.Run("multiple addresses - one not synced at all", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment1)
		// addr2 not added

		query := LogQuery{
			Addrs:      []common.Address{addr1, addr2},
			BlockRange: aggkitcommon.NewBlockRange(1, 100),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.False(t, available)
		require.Nil(t, result)
	})

	t.Run("segment extends beyond query range", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 200),
		}
		set.Add(segment)

		query := LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(1, 100),
		}
		available, result := set.IsPartiallyAvailable(query)
		require.True(t, available)
		require.NotNil(t, result)
		require.Equal(t, uint64(1), result.BlockRange.FromBlock)
		require.Equal(t, uint64(100), result.BlockRange.ToBlock)
	})
}

func TestSetSyncSegment_NextQuery(t *testing.T) {
	t.Run("nil or empty segments", func(t *testing.T) {
		var set *SetSyncSegment
		query, err := set.NextQuery(100, 0, false)
		require.Nil(t, query)
		require.Equal(t, ErrFinished, err)

		emptySet := NewSetSyncSegment()
		query, err = emptySet.NextQuery(100, 0, false)
		require.Nil(t, query)
		require.Equal(t, ErrFinished, err)
	})
}

func TestSetSyncSegment_GetLowestFromBlockSegment(t *testing.T) {
	t.Run("nil or empty segments", func(t *testing.T) {
		var set *SetSyncSegment
		result := set.GetLowestFromBlockSegment()
		require.Nil(t, result)

		emptySet := NewSetSyncSegment()
		result = emptySet.GetLowestFromBlockSegment()
		require.Nil(t, result)
	})
}

func TestSetSyncSegment_GetAddressesForBlockRange(t *testing.T) {
	set := NewSetSyncSegment()
	segment := &SyncSegment{
		ContractAddr: common.HexToAddress("0x123"),
		BlockRange:   aggkitcommon.NewBlockRange(1, 10),
	}
	set.segments = []*SyncSegment{segment}

	blockRange := aggkitcommon.NewBlockRange(5, 15)
	addresses := set.GetAddressesForBlockRange(blockRange)
	require.NotEmpty(t, addresses)
}

func TestSetSyncSegment_Finished(t *testing.T) {
	t.Run("nil set", func(t *testing.T) {
		var set *SetSyncSegment
		require.True(t, set.Finished())
	})

	t.Run("empty set", func(t *testing.T) {
		set := NewSetSyncSegment()
		require.True(t, set.Finished())
	})

	t.Run("non-empty set", func(t *testing.T) {
		set := NewSetSyncSegment()
		segment := &SyncSegment{
			ContractAddr: common.HexToAddress("0x123"),
			BlockRange:   aggkitcommon.NewBlockRange(1, 10),
		}
		set.segments = []*SyncSegment{segment}
		require.False(t, set.Finished())
	})
	t.Run("empty segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		segment := &SyncSegment{
			ContractAddr: common.HexToAddress("0x123"),
			BlockRange:   aggkitcommon.NewBlockRange(1, 10),
		}
		segment.Empty()
		set.segments = []*SyncSegment{segment}
		require.True(t, set.Finished())
	})
}

func TestSetSyncSegment_Clone(t *testing.T) {
	t.Run("nil set", func(t *testing.T) {
		var set *SetSyncSegment
		result := set.Clone()
		require.Nil(t, result)
	})

	t.Run("clone set", func(t *testing.T) {
		set := NewSetSyncSegment()
		segment := SyncSegment{
			ContractAddr: common.HexToAddress("0x123"),
			BlockRange:   aggkitcommon.NewBlockRange(1, 10),
		}
		set.Add(segment)

		cloned := set.Clone()
		require.NotNil(t, cloned)
		require.Len(t, cloned.segments, 1)
	})
}

func TestSetSyncSegment_Remove(t *testing.T) {
	t.Run("nil set or segment", func(t *testing.T) {
		var set *SetSyncSegment
		set.Remove(nil)
		// Should not panic

		validSet := NewSetSyncSegment()
		validSet.Remove(nil)
		// Should not panic
	})
}

func TestSetSyncSegment_UpdateBlockRange(t *testing.T) {
	t.Run("nil set or segment", func(t *testing.T) {
		var set *SetSyncSegment
		newRange := aggkitcommon.NewBlockRange(1, 10)
		set.UpdateBlockRange(nil, newRange)
		// Should not panic

		validSet := NewSetSyncSegment()
		validSet.UpdateBlockRange(nil, newRange)
		// Should not panic
	})

	t.Run("update segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 10),
		}
		set.Add(segment)

		newRange := aggkitcommon.NewBlockRange(5, 15)
		set.UpdateBlockRange(&segment, newRange)

		updatedSegment, exists := set.GetByContract(addr)
		require.True(t, exists)
		require.Equal(t, newRange, updatedSegment.BlockRange)
	})
}

func TestSetSyncSegment_RemoveLogQuerySegment(t *testing.T) {
	t.Run("nil set or query", func(t *testing.T) {
		var set *SetSyncSegment
		require.NoError(t, set.SubtractLogQuery(nil))

		validSet := NewSetSyncSegment()
		require.NoError(t, validSet.SubtractLogQuery(nil))
		// Should not panic
	})

	t.Run("remove partial segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment)

		logQuery := &LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(1, 30),
		}

		err := set.SubtractLogQuery(logQuery)
		require.NoError(t, err)
		res, exists := set.GetByContract(addr)
		require.True(t, exists)
		require.Equal(t, uint64(31), res.BlockRange.FromBlock)
		require.Equal(t, uint64(100), res.BlockRange.ToBlock)
	})

	t.Run("fulfill totally a segment,set it as empty", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment)

		logQuery := &LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(1, 200),
		}

		err := set.SubtractLogQuery(logQuery)
		require.NoError(t, err)
		segment, exists := set.GetByContract(addr)
		require.True(t, segment.IsEmpty(), "segment is empty")
		require.True(t, exists, "is empty but exists")
	})

	t.Run("bad removed segment (middle segment)", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123124543423")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment)

		logQuery := &LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(10, 20),
		}

		err := set.SubtractLogQuery(logQuery)
		require.Error(t, err)
	})
}

func TestSetSyncSegment_GetTotalPendingBlockRange_WithEmptySegments(t *testing.T) {
	t.Run("a segment with empty range", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr:  addr,
			BlockRange:    aggkitcommon.BlockRangeZero,
			TargetToBlock: aggkittypes.LatestBlock,
		}
		set.Add(segment)
		br := set.GetTotalPendingBlockRange()
		require.Nil(t, br)
	})
	t.Run("single empty segment returns nil", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr:  addr,
			BlockRange:    aggkitcommon.NewBlockRange(1, 100),
			TargetToBlock: aggkittypes.LatestBlock,
		}
		set.Add(segment)

		// Sync everything
		logQuery := &LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(1, 100),
		}
		err := set.SubtractLogQuery(logQuery)
		require.NoError(t, err)

		// Verify segment is empty
		segment, exists := set.GetByContract(addr)
		require.True(t, exists)
		require.True(t, segment.IsEmpty())

		// GetTotalPendingBlockRange should return nil, not an invalid range
		totalRange := set.GetTotalPendingBlockRange()
		require.Nil(t, totalRange, "should return nil when all segments are empty")
	})

	t.Run("multiple segments with some empty", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		// Add two segments
		segment1 := SyncSegment{
			ContractAddr:  addr1,
			BlockRange:    aggkitcommon.NewBlockRange(1, 100),
			TargetToBlock: aggkittypes.LatestBlock,
		}
		segment2 := SyncSegment{
			ContractAddr:  addr2,
			BlockRange:    aggkitcommon.NewBlockRange(50, 150),
			TargetToBlock: aggkittypes.LatestBlock,
		}
		set.Add(segment1)
		set.Add(segment2)

		// Sync first segment completely
		logQuery := &LogQuery{
			Addrs:      []common.Address{addr1},
			BlockRange: aggkitcommon.NewBlockRange(1, 100),
		}
		err := set.SubtractLogQuery(logQuery)
		require.NoError(t, err)

		// First segment should be empty
		seg1, exists := set.GetByContract(addr1)
		require.True(t, exists)
		require.True(t, seg1.IsEmpty())

		// Second segment should not be empty
		seg2, exists := set.GetByContract(addr2)
		require.True(t, exists)
		require.False(t, seg2.IsEmpty())

		// GetTotalPendingBlockRange should return only the non-empty segment range
		totalRange := set.GetTotalPendingBlockRange()
		require.NotNil(t, totalRange)
		require.Equal(t, uint64(50), totalRange.FromBlock)
		require.Equal(t, uint64(150), totalRange.ToBlock)
	})
}

func TestNewSetSyncSegmentFromLogQuery(t *testing.T) {
	t.Run("create from valid log query", func(t *testing.T) {
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")
		logQuery := &LogQuery{
			Addrs:      []common.Address{addr1, addr2},
			BlockRange: aggkitcommon.NewBlockRange(10, 100),
		}

		set, err := NewSetSyncSegmentFromLogQuery(logQuery)
		require.NoError(t, err)
		require.Len(t, set.segments, 2)

		seg1, exists := set.GetByContract(addr1)
		require.True(t, exists)
		require.Equal(t, uint64(10), seg1.BlockRange.FromBlock)
		require.Equal(t, uint64(100), seg1.BlockRange.ToBlock)

		seg2, exists := set.GetByContract(addr2)
		require.True(t, exists)
		require.Equal(t, uint64(10), seg2.BlockRange.FromBlock)
		require.Equal(t, uint64(100), seg2.BlockRange.ToBlock)
	})
}

func TestSetSyncSegment_GetTargetToBlockTags(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var set *SetSyncSegment
		result := set.GetTargetToBlockTags()
		require.Nil(t, result)
	})

	t.Run("empty set", func(t *testing.T) {
		set := NewSetSyncSegment()
		result := set.GetTargetToBlockTags()
		require.Empty(t, result)
	})

	t.Run("single segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		segment := SyncSegment{
			ContractAddr:  common.HexToAddress("0x123"),
			BlockRange:    aggkitcommon.NewBlockRange(1, 10),
			TargetToBlock: aggkittypes.FinalizedBlock,
		}
		set.Add(segment)

		result := set.GetTargetToBlockTags()
		require.Len(t, result, 1)
		require.Equal(t, aggkittypes.FinalizedBlock, result[0])
	})

	t.Run("multiple segments with same tag", func(t *testing.T) {
		set := NewSetSyncSegment()
		segment1 := SyncSegment{
			ContractAddr:  common.HexToAddress("0x111"),
			BlockRange:    aggkitcommon.NewBlockRange(1, 10),
			TargetToBlock: aggkittypes.LatestBlock,
		}
		segment2 := SyncSegment{
			ContractAddr:  common.HexToAddress("0x222"),
			BlockRange:    aggkitcommon.NewBlockRange(5, 15),
			TargetToBlock: aggkittypes.LatestBlock,
		}
		set.Add(segment1)
		set.Add(segment2)

		result := set.GetTargetToBlockTags()
		require.Len(t, result, 1)
		require.Equal(t, aggkittypes.LatestBlock, result[0])
	})

	t.Run("multiple segments with different tags", func(t *testing.T) {
		set := NewSetSyncSegment()
		segment1 := SyncSegment{
			ContractAddr:  common.HexToAddress("0x111"),
			BlockRange:    aggkitcommon.NewBlockRange(1, 10),
			TargetToBlock: aggkittypes.LatestBlock,
		}
		segment2 := SyncSegment{
			ContractAddr:  common.HexToAddress("0x222"),
			BlockRange:    aggkitcommon.NewBlockRange(5, 15),
			TargetToBlock: aggkittypes.FinalizedBlock,
		}
		segment3 := SyncSegment{
			ContractAddr:  common.HexToAddress("0x333"),
			BlockRange:    aggkitcommon.NewBlockRange(10, 20),
			TargetToBlock: aggkittypes.LatestBlock,
		}
		set.Add(segment1)
		set.Add(segment2)
		set.Add(segment3)

		result := set.GetTargetToBlockTags()
		require.Len(t, result, 2)
		require.Contains(t, result, aggkittypes.LatestBlock)
		require.Contains(t, result, aggkittypes.FinalizedBlock)
	})
}

func TestSetSyncSegment_GetHighestBlockNumber(t *testing.T) {
	t.Run("nil or empty set", func(t *testing.T) {
		var set *SetSyncSegment
		highest, finality := set.GetHighestBlockNumber()
		require.Equal(t, uint64(0), highest)
		require.Equal(t, aggkittypes.LatestBlock, finality)

		emptySet := NewSetSyncSegment()
		highest, finality = emptySet.GetHighestBlockNumber()
		require.Equal(t, uint64(0), highest)
		require.Equal(t, aggkittypes.LatestBlock, finality)
	})

	t.Run("single segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		segment := SyncSegment{
			ContractAddr:  common.HexToAddress("0x123"),
			BlockRange:    aggkitcommon.NewBlockRange(1, 100),
			TargetToBlock: aggkittypes.FinalizedBlock,
		}
		set.Add(segment)

		highest, finality := set.GetHighestBlockNumber()
		require.Equal(t, uint64(100), highest)
		require.Equal(t, aggkittypes.FinalizedBlock, finality)
	})

	t.Run("multiple segments", func(t *testing.T) {
		set := NewSetSyncSegment()
		segment1 := SyncSegment{
			ContractAddr:  common.HexToAddress("0x111"),
			BlockRange:    aggkitcommon.NewBlockRange(1, 50),
			TargetToBlock: aggkittypes.LatestBlock,
		}
		segment2 := SyncSegment{
			ContractAddr:  common.HexToAddress("0x222"),
			BlockRange:    aggkitcommon.NewBlockRange(10, 200),
			TargetToBlock: aggkittypes.FinalizedBlock,
		}
		segment3 := SyncSegment{
			ContractAddr:  common.HexToAddress("0x333"),
			BlockRange:    aggkitcommon.NewBlockRange(100, 150),
			TargetToBlock: aggkittypes.SafeBlock,
		}
		set.Add(segment1)
		set.Add(segment2)
		set.Add(segment3)

		highest, finality := set.GetHighestBlockNumber()
		require.Equal(t, uint64(200), highest)
		require.Equal(t, aggkittypes.FinalizedBlock, finality)
	})
}

func TestSetSyncSegment_GetAddressesForBlock(t *testing.T) {
	t.Run("single block within range", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(50, 150),
		}
		set.Add(segment1)
		set.Add(segment2)

		addresses := set.GetAddressesForBlock(75)
		require.Len(t, addresses, 2)
		require.Contains(t, addresses, addr1)
		require.Contains(t, addresses, addr2)
	})

	t.Run("block outside all ranges", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment1)

		addresses := set.GetAddressesForBlock(200)
		require.Empty(t, addresses)
	})

	t.Run("block at range boundary", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(10, 20),
		}
		set.Add(segment)

		// Test at FromBlock
		addresses := set.GetAddressesForBlock(10)
		require.Len(t, addresses, 1)
		require.Contains(t, addresses, addr)

		// Test at ToBlock
		addresses = set.GetAddressesForBlock(20)
		require.Len(t, addresses, 1)
		require.Contains(t, addresses, addr)
	})
}

func TestSetSyncSegment_Empty(t *testing.T) {
	t.Run("empty existing segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment)

		// Get the segment reference
		seg, exists := set.GetByContract(addr)
		require.True(t, exists)
		require.False(t, seg.IsEmpty())

		// Empty it
		set.Empty(&seg)

		// Verify it's empty
		updatedSeg, exists := set.GetByContract(addr)
		require.True(t, exists)
		require.True(t, updatedSeg.IsEmpty())
	})

	t.Run("empty non-existent segment does nothing", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}

		// Try to empty a segment that's not in the set
		set.Empty(&segment)
		// Should not panic

		// Verify set is still empty
		require.Len(t, set.segments, 0)
	})
}

func TestSetSyncSegment_Remove_Complete(t *testing.T) {
	t.Run("remove existing segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(50, 150),
		}

		set.Add(segment1)
		set.Add(segment2)
		require.Len(t, set.segments, 2)

		// Remove first segment
		set.Remove(&segment1)
		require.Len(t, set.segments, 1)

		// Verify addr1 is gone
		_, exists := set.GetByContract(addr1)
		require.False(t, exists)

		// Verify addr2 still exists
		_, exists = set.GetByContract(addr2)
		require.True(t, exists)
	})

	t.Run("remove non-existent segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(50, 150),
		}

		set.Add(segment1)

		// Try to remove segment that's not in set
		set.Remove(&segment2)
		require.Len(t, set.segments, 1)

		// Verify addr1 still exists
		_, exists := set.GetByContract(addr1)
		require.True(t, exists)
	})
}

func TestSetSyncSegment_AddLogQuery(t *testing.T) {
	t.Run("nil set or query", func(t *testing.T) {
		var set *SetSyncSegment
		require.NoError(t, set.AddLogQuery(nil))

		validSet := NewSetSyncSegment()
		require.NoError(t, validSet.AddLogQuery(nil))
	})

	t.Run("add log query to empty set", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		logQuery := &LogQuery{
			Addrs:      []common.Address{addr1, addr2},
			BlockRange: aggkitcommon.NewBlockRange(10, 100),
		}

		err := set.AddLogQuery(logQuery)
		require.NoError(t, err)
		require.Len(t, set.segments, 2)

		seg1, exists := set.GetByContract(addr1)
		require.True(t, exists)
		require.Equal(t, uint64(10), seg1.BlockRange.FromBlock)
		require.Equal(t, uint64(100), seg1.BlockRange.ToBlock)

		seg2, exists := set.GetByContract(addr2)
		require.True(t, exists)
		require.Equal(t, uint64(10), seg2.BlockRange.FromBlock)
		require.Equal(t, uint64(100), seg2.BlockRange.ToBlock)
	})

	t.Run("add log query with overlapping ranges", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		// Add initial segment
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 50),
		}
		set.Add(segment)

		// Add log query with overlapping range
		logQuery := &LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(40, 100),
		}

		err := set.AddLogQuery(logQuery)
		require.NoError(t, err)

		// Should merge the ranges
		seg, exists := set.GetByContract(addr)
		require.True(t, exists)
		require.Equal(t, uint64(1), seg.BlockRange.FromBlock)
		require.Equal(t, uint64(100), seg.BlockRange.ToBlock)
	})
}

func TestSetSyncSegment_SegmentsByContract(t *testing.T) {
	t.Run("get segments for addresses", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")
		addr3 := common.HexToAddress("0x333")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(50, 150),
		}
		segment3 := SyncSegment{
			ContractAddr: addr3,
			BlockRange:   aggkitcommon.NewBlockRange(100, 200),
		}

		set.Add(segment1)
		set.Add(segment2)
		set.Add(segment3)

		// Get segments for addr1 and addr2
		result := set.SegmentsByContract([]common.Address{addr1, addr2})
		require.Len(t, result, 2)
		require.Equal(t, addr1, result[0].ContractAddr)
		require.Equal(t, addr2, result[1].ContractAddr)
	})

	t.Run("get segments for non-existent addresses", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment1)

		// Try to get segment for addr2 which doesn't exist
		result := set.SegmentsByContract([]common.Address{addr2})
		require.Empty(t, result)
	})

	t.Run("empty address list", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment)

		result := set.SegmentsByContract([]common.Address{})
		require.Empty(t, result)
	})
}

func TestSetSyncSegment_GetContracts(t *testing.T) {
	t.Run("empty set", func(t *testing.T) {
		set := NewSetSyncSegment()
		contracts := set.GetContracts()
		require.Empty(t, contracts)
	})

	t.Run("get all contracts", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")
		addr3 := common.HexToAddress("0x333")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(50, 150),
		}
		segment3 := SyncSegment{
			ContractAddr: addr3,
			BlockRange:   aggkitcommon.NewBlockRange(100, 200),
		}

		set.Add(segment1)
		set.Add(segment2)
		set.Add(segment3)

		contracts := set.GetContracts()
		require.Len(t, contracts, 3)
		require.Contains(t, contracts, addr1)
		require.Contains(t, contracts, addr2)
		require.Contains(t, contracts, addr3)
	})
}

func TestSetSyncSegment_GetSegments(t *testing.T) {
	t.Run("empty set", func(t *testing.T) {
		set := NewSetSyncSegment()
		segments := set.GetSegments()
		require.Empty(t, segments)
	})

	t.Run("get all segments", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(50, 150),
		}

		set.Add(segment1)
		set.Add(segment2)

		segments := set.GetSegments()
		require.Len(t, segments, 2)
		require.Equal(t, addr1, segments[0].ContractAddr)
		require.Equal(t, addr2, segments[1].ContractAddr)
	})
}

func TestSetSyncSegment_IsAvailable_PositiveCases(t *testing.T) {
	t.Run("query fully available for single address", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment)

		query := LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(10, 50),
		}
		result := set.IsAvailable(query)
		require.True(t, result)
	})

	t.Run("query fully available for multiple addresses", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment1)
		set.Add(segment2)

		query := LogQuery{
			Addrs:      []common.Address{addr1, addr2},
			BlockRange: aggkitcommon.NewBlockRange(10, 50),
		}
		result := set.IsAvailable(query)
		require.True(t, result)
	})

	t.Run("query not available - one address missing coverage", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(1, 30), // Doesn't cover full range
		}
		set.Add(segment1)
		set.Add(segment2)

		query := LogQuery{
			Addrs:      []common.Address{addr1, addr2},
			BlockRange: aggkitcommon.NewBlockRange(10, 50),
		}
		result := set.IsAvailable(query)
		require.False(t, result)
	})
}

func TestSetSyncSegment_NextQuery_PositiveCases(t *testing.T) {
	t.Run("generate next query without maxBlock limit", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 1000),
		}
		set.Add(segment)

		query, err := set.NextQuery(100, 0, false)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, uint64(1), query.BlockRange.FromBlock)
		require.Equal(t, uint64(100), query.BlockRange.ToBlock)
		require.Len(t, query.Addrs, 1)
		require.Contains(t, query.Addrs, addr)
	})

	t.Run("generate next query with maxBlock limit applied", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 1000),
		}
		set.Add(segment)

		query, err := set.NextQuery(100, 50, true)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, uint64(1), query.BlockRange.FromBlock)
		require.Equal(t, uint64(50), query.BlockRange.ToBlock)
	})

	t.Run("generate next query with multiple addresses in same range", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(10, 100),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(10, 100),
		}
		set.Add(segment1)
		set.Add(segment2)

		query, err := set.NextQuery(50, 0, false)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, uint64(10), query.BlockRange.FromBlock)
		require.Equal(t, uint64(59), query.BlockRange.ToBlock)
		require.Len(t, query.Addrs, 2)
	})

	t.Run("maxBlock limit results in empty range", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(100, 200),
		}
		set.Add(segment)

		// Max block is below the segment range
		query, err := set.NextQuery(100, 50, true)
		require.Error(t, err)
		require.Equal(t, ErrFinished, err)
		require.Nil(t, query)
	})

	t.Run("returns ErrFinished when lowest segment is empty", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		// Add an empty segment
		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.BlockRangeZero,
		}
		set.Add(segment)

		query, err := set.NextQuery(100, 0, false)
		require.Error(t, err)
		require.Equal(t, ErrFinished, err)
		require.Nil(t, query)
	})
}

// collectPlannedQueries drives NextQuery to completion, subtracting every
// returned query from the pending set (the same way OnNewSyncedLogQuery does in
// production) and returning the planned queries in order. It fails the test if
// the planner never finishes, returns an empty range or returns no addresses.
func collectPlannedQueries(t *testing.T, set *SetSyncSegment, chunkSize uint32,
	maxBlockNumber uint64, applyMaxBlockNumber bool) []LogQuery {
	t.Helper()
	queries := []LogQuery{}
	for i := 0; i < 10000; i++ {
		query, err := set.NextQuery(chunkSize, maxBlockNumber, applyMaxBlockNumber)
		if err == ErrFinished {
			return queries
		}
		require.NoError(t, err, "unexpected planner error at iteration %d", i)
		require.NotNil(t, query, "iteration %d", i)
		require.False(t, query.BlockRange.IsEmpty(), "iteration %d returned an empty range", i)
		require.NotEmpty(t, query.Addrs, "iteration %d returned no addresses", i)
		queries = append(queries, *query)
		require.NoError(t, set.SubtractLogQuery(query), "subtracting query at iteration %d", i)
	}
	t.Fatalf("planner did not finish after 10000 iterations (%d queries)", len(queries))
	return queries
}

// requireQueriesCoverExactly asserts that the planned queries are pairwise
// disjoint (no block is planned twice) and that their union is exactly want.
func requireQueriesCoverExactly(t *testing.T, queries []LogQuery, want aggkitcommon.BlockRange) {
	t.Helper()
	covered := aggkitcommon.BlockRangeZero
	for i, query := range queries {
		for j := 0; j < i; j++ {
			require.False(t, query.BlockRange.Overlaps(queries[j].BlockRange),
				"query %d (%s) overlaps query %d (%s)",
				i, query.BlockRange.String(), j, queries[j].BlockRange.String())
		}
		covered = covered.Extend(query.BlockRange)
	}
	require.Equal(t, want.String(), covered.String())
}

// TestSetSyncSegment_NextQuery_Regression1224 reproduces the pending-boundary
// fixture of issue #1224 with the exact production block numbers.
//
// Two contracts are fully pending over adjacent ranges:
//
//	pendingAddr: [26414042, 26414124]  (the "pending" contract, from 26414042)
//	bridgeAddr:  [26414125, 26414125]  (the "bridge" contract,  from 26414125)
//
// Before the fix the planner used GetLowestFromBlockSegment, which returns the
// empty segment (BlockRangeZero has FromBlock 0) once pendingAddr is fully
// synced. The initial query was [26414042, 26414124] over pendingAddr, but the
// follow-up returned ErrFinished, so bridgeAddr was never synced.
func TestSetSyncSegment_NextQuery_Regression1224(t *testing.T) {
	pendingAddr := common.HexToAddress("0x1000000000000000000000000000000000000001")
	bridgeAddr := common.HexToAddress("0x1000000000000000000000000000000000000002")

	newSet := func() *SetSyncSegment {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{
			ContractAddr: pendingAddr,
			BlockRange:   aggkitcommon.NewBlockRange(26414042, 26414124),
		})
		set.Add(SyncSegment{
			ContractAddr: bridgeAddr,
			BlockRange:   aggkitcommon.NewBlockRange(26414125, 26414125),
		})
		return &set
	}

	t.Run("initial query is exactly the pending range of the first address", func(t *testing.T) {
		set := newSet()
		query, err := set.NextQuery(1000, 0, false)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, "From: 26414042, To: 26414124 (83)", query.BlockRange.String())
		require.Equal(t, []common.Address{pendingAddr}, query.Addrs,
			"the initial query must only contain the address pending over the whole range")
	})

	t.Run("a finished address does not shadow the remaining pending address", func(t *testing.T) {
		set := newSet()
		query, err := set.NextQuery(1000, 0, false)
		require.NoError(t, err)
		require.NoError(t, set.SubtractLogQuery(query))

		next, err := set.NextQuery(1000, 0, false)
		require.NoError(t, err, "bridgeAddr must still be planned after pendingAddr is finished")
		require.NotNil(t, next)
		require.Equal(t, "From: 26414125, To: 26414125 (1)", next.BlockRange.String())
		require.Equal(t, []common.Address{bridgeAddr}, next.Addrs)
	})

	t.Run("loop covers the whole pending range exactly once", func(t *testing.T) {
		set := newSet()
		queries := collectPlannedQueries(t, set, 1000, 0, false)
		require.Len(t, queries, 2)
		requireQueriesCoverExactly(t, queries, aggkitcommon.NewBlockRange(26414042, 26414125))
		require.True(t, set.Finished())
	})
}

// TestSetSyncSegment_NextQuery_NoOverqueryPastPendingEnd covers the second half
// of the #1224 fixture: the first address is still pending past the point where
// the second one joins, so the planner must split the query at that boundary
// instead of asking the second address to re-download blocks it already has.
//
//	pendingAddr: [26414042, 26414125]
//	bridgeAddr:  [26414125, 26414125]
//
// Before the fix the first query was [26414042, 26414125] and included
// bridgeAddr, which already had [26414042, 26414124] synced.
func TestSetSyncSegment_NextQuery_NoOverqueryPastPendingEnd(t *testing.T) {
	pendingAddr := common.HexToAddress("0x1000000000000000000000000000000000000001")
	bridgeAddr := common.HexToAddress("0x1000000000000000000000000000000000000002")

	set := NewSetSyncSegment()
	set.Add(SyncSegment{
		ContractAddr: pendingAddr,
		BlockRange:   aggkitcommon.NewBlockRange(26414042, 26414125),
	})
	set.Add(SyncSegment{
		ContractAddr: bridgeAddr,
		BlockRange:   aggkitcommon.NewBlockRange(26414125, 26414125),
	})

	query, err := set.NextQuery(1000, 0, false)
	require.NoError(t, err)
	require.NotNil(t, query)
	require.Equal(t, "From: 26414042, To: 26414124 (83)", query.BlockRange.String())
	require.Equal(t, []common.Address{pendingAddr}, query.Addrs,
		"bridgeAddr is not pending before 26414125 and must not be queried there")

	require.NoError(t, set.SubtractLogQuery(query))
	next, err := set.NextQuery(1000, 0, false)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, "From: 26414125, To: 26414125 (1)", next.BlockRange.String())
	require.ElementsMatch(t, []common.Address{pendingAddr, bridgeAddr}, next.Addrs)
}

// TestSetSyncSegment_NextQuery_MixedEmptySegments checks that empty segments
// (fully synced addresses) never shadow the work of a non-empty segment,
// independently of their position in the set.
func TestSetSyncSegment_NextQuery_MixedEmptySegments(t *testing.T) {
	emptyAddr := common.HexToAddress("0x2000000000000000000000000000000000000001")
	otherEmptyAddr := common.HexToAddress("0x2000000000000000000000000000000000000002")
	pendingAddr := common.HexToAddress("0x2000000000000000000000000000000000000003")

	t.Run("empty segment inserted first", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{ContractAddr: emptyAddr, BlockRange: aggkitcommon.BlockRangeZero})
		set.Add(SyncSegment{
			ContractAddr: pendingAddr,
			BlockRange:   aggkitcommon.NewBlockRange(100, 110),
		})

		query, err := set.NextQuery(1000, 0, false)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, "From: 100, To: 110 (11)", query.BlockRange.String())
		require.Equal(t, []common.Address{pendingAddr}, query.Addrs)
	})

	t.Run("empty segment inserted last", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{
			ContractAddr: pendingAddr,
			BlockRange:   aggkitcommon.NewBlockRange(100, 110),
		})
		set.Add(SyncSegment{ContractAddr: emptyAddr, BlockRange: aggkitcommon.BlockRangeZero})

		query, err := set.NextQuery(1000, 0, false)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, "From: 100, To: 110 (11)", query.BlockRange.String())
		require.Equal(t, []common.Address{pendingAddr}, query.Addrs)
	})

	t.Run("several empty segments around the pending one", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{ContractAddr: emptyAddr, BlockRange: aggkitcommon.BlockRangeZero})
		set.Add(SyncSegment{
			ContractAddr: pendingAddr,
			BlockRange:   aggkitcommon.NewBlockRange(100, 110),
		})
		set.Add(SyncSegment{ContractAddr: otherEmptyAddr, BlockRange: aggkitcommon.BlockRangeZero})

		query, err := set.NextQuery(1000, 0, false)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, []common.Address{pendingAddr}, query.Addrs)
	})
}

// TestSetSyncSegment_NextQuery_AllEmptySegments checks that a set whose
// segments are all empty reports ErrFinished and never an empty query.
func TestSetSyncSegment_NextQuery_AllEmptySegments(t *testing.T) {
	t.Run("single empty segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{
			ContractAddr: common.HexToAddress("0x3000000000000000000000000000000000000001"),
			BlockRange:   aggkitcommon.BlockRangeZero,
		})

		query, err := set.NextQuery(1000, 0, false)
		require.ErrorIs(t, err, ErrFinished)
		require.Nil(t, query)
	})

	t.Run("several empty segments", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{
			ContractAddr: common.HexToAddress("0x3000000000000000000000000000000000000001"),
			BlockRange:   aggkitcommon.BlockRangeZero,
		})
		set.Add(SyncSegment{
			ContractAddr: common.HexToAddress("0x3000000000000000000000000000000000000002"),
			BlockRange:   aggkitcommon.BlockRangeZero,
		})

		query, err := set.NextQuery(1000, 0, false)
		require.ErrorIs(t, err, ErrFinished)
		require.Nil(t, query)
	})
}

// TestSetSyncSegment_NextQuery_StartEndBoundaries checks that the planner walks
// the exact start/end boundaries of staggered pending segments.
func TestSetSyncSegment_NextQuery_StartEndBoundaries(t *testing.T) {
	addrA := common.HexToAddress("0x4000000000000000000000000000000000000001")
	addrB := common.HexToAddress("0x4000000000000000000000000000000000000002")
	addrC := common.HexToAddress("0x4000000000000000000000000000000000000003")

	set := NewSetSyncSegment()
	set.Add(SyncSegment{ContractAddr: addrA, BlockRange: aggkitcommon.NewBlockRange(10, 50)})
	set.Add(SyncSegment{ContractAddr: addrB, BlockRange: aggkitcommon.NewBlockRange(20, 40)})
	set.Add(SyncSegment{ContractAddr: addrC, BlockRange: aggkitcommon.NewBlockRange(30, 60)})

	type expected struct {
		blockRange string
		addrs      []common.Address
	}
	expectedQueries := []expected{
		{"From: 10, To: 19 (10)", []common.Address{addrA}},
		{"From: 20, To: 29 (10)", []common.Address{addrA, addrB}},
		{"From: 30, To: 40 (11)", []common.Address{addrA, addrB, addrC}},
		{"From: 41, To: 50 (10)", []common.Address{addrA, addrC}},
		{"From: 51, To: 60 (10)", []common.Address{addrC}},
	}

	queries := collectPlannedQueries(t, &set, 1000, 0, false)
	require.Len(t, queries, len(expectedQueries))
	for i, want := range expectedQueries {
		require.Equal(t, want.blockRange, queries[i].BlockRange.String(), "query %d range", i)
		require.ElementsMatch(t, want.addrs, queries[i].Addrs, "query %d addresses", i)
	}
	requireQueriesCoverExactly(t, queries, aggkitcommon.NewBlockRange(10, 60))
}

// TestSetSyncSegment_NextQuery_ChunkAndMaxBounds covers the chunk-size and
// maxBlockNumber limits, including the overflow edge cases.
func TestSetSyncSegment_NextQuery_ChunkAndMaxBounds(t *testing.T) {
	addr := common.HexToAddress("0x5000000000000000000000000000000000000001")

	t.Run("chunk size limits the query", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{ContractAddr: addr, BlockRange: aggkitcommon.NewBlockRange(10, 1000)})

		query, err := set.NextQuery(10, 0, false)
		require.NoError(t, err)
		require.Equal(t, "From: 10, To: 19 (10)", query.BlockRange.String())
	})

	t.Run("chunk size zero plans a single block", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{ContractAddr: addr, BlockRange: aggkitcommon.NewBlockRange(10, 1000)})

		// Defensive: a zero chunk used to underflow the old arithmetic; the
		// planner must still make progress one block at a time instead of
		// stalling or wrapping.
		query, err := set.NextQuery(0, 0, false)
		require.NoError(t, err)
		require.Equal(t, "From: 10, To: 10 (1)", query.BlockRange.String())
	})

	t.Run("maxBlockNumber caps the query", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{ContractAddr: addr, BlockRange: aggkitcommon.NewBlockRange(10, 1000)})

		query, err := set.NextQuery(1000, 50, true)
		require.NoError(t, err)
		require.Equal(t, "From: 10, To: 50 (41)", query.BlockRange.String())
	})

	t.Run("maxBlockNumber below from returns ErrFinished", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{ContractAddr: addr, BlockRange: aggkitcommon.NewBlockRange(100, 200)})

		query, err := set.NextQuery(1000, 50, true)
		require.ErrorIs(t, err, ErrFinished)
		require.Nil(t, query)
	})

	t.Run("maxBlockNumber ignored when not applied", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{ContractAddr: addr, BlockRange: aggkitcommon.NewBlockRange(10, 1000)})

		query, err := set.NextQuery(10, 1, false)
		require.NoError(t, err)
		require.Equal(t, "From: 10, To: 19 (10)", query.BlockRange.String())
	})

	t.Run("chunk overflow saturates at MaxUint64", func(t *testing.T) {
		from := uint64(math.MaxUint64) - 2
		set := NewSetSyncSegment()
		set.Add(SyncSegment{ContractAddr: addr, BlockRange: aggkitcommon.NewBlockRange(from, math.MaxUint64)})

		query, err := set.NextQuery(100, 0, false)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, from, query.BlockRange.FromBlock)
		require.Equal(t, uint64(math.MaxUint64), query.BlockRange.ToBlock)
	})

	t.Run("from at MaxUint64 does not wrap", func(t *testing.T) {
		set := NewSetSyncSegment()
		set.Add(SyncSegment{ContractAddr: addr, BlockRange: aggkitcommon.NewBlockRange(math.MaxUint64, math.MaxUint64)})

		query, err := set.NextQuery(5, 0, false)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, uint64(math.MaxUint64), query.BlockRange.FromBlock)
		require.Equal(t, uint64(math.MaxUint64), query.BlockRange.ToBlock)
	})
}

// TestSetSyncSegment_NextQuery_LoopCoverageNoRepeats drives a set of
// overlapping and staggered segments through the full planner loop and checks
// that every block is planned exactly once and that the loop terminates.
func TestSetSyncSegment_NextQuery_LoopCoverageNoRepeats(t *testing.T) {
	addrA := common.HexToAddress("0x6000000000000000000000000000000000000001")
	addrB := common.HexToAddress("0x6000000000000000000000000000000000000002")
	addrC := common.HexToAddress("0x6000000000000000000000000000000000000003")
	addrD := common.HexToAddress("0x6000000000000000000000000000000000000004")

	set := NewSetSyncSegment()
	set.Add(SyncSegment{ContractAddr: addrA, BlockRange: aggkitcommon.NewBlockRange(10, 100)})
	set.Add(SyncSegment{ContractAddr: addrB, BlockRange: aggkitcommon.NewBlockRange(5, 80)})
	set.Add(SyncSegment{ContractAddr: addrC, BlockRange: aggkitcommon.NewBlockRange(90, 150)})
	set.Add(SyncSegment{ContractAddr: addrD, BlockRange: aggkitcommon.NewBlockRange(10, 10)})

	queries := collectPlannedQueries(t, &set, 17, 0, false)
	require.NotEmpty(t, queries)
	requireQueriesCoverExactly(t, queries, aggkitcommon.NewBlockRange(5, 150))
	require.True(t, set.Finished())

	_, err := set.NextQuery(17, 0, false)
	require.ErrorIs(t, err, ErrFinished)
}

func TestSetSyncSegment_SubtractLogQuery_EdgeCases(t *testing.T) {
	t.Run("error creating segment from log query", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 100),
		}
		set.Add(segment)

		// Log query with empty addresses should still work
		logQuery := &LogQuery{
			Addrs:      []common.Address{},
			BlockRange: aggkitcommon.NewBlockRange(10, 20),
		}

		err := set.SubtractLogQuery(logQuery)
		require.NoError(t, err)
	})
}

func TestSetSyncSegment_GetTotalPendingBlockRange_EdgeCases(t *testing.T) {
	t.Run("nil set returns nil", func(t *testing.T) {
		var set *SetSyncSegment
		result := set.GetTotalPendingBlockRange()
		require.Nil(t, result)
	})

	t.Run("set with single non-empty segment", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(10, 50),
		}
		set.Add(segment)

		result := set.GetTotalPendingBlockRange()
		require.NotNil(t, result)
		require.Equal(t, uint64(10), result.FromBlock)
		require.Equal(t, uint64(50), result.ToBlock)
	})

	t.Run("set with non-overlapping segments", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr1 := common.HexToAddress("0x111")
		addr2 := common.HexToAddress("0x222")

		segment1 := SyncSegment{
			ContractAddr: addr1,
			BlockRange:   aggkitcommon.NewBlockRange(10, 50),
		}
		segment2 := SyncSegment{
			ContractAddr: addr2,
			BlockRange:   aggkitcommon.NewBlockRange(100, 200),
		}
		set.Add(segment1)
		set.Add(segment2)

		result := set.GetTotalPendingBlockRange()
		require.NotNil(t, result)
		require.Equal(t, uint64(10), result.FromBlock)
		require.Equal(t, uint64(200), result.ToBlock)
	})
}

func TestSetSyncSegment_IsPartiallyAvailable_EdgeCases(t *testing.T) {
	t.Run("segment exactly matches query range", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")

		segment := SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(10, 50),
		}
		set.Add(segment)

		query := LogQuery{
			Addrs:      []common.Address{addr},
			BlockRange: aggkitcommon.NewBlockRange(10, 50),
		}

		available, result := set.IsPartiallyAvailable(query)
		require.True(t, available)
		require.NotNil(t, result)
		require.Equal(t, uint64(10), result.BlockRange.FromBlock)
		require.Equal(t, uint64(50), result.BlockRange.ToBlock)
	})
}
