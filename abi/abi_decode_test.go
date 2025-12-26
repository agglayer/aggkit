package abi

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestDecodeABIEncodedStructArray(t *testing.T) {
	type TestStruct struct {
		Field1 uint8          `abi:"field1"`
		Field2 uint32         `abi:"field2"`
		Field3 common.Address `abi:"field3"`
	}

	// Create test data
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

	// Encode first
	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)
	require.NotEmpty(t, encodedBytes)

	decoded, err := DecodeABIEncodedStructArray[TestStruct](encodedBytes)
	require.NoError(t, err)
	require.Len(t, decoded, 2)
}

func TestDecodeABIEncodedStructArray_EmptyBytes(t *testing.T) {
	type TestStruct struct {
		Field1 uint8 `abi:"field1"`
	}

	_, err := DecodeABIEncodedStructArray[TestStruct]([]byte{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "encoded bytes are empty")
}

func TestDecodeABIEncodedStructArray_WithBigInt(t *testing.T) {
	type TestStruct struct {
		Amount *big.Int `abi:"amount"`
		Value  uint32   `abi:"value"`
	}

	// Create test data
	items := []TestStruct{
		{Amount: big.NewInt(1000), Value: 1},
		{Amount: big.NewInt(2000), Value: 2},
		{Amount: big.NewInt(3000), Value: 3},
	}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)

	decoded, err := DecodeABIEncodedStructArray[TestStruct](encodedBytes)
	require.NoError(t, err)
	require.Len(t, decoded, 3)
}

func TestDecodeABIEncodedStructArray_EmptyArray(t *testing.T) {
	type TestStruct struct {
		Field1 uint8 `abi:"field1"`
	}

	// Encode empty array
	items := []TestStruct{}
	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)

	decoded, err := DecodeABIEncodedStructArray[TestStruct](encodedBytes)
	require.NoError(t, err)
	require.Len(t, decoded, 0)
}

func TestDecodeABIEncodedStructArray_InvalidABIData(t *testing.T) {
	type TestStruct struct {
		Field1 uint8 `abi:"field1"`
	}

	// Invalid ABI encoded data
	invalidData := []byte{0x01, 0x02, 0x03}

	_, err := DecodeABIEncodedStructArray[TestStruct](invalidData)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unpack data")
}

func TestDecodeABIEncodedStructArray_ComplexStruct(t *testing.T) {
	type ComplexStruct struct {
		LeafType           uint8          `abi:"leafType"`
		OriginNetwork      uint32         `abi:"originNetwork"`
		OriginAddress      common.Address `abi:"originAddress"`
		DestinationNetwork uint32         `abi:"destinationNetwork"`
		DestinationAddress common.Address `abi:"destinationAddress"`
		Amount             *big.Int       `abi:"amount"`
		Metadata           []byte         `abi:"metadata"`
	}

	// Create test data
	items := []ComplexStruct{
		{
			LeafType:           1,
			OriginNetwork:      1,
			OriginAddress:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
			DestinationNetwork: 2,
			DestinationAddress: common.HexToAddress("0x2222222222222222222222222222222222222222"),
			Amount:             big.NewInt(1000),
			Metadata:           []byte("test1"),
		},
		{
			LeafType:           2,
			OriginNetwork:      3,
			OriginAddress:      common.HexToAddress("0x3333333333333333333333333333333333333333"),
			DestinationNetwork: 4,
			DestinationAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
			Amount:             big.NewInt(2000),
			Metadata:           []byte("test2"),
		},
	}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)

	decoded, err := DecodeABIEncodedStructArray[ComplexStruct](encodedBytes)
	require.NoError(t, err)
	require.Len(t, decoded, 2)
}

func TestDecodeABIEncodedStructArray_NoABITags(t *testing.T) {
	type BadStruct struct {
		Field1 uint8
		Field2 uint32
	}

	// Try to decode with a struct that has no abi tags
	// BuildABIFields will succeed but return empty fields, which will cause unpack to fail
	_, err := DecodeABIEncodedStructArray[BadStruct]([]byte{0x01})
	require.Error(t, err)
	// The error will be from unpacking due to insufficient data or empty ABI fields
	require.Contains(t, err.Error(), "failed to")
}

func TestDecodeABIEncodedStructArray_SingleItem(t *testing.T) {
	type TestStruct struct {
		Value uint64 `abi:"value"`
	}

	// Create single item array
	items := []TestStruct{
		{Value: 12345},
	}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)

	decoded, err := DecodeABIEncodedStructArray[TestStruct](encodedBytes)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	require.Equal(t, uint64(12345), decoded[0].Value)
}
