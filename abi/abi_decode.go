package abi

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

const tupleArrayType = "tuple[]"

// DecodeABIEncodedStructArray is a generic helper that decodes ABI-encoded tuple array
// It handles the ABI unpacking and type conversion boilerplate
func DecodeABIEncodedStructArray[T any](encodedBytes []byte) ([]T, error) {
	if len(encodedBytes) == 0 {
		return nil, errors.New("encoded bytes are empty")
	}

	var item T
	abiFields, err := BuildABIFields(item)
	if err != nil {
		return nil, fmt.Errorf("failed to build ABI fields: %w", err)
	}

	arrayType, err := abi.NewType(tupleArrayType, "", abiFields)
	if err != nil {
		return nil, fmt.Errorf("failed to create array type: %w", err)
	}

	args := abi.Arguments{{Type: arrayType, Name: "data"}}

	unpacked, err := args.Unpack(encodedBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack data: %w", err)
	}

	if len(unpacked) == 0 {
		return nil, errors.New("unpacked data is empty")
	}

	decodedData, ok := abi.ConvertType(unpacked[0], new([]T)).(*[]T)
	if !ok {
		return nil, errors.New("failed to convert unpacked data to the expected type")
	}

	return *decodedData, nil
}
