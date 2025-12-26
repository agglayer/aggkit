package abi

import (
	"math/big"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestBuildABIFields(t *testing.T) {
	type TestStruct struct {
		Field1 uint8          `abi:"field1"`
		Field2 uint32         `abi:"field2"`
		Field3 common.Address `abi:"field3"`
		Field4 *big.Int       `abi:"field4"`
		Field5 []byte         `abi:"field5"`
		Field6 string         // No tag, should be skipped
	}

	fields, err := BuildABIFields(TestStruct{})
	require.NoError(t, err)
	require.Len(t, fields, 5)

	expected := []abi.ArgumentMarshaling{
		{Name: "field1", Type: "uint8"},
		{Name: "field2", Type: "uint32"},
		{Name: "field3", Type: "address"},
		{Name: "field4", Type: "uint256"},
		{Name: "field5", Type: "bytes"},
	}

	require.Equal(t, expected, fields)
}

func TestBuildABIFields_ErrorCases(t *testing.T) {
	t.Run("non-struct type", func(t *testing.T) {
		_, err := BuildABIFields(42)
		require.Error(t, err)
		require.Contains(t, err.Error(), "expected struct type")
	})

	t.Run("unsupported field type", func(t *testing.T) {
		type BadStruct struct {
			InvalidField map[string]string `abi:"invalid"`
		}
		_, err := BuildABIFields(BadStruct{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported type")
	})
}

func TestBuildABIFields_WithPointer(t *testing.T) {
	type TestStruct struct {
		Field1 uint8  `abi:"field1"`
		Field2 uint32 `abi:"field2"`
	}

	// Test with pointer to struct
	fields, err := BuildABIFields(&TestStruct{})
	require.NoError(t, err)
	require.Len(t, fields, 2)

	expected := []abi.ArgumentMarshaling{
		{Name: "field1", Type: "uint8"},
		{Name: "field2", Type: "uint32"},
	}

	require.Equal(t, expected, fields)
}

func TestInferABIType(t *testing.T) {
	tests := []struct {
		name     string
		input    reflect.Type
		expected string
		wantErr  bool
	}{
		// Special types
		{
			name:     "common.Address",
			input:    reflect.TypeOf(common.Address{}),
			expected: "address",
			wantErr:  false,
		},
		{
			name:     "big.Int pointer",
			input:    reflect.TypeOf(&big.Int{}),
			expected: "uint256",
			wantErr:  false,
		},
		{
			name:     "big.Int value",
			input:    reflect.TypeOf(big.Int{}),
			expected: "uint256",
			wantErr:  false,
		},
		{
			name:     "common.Hash",
			input:    reflect.TypeOf(common.Hash{}),
			expected: "bytes32",
			wantErr:  false,
		},
		// Unsigned integers
		{
			name:     "uint8",
			input:    reflect.TypeOf(uint8(0)),
			expected: "uint8",
			wantErr:  false,
		},
		{
			name:     "uint16",
			input:    reflect.TypeOf(uint16(0)),
			expected: "uint16",
			wantErr:  false,
		},
		{
			name:     "uint32",
			input:    reflect.TypeOf(uint32(0)),
			expected: "uint32",
			wantErr:  false,
		},
		{
			name:     "uint64",
			input:    reflect.TypeOf(uint64(0)),
			expected: "uint64",
			wantErr:  false,
		},
		// Signed integers
		{
			name:     "int8",
			input:    reflect.TypeOf(int8(0)),
			expected: "int8",
			wantErr:  false,
		},
		{
			name:     "int16",
			input:    reflect.TypeOf(int16(0)),
			expected: "int16",
			wantErr:  false,
		},
		{
			name:     "int32",
			input:    reflect.TypeOf(int32(0)),
			expected: "int32",
			wantErr:  false,
		},
		{
			name:     "int64",
			input:    reflect.TypeOf(int64(0)),
			expected: "int64",
			wantErr:  false,
		},
		// Other basic types
		{
			name:     "bool",
			input:    reflect.TypeOf(true),
			expected: "bool",
			wantErr:  false,
		},
		{
			name:     "string",
			input:    reflect.TypeOf(""),
			expected: "string",
			wantErr:  false,
		},
		// Slice and array types
		{
			name:     "byte slice",
			input:    reflect.TypeOf([]byte{}),
			expected: "bytes",
			wantErr:  false,
		},
		{
			name:     "byte array",
			input:    reflect.TypeOf([32]byte{}),
			expected: "bytes32",
			wantErr:  false,
		},
		{
			name:     "byte array different size",
			input:    reflect.TypeOf([20]byte{}),
			expected: "bytes20",
			wantErr:  false,
		},
		// Error cases
		{
			name:     "unsupported slice type",
			input:    reflect.TypeOf([]int{}),
			expected: "",
			wantErr:  true,
		},
		{
			name:     "unsupported array type",
			input:    reflect.TypeOf([5]int{}),
			expected: "",
			wantErr:  true,
		},
		{
			name:     "unsupported type map",
			input:    reflect.TypeOf(map[string]string{}),
			expected: "",
			wantErr:  true,
		},
		{
			name:     "unsupported type struct",
			input:    reflect.TypeOf(struct{}{}),
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := inferABIType(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "unsupported")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}
