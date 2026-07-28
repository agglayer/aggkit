package exit_certificate

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

const (
	hexBase         = 16
	decimalBase     = 10
	maxMetadataSize = 1 << 20 // 1 MB

	abiWordBytes      = 32  // EVM ABI word size in bytes
	twoABIWords       = 64  // two ABI words (offset + length header for dynamic types)
	fourABIWords      = 128 // four ABI words (error decoder minimum size)
	splitInTwo        = 2   // used with strings.SplitN
	bridgeEventFields = 8   // number of fields in the BridgeEvent log
	ethDecimals       = 18  // standard ETH/ERC-20 decimal precision
	minTopicsForLeaf  = 2   // minimum topics required to extract leaf count
	uncheckedStatus   = "unchecked"
	okStatus          = "ok"
	errorStatus       = "error"
)

// safeUint32 converts a big.Int to uint32, returning an error on overflow.
func safeUint32(val *big.Int) (uint32, error) {
	if !val.IsUint64() || val.Uint64() > math.MaxUint32 {
		return 0, fmt.Errorf("value %s overflows uint32", val)
	}
	return uint32(val.Uint64()), nil
}

// safeUint8 converts a big.Int to uint8, returning an error on overflow.
func safeUint8(val *big.Int) (uint8, error) {
	if !val.IsUint64() || val.Uint64() > math.MaxUint8 {
		return 0, fmt.Errorf("value %s overflows uint8", val)
	}
	return uint8(val.Uint64()), nil
}

// hexToUint64 parses a hex string (with or without 0x prefix) to uint64.
// Invalid characters, empty input and overflow are errors: the strings it parses
// are RPC-provided block numbers, so a garbage value must never silently become
// a plausible-looking number.
func hexToUint64(s string) (uint64, error) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	n, err := strconv.ParseUint(trimmed, hexBase, 64)
	if err != nil {
		return 0, fmt.Errorf("parse hex uint64 %q: %w", s, err)
	}
	return n, nil
}

// hexToBigInt parses a 0x-prefixed hex string to a *big.Int. Returns zero on empty/invalid input.
func hexToBigInt(s string) *big.Int {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		return new(big.Int)
	}
	n, ok := new(big.Int).SetString(s, hexBase)
	if !ok {
		return new(big.Int)
	}
	return n
}

// toBlockTag formats a block number as a 0x-prefixed hex string for use in RPC calls.
func toBlockTag(blockNum uint64) string {
	return fmt.Sprintf("0x%x", blockNum)
}

// parseDecimalBigInt parses a decimal string to *big.Int. Empty or invalid input is an error:
// the strings it parses are tool-generated, so a failure always means corrupted intermediate
// files and must never silently become a zero amount (which would drop a bridge exit).
func parseDecimalBigInt(s string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(s, decimalBase)
	if !ok {
		return nil, fmt.Errorf("parse decimal big int: invalid value %q", s)
	}
	return n, nil
}
