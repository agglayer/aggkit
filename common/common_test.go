package common

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/log"
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

func TestUint64ToBytes(t *testing.T) {
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
			result := Uint64ToBytes(tt.input)
			require.Equal(t, tt.expected, hex.EncodeToString(result))
		})
	}
}

func TestBytesToUint64(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected uint64
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputBytes, _ := hex.DecodeString(tt.input)
			result := BytesToUint64(inputBytes)
			require.Equal(t, tt.expected, result)
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

func TestCalculateAccInputHash(t *testing.T) {
	logger := log.WithFields("module", "common_test")

	tests := []struct {
		name              string
		oldAccInputHash   common.Hash
		batchData         []byte
		l1InfoRoot        common.Hash
		timestampLimit    uint64
		sequencerAddr     common.Address
		forcedBlockhashL1 common.Hash
		expectedHash      common.Hash
	}{
		{
			name:              "Test case 1",
			oldAccInputHash:   common.HexToHash("0x1"),
			batchData:         []byte("batch data"),
			l1InfoRoot:        common.HexToHash("0x2"),
			timestampLimit:    1234567890,
			sequencerAddr:     common.HexToAddress("0x3"),
			forcedBlockhashL1: common.HexToHash("0x4"),
			expectedHash:      common.HexToHash("0xc52fa6fdbba28ac34f02f3ca08c35b719239033028e49e28ba996a5fc75096e9"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateAccInputHash(
				logger,
				tt.oldAccInputHash,
				tt.batchData,
				tt.l1InfoRoot,
				tt.timestampLimit,
				tt.sequencerAddr,
				tt.forcedBlockhashL1,
			)
			require.Equal(t, tt.expectedHash, result)
		})
	}
}
