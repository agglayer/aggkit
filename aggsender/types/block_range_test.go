package types

import (
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
