package common

import (
	"crypto/ecdsa"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const KB = 1 << 10 // 1024

var (
	ZeroHash       = common.HexToHash("0x0")
	EmptyBytesHash = crypto.Keccak256(nil)
	ZeroAddress    = common.HexToAddress("0x0")
	EmptySignature = make([]byte, SignatureSize)
)

const (
	Uint32ByteSize = 4
	Uint64ByteSize = 8

	SignatureSize = 65
	HashSize      = 32
)

// Uint64ToBigEndianBytes converts a uint64 to a byte slice in big-endian order
func Uint64ToBigEndianBytes(num uint64) []byte {
	bytes := make([]byte, Uint64ByteSize)
	binary.BigEndian.PutUint64(bytes, num)

	return bytes
}

// Uint64ToLittleEndianBytes converts a uint64 to a byte slice in little-endian order
func Uint64ToLittleEndianBytes(num uint64) []byte {
	bytes := make([]byte, Uint64ByteSize)
	binary.LittleEndian.PutUint64(bytes, num)

	return bytes
}

// BytesToUint64 converts a byte slice to a uint64
func BytesToUint64(bytes []byte) uint64 {
	if len(bytes) > Uint64ByteSize {
		panic("Uint64ByteSize: input byte slice is too long")
	}

	padded := make([]byte, Uint64ByteSize)
	copy(padded[Uint64ByteSize-len(bytes):], bytes)
	return binary.BigEndian.Uint64(padded)
}

// Uint32ToBigEndianBytes converts a uint32 to a byte slice in big-endian order
// it's an alias of Uint32ToBytes
func Uint32ToBigEndianBytes(num uint32) []byte {
	return Uint32ToBytes(num)
}

// Uint32ToBytes converts a uint32 to a byte slice in big-endian order
func Uint32ToBytes(num uint32) []byte {
	bytes := make([]byte, Uint32ByteSize)
	binary.BigEndian.PutUint32(bytes, num)

	return bytes
}

// BytesToUint32 converts a byte slice to a uint32.
// If byte slice is shorter than 4 bytes, it is padded with 0s.
// In case it is longer than 4 bytes, it panics.
func BytesToUint32(bytes []byte) uint32 {
	if len(bytes) > Uint32ByteSize {
		panic("BytesToUint32: input byte slice is too long")
	}

	padded := make([]byte, Uint32ByteSize)
	copy(padded[Uint32ByteSize-len(bytes):], bytes)
	return binary.BigEndian.Uint32(padded)
}

// NewKeyFromKeystore creates a private key from a keystore file
func NewKeyFromKeystore(cfg types.KeystoreFileConfig) (*ecdsa.PrivateKey, error) {
	if cfg.Path == "" && cfg.Password == "" {
		return nil, nil
	}
	keystoreEncrypted, err := os.ReadFile(filepath.Clean(cfg.Path))
	if err != nil {
		return nil, err
	}
	key, err := keystore.DecryptKey(keystoreEncrypted, cfg.Password)
	if err != nil {
		return nil, err
	}
	return key.PrivateKey, nil
}

// BigIntToLittleEndianBytes converts a big.Int to a 32-byte little-endian representation.
// big.Int is capped to 32 bytes
func BigIntToLittleEndianBytes(n *big.Int) []byte {
	// Get the absolute value in big-endian byte slice
	beBytes := n.Bytes()

	// Initialize a 32-byte array for the result
	leBytes := make([]byte, common.HashLength)

	// Fill the array in reverse order to convert to little-endian
	for i := 0; i < len(beBytes) && i < common.HashLength; i++ {
		leBytes[i] = beBytes[len(beBytes)-1-i]
	}

	return leBytes
}

// EstimateSliceCapacity estimates the capacity of a slice based on the total number
// of elements, the span of interest, and the full span of the range.
//
// Parameters:
//   - total: The total number of elements.
//   - span: The span of interest within the range.
//   - fullSpan: The full span of the range.
//
// Returns:
//   - An integer representing the estimated slice capacity. If fullSpan is 0, the
//     function returns 0 to avoid division by zero. If the calculation would result
//     in integer overflow, it returns math.MaxInt to prevent overflow.
func EstimateSliceCapacity(total int, span, fullSpan uint64) int {
	if fullSpan == 0 {
		return 0
	}
	result := (uint64(total) * span) / fullSpan
	// Check if result would overflow when converting to int
	if result > uint64(math.MaxInt) {
		return math.MaxInt
	}

	return int(result)
}

// MapSlice transforms a slice of type T into a slice of type R using the provided mapping function f.
// It's a generic utility that reduces boilerplate when converting between types.
func MapSlice[T any, R any](in []T, f func(T) R) []R {
	out := make([]R, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

const (
	hexBase = 16
	decBase = 10
)

func IsHex(s string) bool {
	return len(s) > 2 && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X"))
}

func ParseUint64Hex(hexStr string) (uint64, error) {
	if !IsHex(hexStr) {
		return 0, fmt.Errorf("ParseUint64Hex: invalid hex string %s", hexStr)
	}
	bigInt, ok := new(big.Int).SetString(hexStr[2:], hexBase)
	if !ok {
		return 0, fmt.Errorf("ParseUint64Hex: invalid hex string %s", hexStr)
	}
	return bigInt.Uint64(), nil
}

func ParseUint64HexOrDecimal(str string) (uint64, error) {
	if IsHex(str) {
		return ParseUint64Hex(str)
	}
	num, err := strconv.ParseUint(str, decBase, 64)
	if err != nil {
		return 0, fmt.Errorf("ParseUint64HexOrDecimal: invalid decimal string %s: %w", str, err)
	}
	return num, nil
}
