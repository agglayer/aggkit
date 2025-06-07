package common

import (
	"crypto/ecdsa"
	"encoding/binary"
	"math/big"
	"os"
	"path/filepath"

	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
)

const KB = 1 << 10 // 1024

var (
	ZeroHash = common.HexToHash("0x0")
)

const (
	Uint32ByteSize = 4
	Uint64ByteSize = 8
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
//     function returns 0 to avoid division by zero.
func EstimateSliceCapacity(total int, span, fullSpan uint64) int {
	if fullSpan == 0 {
		return 0
	}
	return int((uint64(total) * span) / fullSpan)
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
