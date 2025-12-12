package bridgesync

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"

	aggkitabi "github.com/agglayer/aggkit/abi"
	"github.com/ethereum/go-ethereum/common"
)

type LeafData struct {
	LeafType           uint8          `abiarg:"leafType"`
	OriginNetwork      uint32         `abiarg:"originNetwork"`
	OriginAddress      common.Address `abiarg:"originAddress"`
	DestinationNetwork uint32         `abiarg:"destinationNetwork"`
	DestinationAddress common.Address `abiarg:"destinationAddress"`
	Amount             *big.Int       `abiarg:"amount,uint256"`
	Metadata           []byte         `abiarg:"metadata"`
}

func (l LeafData) ToBridge(blockNum, blockPos, blockTimestamp uint64, depositCount uint32) Bridge {
	return Bridge{
		BlockNum: blockNum,
		// for all leaves from ForwardLET, BlockPos is the same as they are in the same transaction
		BlockPos:           blockPos,
		BlockTimestamp:     blockTimestamp,
		DepositCount:       depositCount,
		LeafType:           l.LeafType,
		OriginNetwork:      l.OriginNetwork,
		OriginAddress:      l.OriginAddress,
		DestinationNetwork: l.DestinationNetwork,
		DestinationAddress: l.DestinationAddress,
		Amount:             l.Amount,
		Metadata:           l.Metadata,
		// FromAddress is always zero address, because this is a recovery mechanism bridge
		// TxnSender is always zero address, because this is a recovery mechanism bridge
	}
}

// decodeForwardLETLeaves decodes the newLeaves bytes from a ForwardLET event
func decodeForwardLETLeaves(newLeavesBytes []byte) ([]LeafData, error) {
	return aggkitabi.DecodeABIEncodedStructArray(newLeavesBytes, convertABILeafData)
}

// convertABILeafData converts an anonymous struct returned by the ABI decoder
func convertABILeafData(item any) (LeafData, error) {
	// Use reflection to extract fields from the anonymous struct created by ABI library
	// The ABI library generates structs with JSON tags that don't match our named types
	val := reflect.ValueOf(item)
	if val.Kind() != reflect.Struct {
		return LeafData{}, fmt.Errorf("expected struct, got %T", item)
	}

	expectedFields := reflect.TypeOf(LeafData{}).NumField()
	if val.NumField() != expectedFields {
		return LeafData{}, fmt.Errorf("expected %d fields, got %d", expectedFields, val.NumField())
	}

	// Create a map of field names to values from the ABI struct
	fieldMap := make(map[string]any)
	valType := val.Type()
	for i := 0; i < val.NumField(); i++ {
		fieldName := valType.Field(i).Name
		fieldMap[fieldName] = val.Field(i).Interface()
	}

	// Extract fields by name with type assertions
	leafType, ok := fieldMap["LeafType"].(uint8)
	if !ok {
		return LeafData{}, errors.New("failed to convert field 'leafType' to uint8")
	}

	originNetwork, ok := fieldMap["OriginNetwork"].(uint32)
	if !ok {
		return LeafData{}, errors.New("failed to convert field 'originNetwork' to uint32")
	}

	originAddress, ok := fieldMap["OriginAddress"].(common.Address)
	if !ok {
		return LeafData{}, errors.New("failed to convert field 'originAddress' to common.Address")
	}

	destinationNetwork, ok := fieldMap["DestinationNetwork"].(uint32)
	if !ok {
		return LeafData{}, errors.New("failed to convert field 'destinationNetwork' to uint32")
	}

	destinationAddress, ok := fieldMap["DestinationAddress"].(common.Address)
	if !ok {
		return LeafData{}, errors.New("failed to convert field 'destinationAddress' to common.Address")
	}

	amount, ok := fieldMap["Amount"].(*big.Int)
	if !ok {
		return LeafData{}, errors.New("failed to convert field 'amount' to *big.Int")
	}

	metadata, ok := fieldMap["Metadata"].([]byte)
	if !ok {
		return LeafData{}, errors.New("failed to convert field 'metadata' to []byte")
	}

	return LeafData{
		LeafType:           leafType,
		OriginNetwork:      originNetwork,
		OriginAddress:      originAddress,
		DestinationNetwork: destinationNetwork,
		DestinationAddress: destinationAddress,
		Amount:             amount,
		Metadata:           metadata,
	}, nil
}
