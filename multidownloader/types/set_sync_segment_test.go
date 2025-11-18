package types

import (
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

func TestSetSyncSegment_Segments(t *testing.T) {
	set := NewSetSyncSegment()
	segment := SyncSegment{
		ContractAddr: common.HexToAddress("0x123"),
		BlockRange:   aggkitcommon.NewBlockRange(1, 10),
	}
	set.segments = []*SyncSegment{&segment}

	result := set.Segments()
	require.Len(t, result, 1)
	require.Equal(t, segment, result[0])
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
		res := set.GetByContract(addr)
		require.NotNil(t, res)
		require.Equal(t, uint64(1), res.BlockRange.FromBlock)
		require.Equal(t, uint64(15), res.BlockRange.ToBlock)
	})
}

func TestSetSyncSegment_GetByContract(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var set *SetSyncSegment
		result := set.GetByContract(common.HexToAddress("0x123"))
		require.Nil(t, result)
	})

	t.Run("segment found", func(t *testing.T) {
		set := NewSetSyncSegment()
		addr := common.HexToAddress("0x123")
		segment := NewSyncSegment(addr, aggkitcommon.NewBlockRange(1, 10),
			aggkittypes.LatestBlock, true)
		set.Add(segment)
		result := set.GetByContract(addr)
		require.NotNil(t, result)
	})

	t.Run("segment not found", func(t *testing.T) {
		set := NewSetSyncSegment()
		result := set.GetByContract(common.HexToAddress("0x123"))
		require.Nil(t, result)
	})
}

func TestSetSyncSegment_Subtract(t *testing.T) {
	t.Run("nil segments", func(t *testing.T) {
		set := NewSetSyncSegment()
		result := set.Subtract(nil)
		require.Equal(t, &set, result)
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

		result := set1.Subtract(&set2)
		require.NotNil(t, result)
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

		set.segments = []*SyncSegment{segment1, segment2}
		result := set.TotalBlocks()
		require.Greater(t, result, uint64(0))
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

func TestSetSyncSegment_NextQuery(t *testing.T) {
	t.Run("nil or empty segments", func(t *testing.T) {
		var set *SetSyncSegment
		query, err := set.NextQuery(100, 0)
		require.Nil(t, query)
		require.Equal(t, ErrFinished, err)

		emptySet := NewSetSyncSegment()
		query, err = emptySet.NextQuery(100, 0)
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
}

func TestSetSyncSegment_UpdateSyncingAfterDoingQuery(t *testing.T) {
	t.Run("nil set or query", func(t *testing.T) {
		var set *SetSyncSegment
		result := set.UpdateSyncingAfterDoingQuery(nil)
		require.Nil(t, result)

		validSet := NewSetSyncSegment()
		result = validSet.UpdateSyncingAfterDoingQuery(nil)
		require.Equal(t, &validSet, result)
	})
}
