package abi

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// DecodeABIEncodedStructArray is a generic helper that decodes ABI-encoded tuple array
// It handles the ABI unpacking and type conversion boilerplate
func DecodeABIEncodedStructArray[T any](
	encodedBytes []byte,
	converter func(any) (T, error),
) ([]T, error) {
	if len(encodedBytes) == 0 {
		return nil, errors.New("encoded bytes are empty")
	}

	var item T
	abiFields, err := BuildABIFields(item)
	if err != nil {
		return nil, fmt.Errorf("failed to build ABI fields: %w", err)
	}

	arrayType, err := abi.NewType("tuple[]", "", abiFields)
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

	// The unpacked[0] contains the slice, but we need to extract it via reflection
	// since the ABI library returns anonymous structs
	val := reflect.ValueOf(unpacked[0])
	if val.Kind() != reflect.Slice {
		return nil, fmt.Errorf("expected slice, got %v", val.Kind())
	}

	result := make([]T, val.Len())
	for i := 0; i < val.Len(); i++ {
		item := val.Index(i).Interface()
		converted, err := converter(item)
		if err != nil {
			return nil, fmt.Errorf("failed to convert item %d: %w", i, err)
		}
		result[i] = converted
	}

	return result, nil
}
