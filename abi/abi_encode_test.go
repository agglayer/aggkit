package abi

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestEncodeABIStructArray(t *testing.T) {
	type TestStruct struct {
		Field1 uint8          `abi:"field1"`
		Field2 uint32         `abi:"field2"`
		Field3 common.Address `abi:"field3"`
	}

	items := []TestStruct{
		{
			Field1: 1,
			Field2: 100,
			Field3: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		},
		{
			Field1: 2,
			Field2: 200,
			Field3: common.HexToAddress("0x2222222222222222222222222222222222222222"),
		},
	}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)
	require.NotEmpty(t, encodedBytes)

	_, err = DecodeABIEncodedStructArray[TestStruct](encodedBytes)
	require.NoError(t, err)
}

func TestEncodeABIStructArray_EmptySlice(t *testing.T) {
	type TestStruct struct {
		Field1 uint8  `abi:"field1"`
		Field2 uint32 `abi:"field2"`
	}

	items := []TestStruct{}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)
	require.NotEmpty(t, encodedBytes)
}

func TestEncodeABIStructArray_WithBigInt(t *testing.T) {
	type TestStruct struct {
		Amount *big.Int `abi:"amount"`
	}

	items := []TestStruct{
		{Amount: big.NewInt(1000)},
		{Amount: big.NewInt(2000)},
	}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)
	require.NotEmpty(t, encodedBytes)
}

func TestEncodeABIStructArray_NoTags(t *testing.T) {
	type BadStruct struct {
		Field1 uint8
		Field2 uint32
	}

	items := []BadStruct{
		{Field1: 1, Field2: 100},
	}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err) // Should work with empty fields (encodes empty array)
	require.NotEmpty(t, encodedBytes)
}
