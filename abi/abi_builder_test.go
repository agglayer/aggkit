package abi

import (
	"math/big"
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
