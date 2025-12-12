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
		Field1 uint8          `abiarg:"field1"`
		Field2 uint32         `abiarg:"field2"`
		Field3 common.Address `abiarg:"field3"`
		Field4 *big.Int       `abiarg:"field4,uint256"`
		Field5 []byte         `abiarg:"field5"`
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

func TestBuildABIFields_TypeInference(t *testing.T) {
	type TestStruct struct {
		Uint8Field    uint8          `abiarg:"uint8Field"`
		Uint16Field   uint16         `abiarg:"uint16Field"`
		Uint32Field   uint32         `abiarg:"uint32Field"`
		Uint64Field   uint64         `abiarg:"uint64Field"`
		BoolField     bool           `abiarg:"boolField"`
		StringField   string         `abiarg:"stringField"`
		BytesField    []byte         `abiarg:"bytesField"`
		AddressField  common.Address `abiarg:"addressField"`
		HashField     common.Hash    `abiarg:"hashField"`
		BigIntField   *big.Int       `abiarg:"bigIntField"` // Inferred as uint256
		BigIntExplict *big.Int       `abiarg:"bigIntExplict,uint128"`
	}

	fields, err := BuildABIFields(TestStruct{})
	require.NoError(t, err)
	require.Len(t, fields, 11)

	expected := []abi.ArgumentMarshaling{
		{Name: "uint8Field", Type: "uint8"},
		{Name: "uint16Field", Type: "uint16"},
		{Name: "uint32Field", Type: "uint32"},
		{Name: "uint64Field", Type: "uint64"},
		{Name: "boolField", Type: "bool"},
		{Name: "stringField", Type: "string"},
		{Name: "bytesField", Type: "bytes"},
		{Name: "addressField", Type: "address"},
		{Name: "hashField", Type: "bytes32"},
		{Name: "bigIntField", Type: "uint256"},
		{Name: "bigIntExplict", Type: "uint128"},
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
			InvalidField map[string]string `abiarg:"invalid"`
		}
		_, err := BuildABIFields(BadStruct{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported type")
	})
}

func TestBuildABIFields_WithPointer(t *testing.T) {
	type TestStruct struct {
		Field1 uint8  `abiarg:"field1"`
		Field2 uint32 `abiarg:"field2"`
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
