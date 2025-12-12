package common

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlockRange_Gap(t *testing.T) {
	tests := []struct {
		name     string
		a        BlockRange
		b        BlockRange
		expected BlockRange
	}{
		{
			name:     "a before b with gap",
			a:        NewBlockRange(1, 5),
			b:        NewBlockRange(10, 15),
			expected: NewBlockRange(6, 9),
		},
		{
			name:     "b before a with gap",
			a:        NewBlockRange(10, 15),
			b:        NewBlockRange(1, 5),
			expected: NewBlockRange(6, 9),
		},
		{
			name:     "a and b overlap",
			a:        NewBlockRange(5, 15),
			b:        NewBlockRange(10, 20),
			expected: NewBlockRange(0, 0),
		},
		{
			name:     "a and b touch at edge",
			a:        NewBlockRange(1, 5),
			b:        NewBlockRange(6, 10),
			expected: NewBlockRange(0, 0),
		},
		{
			name:     "b and a touch at edge",
			a:        NewBlockRange(6, 10),
			b:        NewBlockRange(1, 5),
			expected: NewBlockRange(0, 0),
		},
		{
			name:     "identical ranges",
			a:        NewBlockRange(5, 10),
			b:        NewBlockRange(5, 10),
			expected: NewBlockRange(0, 0),
		},
		{
			name:     "a after b with no overlap and gap of 1",
			a:        NewBlockRange(12, 15),
			b:        NewBlockRange(10, 10),
			expected: NewBlockRange(11, 11),
		},
		{
			name:     "a before b with no overlap and gap of 1",
			a:        NewBlockRange(10, 10),
			b:        NewBlockRange(12, 15),
			expected: NewBlockRange(11, 11),
		},
		{
			name:     "empty a",
			a:        NewBlockRange(0, 0),
			b:        NewBlockRange(10, 15),
			expected: NewBlockRange(1, 9),
		},
		{
			name:     "empty b",
			a:        NewBlockRange(10, 15),
			b:        NewBlockRange(0, 0),
			expected: NewBlockRange(1, 9),
		},
		{
			name:     "both empty",
			a:        NewBlockRange(0, 0),
			b:        NewBlockRange(0, 0),
			expected: NewBlockRange(0, 0),
		},
		{
			name:     "b before a with no gap",
			a:        NewBlockRange(5, 10),
			b:        NewBlockRange(1, 4),
			expected: NewBlockRange(0, 0),
		},
		{
			name:     "invalid a",
			a:        NewBlockRange(10, 5), // from > to
			b:        NewBlockRange(1, 15),
			expected: NewBlockRange(0, 0), // should return empty range
		},
		{
			name:     "invalid b",
			a:        NewBlockRange(1, 15),
			b:        NewBlockRange(10, 5), // from > to
			expected: NewBlockRange(0, 0),  // should return empty range
		},
		{
			name:     "start verification case",
			a:        NewBlockRange(1, 5),
			b:        NewBlockRange(10, 10),
			expected: NewBlockRange(6, 9),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Gap(tt.b)
			require.Equal(t, tt.expected, got, "Gap() for %s: expected %v, got %v", tt.name, tt.expected, got)
		})
	}
}

func TestBlockRange_IsEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		br       BlockRange
		expected bool
	}{
		{
			name:     "empty zero value",
			br:       NewBlockRange(0, 0),
			expected: true,
		},
		{
			name:     "from > to",
			br:       NewBlockRange(10, 5),
			expected: true,
		},
		{
			name:     "from == to",
			br:       NewBlockRange(7, 7),
			expected: false,
		},
		{
			name:     "from < to",
			br:       NewBlockRange(3, 8),
			expected: false,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.br.IsEmpty()
			require.Equal(t, tt.expected, got, "IsEmpty() for %s: expected %v, got %v", tt.name, tt.expected, got)
		})
	}
}
func TestBlockRange_Greater(t *testing.T) {
	tests := []struct {
		name     string
		a        BlockRange
		b        BlockRange
		expected bool
	}{
		{
			name:     "a strictly greater than b",
			a:        NewBlockRange(10, 20),
			b:        NewBlockRange(1, 9),
			expected: true,
		},
		{
			name:     "a overlaps b",
			a:        NewBlockRange(10, 20),
			b:        NewBlockRange(15, 25),
			expected: false,
		},
		{
			name:     "a adjacent to b",
			a:        NewBlockRange(11, 20),
			b:        NewBlockRange(1, 10),
			expected: true,
		},
		{
			name:     "a not greater than b",
			a:        NewBlockRange(1, 5),
			b:        NewBlockRange(6, 10),
			expected: false,
		},
		{
			name:     "identical ranges",
			a:        NewBlockRange(5, 10),
			b:        NewBlockRange(5, 10),
			expected: false,
		},
		{
			name:     "a starts after b ends by 1",
			a:        NewBlockRange(11, 15),
			b:        NewBlockRange(1, 10),
			expected: true,
		},
		{
			name:     "a starts at b end",
			a:        NewBlockRange(10, 15),
			b:        NewBlockRange(1, 10),
			expected: false,
		},
		{
			name:     "empty a, non-empty b",
			a:        NewBlockRange(0, 0),
			b:        NewBlockRange(1, 10),
			expected: false,
		},
		{
			name:     "non-empty a, empty b",
			a:        NewBlockRange(5, 10),
			b:        NewBlockRange(0, 0),
			expected: true,
		},
		{
			name:     "both empty",
			a:        NewBlockRange(0, 0),
			b:        NewBlockRange(0, 0),
			expected: false,
		},
		{
			name:     "invalid a (from > to)",
			a:        NewBlockRange(10, 5),
			b:        NewBlockRange(1, 4),
			expected: true,
		},
		{
			name:     "invalid b (from > to)",
			a:        NewBlockRange(5, 10),
			b:        NewBlockRange(10, 5),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Greater(tt.b)
			require.Equal(t, tt.expected, got, "Greater() for %s: expected %v, got %v", tt.name, tt.expected, got)
		})
	}
}

func TestBlockRange_Contains(t *testing.T) {
	tests := []struct {
		name     string
		a        BlockRange
		b        BlockRange
		expected bool
	}{
		{
			name:     "no contained",
			a:        NewBlockRange(10, 20),
			b:        NewBlockRange(1, 9),
			expected: false,
		},
		{
			name:     "a overlaps b but not contained",
			a:        NewBlockRange(10, 20),
			b:        NewBlockRange(15, 25),
			expected: false,
		},
		{
			name:     "adjacent but not contained",
			a:        NewBlockRange(1, 5),
			b:        NewBlockRange(6, 10),
			expected: false,
		},
		{
			name:     "identical ranges",
			a:        NewBlockRange(5, 10),
			b:        NewBlockRange(5, 10),
			expected: true,
		},
		{
			name:     "contained =toBLock",
			a:        NewBlockRange(10, 15),
			b:        NewBlockRange(11, 15),
			expected: true,
		},
		{
			name:     "contained =fromBLock",
			a:        NewBlockRange(10, 15),
			b:        NewBlockRange(10, 14),
			expected: true,
		},
		{
			name:     "empty a, non-empty b",
			a:        NewBlockRange(0, 0),
			b:        NewBlockRange(1, 10),
			expected: false,
		},
		{
			name:     "non-empty a, empty b",
			a:        NewBlockRange(5, 10),
			b:        NewBlockRange(0, 0),
			expected: false,
		},
		{
			name:     "both empty",
			a:        NewBlockRange(0, 0),
			b:        NewBlockRange(0, 0),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Contains(tt.b)
			require.Equal(t, tt.expected, got, "Contains() for %s: expected %v, got %v", tt.name, tt.expected, got)
		})
	}
}

func TestBlockRange_Subtract(t *testing.T) {
	bn := NewBlockRange(10, 50)
	require.Equal(t, []BlockRange{NewBlockRange(10, 19), NewBlockRange(31, 50)}, bn.Subtract(NewBlockRange(20, 30)))
	require.Equal(t, []BlockRange{NewBlockRange(31, 50)}, bn.Subtract(NewBlockRange(1, 30)))
	require.Equal(t, []BlockRange{NewBlockRange(10, 29)}, bn.Subtract(NewBlockRange(30, 50)))
	require.Equal(t, []BlockRange{bn}, bn.Subtract(NewBlockRange(300, 500)))
	require.Equal(t, []BlockRange{}, bn.Subtract(NewBlockRange(1, 500)))
	require.Equal(t, []BlockRange{bn}, bn.Subtract(NewBlockRange(0, 0)))
}
func TestBlockRange_Intersect(t *testing.T) {
	bn := NewBlockRange(10, 50)
	require.Equal(t, BlockRange{10, 15}, bn.Intersect(NewBlockRange(5, 15)))
	require.Equal(t, BlockRange{30, 40}, bn.Intersect(NewBlockRange(30, 40)))
	require.Equal(t, BlockRangeZero, bn.Intersect(NewBlockRange(51, 60)))
}

func TestBlockRange_Cap(t *testing.T) {
	bn := NewBlockRange(10, 50)
	require.Equal(t, BlockRange{10, 40}, bn.Cap(40))
	require.Equal(t, BlockRange{10, 50}, bn.Cap(60))
	require.Equal(t, BlockRangeZero, bn.Cap(5))
}

func TestBlockRange_Merge(t *testing.T) {
	bn1 := NewBlockRange(10, 50)
	bn2 := NewBlockRange(1, 30)
	bn3 := NewBlockRange(1000, 1050)
	require.Equal(t, []BlockRange{bn1}, bn1.Merge(bn1))
	require.Equal(t, []BlockRange{NewBlockRange(1, 50)}, bn1.Merge(bn2))
	require.Equal(t, []BlockRange{NewBlockRange(1, 50)}, bn2.Merge(bn1))
	require.Equal(t, []BlockRange{bn1, bn3}, bn1.Merge(bn3))
	require.Equal(t, []BlockRange{bn1, bn3}, bn3.Merge(bn1))
}

func TestChunkedRangeQuery_IntSlice(t *testing.T) {
	ctx := context.Background()
	fromBlock := uint64(1)
	toBlock := uint64(10)
	maxRange := uint64(3)

	// Simulate fetchChunk: returns a slice of ints representing block numbers
	fetchChunk := func(ctx context.Context, from, to uint64) ([]int, error) {
		result := make([]int, 0, to-from+1)
		for i := from; i <= to; i++ {
			result = append(result, int(i))
		}
		return result, nil
	}

	// Combine: append slices
	combine := func(all, chunk []int) []int {
		return append(all, chunk...)
	}

	empty := []int{}

	result, err := ChunkedRangeQuery(ctx, fromBlock, toBlock, maxRange, fetchChunk, combine, empty)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, result)
}

func TestChunkedRangeQuery_ErrorPropagation(t *testing.T) {
	ctx := context.Background()
	fromBlock := uint64(1)
	toBlock := uint64(5)
	maxRange := uint64(2)

	fetchChunk := func(ctx context.Context, from, to uint64) ([]int, error) {
		if from == 3 {
			return nil, fmt.Errorf("simulated error")
		}
		result := make([]int, 0, to-from+1)
		for i := from; i <= to; i++ {
			result = append(result, int(i))
		}
		return result, nil
	}

	combine := func(all, chunk []int) []int {
		return append(all, chunk...)
	}

	empty := []int{}

	result, err := ChunkedRangeQuery(ctx, fromBlock, toBlock, maxRange, fetchChunk, combine, empty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "simulated error")
	require.Equal(t, empty, result)
}

func TestChunkedRangeQuery_ZeroMaxRange(t *testing.T) {
	ctx := context.Background()
	fromBlock := uint64(1)
	toBlock := uint64(5)
	maxRange := uint64(0)

	fetchChunk := func(ctx context.Context, from, to uint64) ([]int, error) {
		return []int{}, nil
	}

	combine := func(all, chunk []int) []int {
		return append(all, chunk...)
	}

	empty := []int{}

	result, err := ChunkedRangeQuery(ctx, fromBlock, toBlock, maxRange, fetchChunk, combine, empty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "maxRange must be greater than 0")
	require.Equal(t, empty, result)
}

func TestChunkedRangeQuery_SingleBlock(t *testing.T) {
	ctx := context.Background()
	fromBlock := uint64(7)
	toBlock := uint64(7)
	maxRange := uint64(10)

	fetchChunk := func(ctx context.Context, from, to uint64) ([]int, error) {
		require.Equal(t, uint64(7), from)
		require.Equal(t, uint64(7), to)
		return []int{int(from)}, nil
	}

	combine := func(all, chunk []int) []int {
		return append(all, chunk...)
	}

	empty := []int{}

	result, err := ChunkedRangeQuery(ctx, fromBlock, toBlock, maxRange, fetchChunk, combine, empty)
	require.NoError(t, err)
	require.Equal(t, []int{7}, result)
}

func TestChunkedRangeQuery_EmptyRange(t *testing.T) {
	ctx := context.Background()
	fromBlock := uint64(10)
	toBlock := uint64(9)
	maxRange := uint64(5)

	fetchChunk := func(ctx context.Context, from, to uint64) ([]int, error) {
		return []int{}, nil
	}

	combine := func(all, chunk []int) []int {
		return append(all, chunk...)
	}

	empty := []int{}

	result, err := ChunkedRangeQuery(ctx, fromBlock, toBlock, maxRange, fetchChunk, combine, empty)
	require.NoError(t, err)
	require.Equal(t, empty, result)
}
