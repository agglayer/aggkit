package abi

import (
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// EncodeABIStructArray is a generic helper that encodes a slice of structs to ABI-encoded tuple array
// It automatically builds ABI fields from the struct type using reflection and abiarg tags
func EncodeABIStructArray[T any](items []T) ([]byte, error) {
	// For empty slices, we need a sample instance to build ABI fields
	var item T
	if len(items) > 0 {
		// Use the first item to infer the type
		item = items[0]
	}

	// Use first item to build ABI fields
	abiFields, err := BuildABIFields(item)
	if err != nil {
		return nil, fmt.Errorf("failed to build ABI fields: %w", err)
	}

	arrayType, err := abi.NewType(tupleArrayType, "", abiFields)
	if err != nil {
		return nil, fmt.Errorf("failed to create array type: %w", err)
	}

	args := abi.Arguments{{Type: arrayType, Name: "data"}}

	encodedBytes, err := args.Pack(items)
	if err != nil {
		return nil, fmt.Errorf("failed to pack data: %w", err)
	}

	return encodedBytes, nil
}
