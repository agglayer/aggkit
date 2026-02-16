package types

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestNewListBlockHeadersEmpty(t *testing.T) {
	t.Run("creates empty list with pre-allocated capacity", func(t *testing.T) {
		size := 10
		list := NewListBlockHeadersEmpty(size)

		require.NotNil(t, list)
		require.Equal(t, 0, list.Len())
		require.Equal(t, size, cap(list))
	})

	t.Run("creates empty list with zero capacity", func(t *testing.T) {
		list := NewListBlockHeadersEmpty(0)

		require.NotNil(t, list)
		require.Equal(t, 0, list.Len())
	})
}

func TestNewListBlockHeaders(t *testing.T) {
	t.Run("creates list with specified size filled with nil", func(t *testing.T) {
		size := 5
		list := NewListBlockHeaders(size)

		require.NotNil(t, list)
		require.Equal(t, size, list.Len())
		for i := range size {
			require.Nil(t, list[i])
		}
	})

	t.Run("creates empty list when size is zero", func(t *testing.T) {
		list := NewListBlockHeaders(0)

		require.NotNil(t, list)
		require.Equal(t, 0, list.Len())
	})
}

func TestListBlockHeaders_Len(t *testing.T) {
	t.Run("returns correct length for empty list", func(t *testing.T) {
		list := ListBlockHeaders{}
		require.Equal(t, 0, list.Len())
	})

	t.Run("returns correct length for list with elements", func(t *testing.T) {
		list := ListBlockHeaders{
			NewBlockHeader(1, common.HexToHash("0x01"), 1000, nil),
			NewBlockHeader(2, common.HexToHash("0x02"), 2000, nil),
			NewBlockHeader(3, common.HexToHash("0x03"), 3000, nil),
		}
		require.Equal(t, 3, list.Len())
	})

	t.Run("returns correct length for list with nil elements", func(t *testing.T) {
		list := ListBlockHeaders{nil, nil, nil}
		require.Equal(t, 3, list.Len())
	})
}

func TestListBlockHeaders_ToMap(t *testing.T) {
	t.Run("converts empty list to empty map", func(t *testing.T) {
		list := ListBlockHeaders{}
		result := list.ToMap()

		require.NotNil(t, result)
		require.Equal(t, 0, len(result))
	})

	t.Run("converts list with headers to map", func(t *testing.T) {
		header1 := NewBlockHeader(1, common.HexToHash("0x01"), 1000, nil)
		header2 := NewBlockHeader(2, common.HexToHash("0x02"), 2000, nil)
		header3 := NewBlockHeader(5, common.HexToHash("0x05"), 5000, nil)

		list := ListBlockHeaders{header1, header2, header3}
		result := list.ToMap()

		require.Equal(t, 3, len(result))
		require.Equal(t, header1, result[1])
		require.Equal(t, header2, result[2])
		require.Equal(t, header3, result[5])
	})

	t.Run("skips nil headers when converting to map", func(t *testing.T) {
		header1 := NewBlockHeader(1, common.HexToHash("0x01"), 1000, nil)
		header3 := NewBlockHeader(3, common.HexToHash("0x03"), 3000, nil)

		list := ListBlockHeaders{header1, nil, header3, nil}
		result := list.ToMap()

		require.Equal(t, 2, len(result))
		require.Equal(t, header1, result[1])
		require.Equal(t, header3, result[3])
		_, exists := result[0]
		require.False(t, exists)
	})

	t.Run("handles list with only nil headers", func(t *testing.T) {
		list := ListBlockHeaders{nil, nil, nil}
		result := list.ToMap()

		require.NotNil(t, result)
		require.Equal(t, 0, len(result))
	})
}

func TestListBlockHeaders_BlockNumbers(t *testing.T) {
	t.Run("returns empty slice for empty list", func(t *testing.T) {
		list := ListBlockHeaders{}
		result := list.BlockNumbers()

		require.NotNil(t, result)
		require.Equal(t, 0, len(result))
	})

	t.Run("returns sorted block numbers", func(t *testing.T) {
		header1 := NewBlockHeader(5, common.HexToHash("0x05"), 5000, nil)
		header2 := NewBlockHeader(2, common.HexToHash("0x02"), 2000, nil)
		header3 := NewBlockHeader(8, common.HexToHash("0x08"), 8000, nil)
		header4 := NewBlockHeader(1, common.HexToHash("0x01"), 1000, nil)

		list := ListBlockHeaders{header1, header2, header3, header4}
		result := list.BlockNumbers()

		require.Equal(t, 4, len(result))
		require.Equal(t, []uint64{1, 2, 5, 8}, result)
	})

	t.Run("skips nil headers when extracting block numbers", func(t *testing.T) {
		header1 := NewBlockHeader(3, common.HexToHash("0x03"), 3000, nil)
		header2 := NewBlockHeader(1, common.HexToHash("0x01"), 1000, nil)

		list := ListBlockHeaders{nil, header1, nil, header2, nil}
		result := list.BlockNumbers()

		require.Equal(t, 2, len(result))
		require.Equal(t, []uint64{1, 3}, result)
	})

	t.Run("returns empty slice for list with only nil headers", func(t *testing.T) {
		list := ListBlockHeaders{nil, nil, nil}
		result := list.BlockNumbers()

		require.NotNil(t, result)
		require.Equal(t, 0, len(result))
	})

	t.Run("handles duplicate block numbers", func(t *testing.T) {
		header1 := NewBlockHeader(2, common.HexToHash("0x02"), 2000, nil)
		header2 := NewBlockHeader(2, common.HexToHash("0x02b"), 2001, nil)
		header3 := NewBlockHeader(1, common.HexToHash("0x01"), 1000, nil)

		list := ListBlockHeaders{header1, header2, header3}
		result := list.BlockNumbers()

		require.Equal(t, 3, len(result))
		require.Equal(t, []uint64{1, 2, 2}, result)
	})
}

func TestListBlockHeaders_BlockRange(t *testing.T) {
	t.Run("returns empty block range for empty list", func(t *testing.T) {
		list := ListBlockHeaders{}
		result := list.BlockRange()

		require.Equal(t, aggkitcommon.BlockRange{}, result)
	})

	t.Run("returns correct range for single header", func(t *testing.T) {
		header := NewBlockHeader(5, common.HexToHash("0x05"), 5000, nil)
		list := ListBlockHeaders{header}
		result := list.BlockRange()

		expected := aggkitcommon.NewBlockRange(5, 5)
		require.Equal(t, expected, result)
	})

	t.Run("returns correct range for multiple headers in order", func(t *testing.T) {
		header1 := NewBlockHeader(1, common.HexToHash("0x01"), 1000, nil)
		header2 := NewBlockHeader(2, common.HexToHash("0x02"), 2000, nil)
		header3 := NewBlockHeader(3, common.HexToHash("0x03"), 3000, nil)

		list := ListBlockHeaders{header1, header2, header3}
		result := list.BlockRange()

		expected := aggkitcommon.NewBlockRange(1, 3)
		require.Equal(t, expected, result)
	})

	t.Run("returns correct range for multiple headers out of order", func(t *testing.T) {
		header1 := NewBlockHeader(5, common.HexToHash("0x05"), 5000, nil)
		header2 := NewBlockHeader(2, common.HexToHash("0x02"), 2000, nil)
		header3 := NewBlockHeader(8, common.HexToHash("0x08"), 8000, nil)
		header4 := NewBlockHeader(1, common.HexToHash("0x01"), 1000, nil)

		list := ListBlockHeaders{header1, header2, header3, header4}
		result := list.BlockRange()

		expected := aggkitcommon.NewBlockRange(1, 8)
		require.Equal(t, expected, result)
	})

	t.Run("skips nil headers when calculating range", func(t *testing.T) {
		header1 := NewBlockHeader(3, common.HexToHash("0x03"), 3000, nil)
		header2 := NewBlockHeader(10, common.HexToHash("0x0a"), 10000, nil)

		list := ListBlockHeaders{nil, header1, nil, header2, nil}
		result := list.BlockRange()

		expected := aggkitcommon.NewBlockRange(3, 10)
		require.Equal(t, expected, result)
	})

	t.Run("returns empty range for list with only nil headers", func(t *testing.T) {
		list := ListBlockHeaders{nil, nil, nil}
		result := list.BlockRange()

		require.Equal(t, aggkitcommon.BlockRange{}, result)
	})

	t.Run("handles non-consecutive block numbers", func(t *testing.T) {
		header1 := NewBlockHeader(100, common.HexToHash("0x64"), 100000, nil)
		header2 := NewBlockHeader(500, common.HexToHash("0x01f4"), 500000, nil)
		header3 := NewBlockHeader(250, common.HexToHash("0xfa"), 250000, nil)

		list := ListBlockHeaders{header1, header2, header3}
		result := list.BlockRange()

		expected := aggkitcommon.NewBlockRange(100, 500)
		require.Equal(t, expected, result)
	})

	t.Run("handles duplicate block numbers", func(t *testing.T) {
		header1 := NewBlockHeader(5, common.HexToHash("0x05"), 5000, nil)
		header2 := NewBlockHeader(5, common.HexToHash("0x05b"), 5001, nil)
		header3 := NewBlockHeader(10, common.HexToHash("0x0a"), 10000, nil)

		list := ListBlockHeaders{header1, header2, header3}
		result := list.BlockRange()

		expected := aggkitcommon.NewBlockRange(5, 10)
		require.Equal(t, expected, result)
	})
}
