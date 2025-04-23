package common

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestAsLittleEndianSlice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    *big.Int
		expected []byte
	}{
		{
			name:     "Zero value",
			input:    big.NewInt(0),
			expected: make([]byte, 32),
		},
		{
			name:     "Positive value",
			input:    big.NewInt(123456789),
			expected: append([]byte{21, 205, 91, 7}, make([]byte, 28)...),
		},
		{
			name:     "Negative value",
			input:    big.NewInt(-123456789),
			expected: append([]byte{21, 205, 91, 7}, make([]byte, 28)...),
		},
		{
			name: "Large positive value",
			input: new(big.Int).SetBytes([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}),
			expected: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := BigIntToLittleEndianBytes(tt.input)
			require.Len(t, result, common.HashLength)

			for i := range result {
				require.Equal(t, tt.expected[i], result[i],
					fmt.Sprintf("expected byte at index %d to be %x, got %x", i, tt.expected[i], result[i]))
			}
		})
	}
}

func TestEstimateSliceCapacity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		total    int
		span     uint64
		fullSpan uint64
		expected int
	}{
		{
			name:     "Zero fullSpan",
			total:    100,
			span:     50,
			fullSpan: 0,
			expected: 0,
		},
		{
			name:     "Zero total",
			total:    0,
			span:     50,
			fullSpan: 100,
			expected: 0,
		},
		{
			name:     "Zero span",
			total:    100,
			span:     0,
			fullSpan: 100,
			expected: 0,
		},
		{
			name:     "Normal case",
			total:    100,
			span:     50,
			fullSpan: 100,
			expected: 50,
		},
		{
			name:     "Span equals fullSpan",
			total:    100,
			span:     100,
			fullSpan: 100,
			expected: 100,
		},
		{
			name:     "Span greater than fullSpan",
			total:    100,
			span:     150,
			fullSpan: 100,
			expected: 150,
		},
		{
			name:     "Large values",
			total:    1_000_000,
			span:     500_000,
			fullSpan: 1_000_000,
			expected: 500_000,
		},
		{
			name:     "random values test",
			total:    57,
			span:     198,
			fullSpan: 213,
			expected: 52,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := EstimateSliceCapacity(tt.total, tt.span, tt.fullSpan)
			require.Equal(t, tt.expected, result)
		})
	}
}
