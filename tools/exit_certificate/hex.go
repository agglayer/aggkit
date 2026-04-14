package exit_certificate

import (
	"fmt"
	"math/big"
	"strings"
)

// hexToUint64 parses a hex string (with or without 0x prefix) to uint64.
func hexToUint64(s string) uint64 {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	var n uint64
	for _, c := range s {
		n <<= 4
		switch {
		case c >= '0' && c <= '9':
			n |= uint64(c - '0')
		case c >= 'a' && c <= 'f':
			n |= uint64(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			n |= uint64(c - 'A' + 10)
		}
	}
	return n
}

// hexToBigInt parses a 0x-prefixed hex string to a *big.Int. Returns zero on empty/invalid input.
func hexToBigInt(s string) *big.Int {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		return new(big.Int)
	}
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return new(big.Int)
	}
	return n
}

// toBlockTag formats a block number as a 0x-prefixed hex string for use in RPC calls.
func toBlockTag(blockNum uint64) string {
	return fmt.Sprintf("0x%x", blockNum)
}

// parseDecimalBigInt parses a decimal string to *big.Int. Returns zero on empty/invalid input.
func parseDecimalBigInt(s string) *big.Int {
	if s == "" {
		return new(big.Int)
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return new(big.Int)
	}
	return n
}
