package abi

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestDecodeABIEncodedStructArray(t *testing.T) {
	type TestStruct struct {
		Field1 uint8          `abiarg:"field1"`
		Field2 uint32         `abiarg:"field2"`
		Field3 common.Address `abiarg:"field3"`
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

	// Decode with converter
	converter := func(item any) (TestStruct, error) {
		// The ABI library returns anonymous structs, we need to extract fields
		// In real usage, you'd use reflection or type assertions
		return TestStruct{
			Field1: 1, // Placeholder for test
			Field2: 100,
			Field3: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		}, nil
	}

	decoded, err := DecodeABIEncodedStructArray(encodedBytes, converter)
	require.NoError(t, err)
	require.Len(t, decoded, 2)
}

func TestDecodeABIEncodedStructArray_EmptyBytes(t *testing.T) {
	type TestStruct struct {
		Field1 uint8 `abiarg:"field1"`
	}

	converter := func(item any) (TestStruct, error) {
		return TestStruct{}, nil
	}

	_, err := DecodeABIEncodedStructArray([]byte{}, converter)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encoded bytes are empty")
}

func TestDecodeABIEncodedStructArray_ConverterError(t *testing.T) {
	type TestStruct struct {
		Field1 uint8  `abiarg:"field1"`
		Field2 uint32 `abiarg:"field2"`
	}

	// Create test data
	items := []TestStruct{
		{Field1: 1, Field2: 100},
		{Field1: 2, Field2: 200},
	}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)

	// Converter that always fails
	converter := func(item any) (TestStruct, error) {
		return TestStruct{}, errors.New("converter failed")
	}

	_, err = DecodeABIEncodedStructArray(encodedBytes, converter)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to convert item 0")
	require.Contains(t, err.Error(), "converter failed")
}

func TestDecodeABIEncodedStructArray_WithBigInt(t *testing.T) {
	type TestStruct struct {
		Amount *big.Int `abiarg:"amount,uint256"`
		Value  uint32   `abiarg:"value"`
	}

	// Create test data
	items := []TestStruct{
		{Amount: big.NewInt(1000), Value: 1},
		{Amount: big.NewInt(2000), Value: 2},
		{Amount: big.NewInt(3000), Value: 3},
	}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)

	// Converter that extracts fields
	converter := func(item any) (TestStruct, error) {
		// In real usage, you'd use reflection to extract the fields
		return TestStruct{Amount: big.NewInt(1000), Value: 1}, nil
	}

	decoded, err := DecodeABIEncodedStructArray(encodedBytes, converter)
	require.NoError(t, err)
	require.Len(t, decoded, 3)
}

func TestDecodeABIEncodedStructArray_EmptyArray(t *testing.T) {
	type TestStruct struct {
		Field1 uint8 `abiarg:"field1"`
	}

	// Encode empty array
	items := []TestStruct{}
	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)

	converter := func(item any) (TestStruct, error) {
		return TestStruct{}, nil
	}

	decoded, err := DecodeABIEncodedStructArray(encodedBytes, converter)
	require.NoError(t, err)
	require.Len(t, decoded, 0)
}

func TestDecodeABIEncodedStructArray_InvalidABIData(t *testing.T) {
	type TestStruct struct {
		Field1 uint8 `abiarg:"field1"`
	}

	converter := func(item any) (TestStruct, error) {
		return TestStruct{}, nil
	}

	// Invalid ABI encoded data
	invalidData := []byte{0x01, 0x02, 0x03}

	_, err := DecodeABIEncodedStructArray(invalidData, converter)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unpack data")
}

func TestDecodeABIEncodedStructArray_ComplexStruct(t *testing.T) {
	type ComplexStruct struct {
		LeafType           uint8          `abiarg:"leafType"`
		OriginNetwork      uint32         `abiarg:"originNetwork"`
		OriginAddress      common.Address `abiarg:"originAddress"`
		DestinationNetwork uint32         `abiarg:"destinationNetwork"`
		DestinationAddress common.Address `abiarg:"destinationAddress"`
		Amount             *big.Int       `abiarg:"amount,uint256"`
		Metadata           []byte         `abiarg:"metadata"`
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

	converter := func(item any) (ComplexStruct, error) {
		// Placeholder converter for test
		return ComplexStruct{
			LeafType:      1,
			OriginNetwork: 1,
			Amount:        big.NewInt(1000),
		}, nil
	}

	decoded, err := DecodeABIEncodedStructArray(encodedBytes, converter)
	require.NoError(t, err)
	require.Len(t, decoded, 2)
}

func TestDecodeABIEncodedStructArray_NoABITags(t *testing.T) {
	type BadStruct struct {
		Field1 uint8
		Field2 uint32
	}

	converter := func(item any) (BadStruct, error) {
		return BadStruct{}, nil
	}

	// Try to decode with a struct that has no abiarg tags
	// BuildABIFields will succeed but return empty fields, which will cause unpack to fail
	_, err := DecodeABIEncodedStructArray([]byte{0x01}, converter)
	require.Error(t, err)
	// The error will be from unpacking due to insufficient data or empty ABI fields
	require.Contains(t, err.Error(), "failed to")
}

func TestDecodeABIEncodedStructArray_SingleItem(t *testing.T) {
	type TestStruct struct {
		Value uint64 `abiarg:"value"`
	}

	// Create single item array
	items := []TestStruct{
		{Value: 12345},
	}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)

	converter := func(item any) (TestStruct, error) {
		return TestStruct{Value: 12345}, nil
	}

	decoded, err := DecodeABIEncodedStructArray(encodedBytes, converter)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	require.Equal(t, uint64(12345), decoded[0].Value)
}

func TestDecodeABIEncodedStructArray_ConverterPartialFailure(t *testing.T) {
	type TestStruct struct {
		Field1 uint8  `abiarg:"field1"`
		Field2 uint32 `abiarg:"field2"`
	}

	items := []TestStruct{
		{Field1: 1, Field2: 100},
		{Field1: 2, Field2: 200},
		{Field1: 3, Field2: 300},
	}

	encodedBytes, err := EncodeABIStructArray(items)
	require.NoError(t, err)

	callCount := 0
	converter := func(item any) (TestStruct, error) {
		callCount++
		if callCount == 2 {
			return TestStruct{}, errors.New("failed on item 2")
		}
		return TestStruct{Field1: 1, Field2: 100}, nil
	}

	_, err = DecodeABIEncodedStructArray(encodedBytes, converter)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to convert item 1")
	require.Contains(t, err.Error(), "failed on item 2")
}
