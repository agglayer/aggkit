package abi

import (
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// BuildABIFields constructs ABI ArgumentMarshaling slice from a struct type using reflection
// It uses the "abiarg" tag to determine field names and optionally types
// Tag format: `abiarg:"fieldName"` or `abiarg:"fieldName,type"`
// If type is omitted, it will be inferred from the Go type
func BuildABIFields(structType any) ([]abi.ArgumentMarshaling, error) {
	t := reflect.TypeOf(structType)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct type, got %v", t.Kind())
	}

	fields := make([]abi.ArgumentMarshaling, 0, t.NumField())

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		abiTag := field.Tag.Get("abi")
		if abiTag == "" {
			continue // Skip fields without abi tag
		}

		parts := strings.Split(abiTag, ",")
		name := parts[0]

		// Infer type from Go type
		inferredType, err := inferABIType(field.Type)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}

		fields = append(fields, abi.ArgumentMarshaling{
			Name: name,
			Type: inferredType,
		})
	}

	return fields, nil
}

// inferABIType automatically maps Go types to Solidity ABI types
func inferABIType(goType reflect.Type) (string, error) {
	// Handle special types first (before checking Kind)
	switch goType {
	case reflect.TypeOf(common.Address{}):
		return "address", nil
	case reflect.TypeOf(&big.Int{}), reflect.TypeOf(big.Int{}):
		// Default to uint256 for big.Int, but can be overridden with explicit tag
		return "uint256", nil
	case reflect.TypeOf(common.Hash{}):
		return "bytes32", nil
	}

	switch goType.Kind() {
	case reflect.Uint8:
		return "uint8", nil
	case reflect.Uint16:
		return "uint16", nil
	case reflect.Uint32:
		return "uint32", nil
	case reflect.Uint64:
		return "uint64", nil
	case reflect.Int8:
		return "int8", nil
	case reflect.Int16:
		return "int16", nil
	case reflect.Int32:
		return "int32", nil
	case reflect.Int64:
		return "int64", nil
	case reflect.Bool:
		return "bool", nil
	case reflect.String:
		return "string", nil
	case reflect.Slice:
		if goType.Elem().Kind() == reflect.Uint8 {
			return "bytes", nil
		}
		return "", fmt.Errorf("unsupported slice type: %v", goType)
	case reflect.Array:
		if goType.Elem().Kind() == reflect.Uint8 {
			return fmt.Sprintf("bytes%d", goType.Len()), nil
		}
		return "", fmt.Errorf("unsupported array type: %v", goType)
	}

	return "", fmt.Errorf("unsupported type: %v", goType)
}
