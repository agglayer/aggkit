package types

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/etherman/types/mocks"
	"github.com/agglayer/aggkit/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
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
func TestSetSyncSegment_UpdateTargetBlockToNumber(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var set *SetSyncSegment
		err := set.UpdateTargetBlockToNumber(t.Context(), nil)
		require.NoError(t, err)
	})

	t.Run("update target block", func(t *testing.T) {
		set := NewSetSyncSegment()
		finality := aggkittypes.LatestBlock
		segment := SyncSegment{
			ContractAddr:  common.HexToAddress("0x123"),
			BlockRange:    aggkitcommon.NewBlockRange(1, 10),
			TargetToBlock: finality,
		}
		set.Add(segment)
		mockBlockNotifierManager := mocks.NewBlockNotifierManager(t)

		mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, finality).Return(uint64(150), nil).Once()
		err := set.UpdateTargetBlockToNumber(t.Context(), mockBlockNotifierManager)
		require.NoError(t, err)
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

	t.Run("remove totally a  segment", func(t *testing.T) {
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
		_, exists := set.GetByContract(addr)
		require.False(t, exists)
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
func TestSetSyncSegment_AfterFullySync(t *testing.T) {
	set := NewSetSyncSegment()
	addr := common.HexToAddress("0x123124543423")
	segment := SyncSegment{
		ContractAddr:  addr,
		BlockRange:    aggkitcommon.NewBlockRange(1, 100),
		TargetToBlock: types.LatestBlock,
	}
	set.Add(segment)

	logQuery := &LogQuery{
		Addrs:      []common.Address{addr},
		BlockRange: aggkitcommon.NewBlockRange(1, 100),
	}

	err := set.SubtractLogQuery(logQuery)
	require.NoError(t, err)
	// The segment is empty so is not returned by GetByContract
	segment, exists := set.GetByContract(addr)
	require.True(t, exists)
	require.True(t, segment.IsEmpty())
	require.True(t, set.Finished())
	require.Equal(t, uint64(0), set.TotalBlocks())

	mockBlockManager := mocks.NewBlockNotifierManager(t)
	mockBlockManager.EXPECT().GetCurrentBlockNumber(mock.Anything, types.LatestBlock).Return(uint64(150), nil).Once()
	set.UpdateTargetBlockToNumber(t.Context(), mockBlockManager)
	require.Equal(t, uint64(50), set.TotalBlocks())
	segment, exists = set.GetByContract(addr)
	require.True(t, exists)
	require.Equal(t, "From: 101, To: 150 (50)", segment.BlockRange.String())
}
