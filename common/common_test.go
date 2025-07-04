package common

import (
	"encoding/hex"
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

func TestBytesToUint32(t *testing.T) {
	tests := []struct {
		name        string
		input       []byte
		expected    uint32
		expectPanic bool
	}{
		{
			name:     "Empty byte slice",
			input:    []byte{},
			expected: 0,
		},
		{
			name:     "Single byte",
			input:    []byte{0x01},
			expected: 1,
		},
		{
			name:     "Two bytes",
			input:    []byte{0x01, 0x02},
			expected: 258,
		},
		{
			name:     "Three bytes",
			input:    []byte{0x01, 0x02, 0x03},
			expected: 66051,
		},
		{
			name:     "Four bytes",
			input:    []byte{0x01, 0x02, 0x03, 0x04},
			expected: 16909060,
		},
		{
			name:        "More than four bytes",
			input:       []byte{0x01, 0x02, 0x03, 0x04, 0x05},
			expectPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectPanic {
				require.Panics(t, func() {
					_ = BytesToUint32(tt.input)
				})
			} else {
				result := BytesToUint32(tt.input)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestUint64ToBigEndianBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    uint64
		expected string
	}{
		{
			name:     "Zero",
			input:    0,
			expected: "0000000000000000",
		},
		{
			name:     "MaxUint64",
			input:    18446744073709551615,
			expected: "ffffffffffffffff",
		},
		{
			name:     "Arbitrary value",
			input:    1234567890,
			expected: "00000000499602d2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Uint64ToBigEndianBytes(tt.input)
			require.Equal(t, tt.expected, hex.EncodeToString(result))
		})
	}
}

func TestBytesToUint64(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expected      uint64
		panicExpected bool
	}{
		{
			name:     "Zero",
			input:    "0000000000000000",
			expected: 0,
		},
		{
			name:     "MaxUint64",
			input:    "ffffffffffffffff",
			expected: 18446744073709551615,
		},
		{
			name:     "Arbitrary value",
			input:    "00000000499602d2",
			expected: 1234567890,
		},
		{
			name:          "Value too long",
			input:         "499602d2499602d2499602d2",
			panicExpected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputBytes, err := hex.DecodeString(tt.input)
			require.NoError(t, err)

			if tt.panicExpected {
				require.Panics(t, func() {
					_ = BytesToUint64(inputBytes)
				})
			} else {
				result := BytesToUint64(inputBytes)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestUint32ToBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    uint32
		expected string
	}{
		{
			name:     "Zero",
			input:    0,
			expected: "00000000",
		},
		{
			name:     "MaxUint32",
			input:    4294967295,
			expected: "ffffffff",
		},
		{
			name:     "Arbitrary value",
			input:    1234567890,
			expected: "499602d2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Uint32ToBytes(tt.input)
			require.Equal(t, tt.expected, hex.EncodeToString(result))
		})
	}
}

func TestUint64ToLittleEndianBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    uint64
		expected []byte
	}{
		{
			name:     "Zero value",
			input:    0,
			expected: []byte{0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name:     "Small value",
			input:    1,
			expected: []byte{1, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name:     "Max uint64 value",
			input:    ^uint64(0),
			expected: []byte{255, 255, 255, 255, 255, 255, 255, 255},
		},
		{
			name:     "Arbitrary value",
			input:    123456789,
			expected: []byte{21, 205, 91, 7, 0, 0, 0, 0},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := Uint64ToLittleEndianBytes(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
