package common

import (
	"context"
	"crypto/ecdsa"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/iden3/go-iden3-crypto/keccak256"
)

var (
	ZeroHash = common.HexToHash("0x0")
)

const (
	Uint32ByteSize = 4
	Uint64ByteSize = 8
)

// Uint64ToBytes converts a uint64 to a byte slice
func Uint64ToBytes(num uint64) []byte {
	bytes := make([]byte, Uint64ByteSize)
	binary.BigEndian.PutUint64(bytes, num)

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

// CalculateAccInputHash computes the hash of accumulated input data for a given batch.
func CalculateAccInputHash(
	logger *log.Logger,
	oldAccInputHash common.Hash,
	batchData []byte,
	l1InfoRoot common.Hash,
	timestampLimit uint64,
	sequencerAddr common.Address,
	forcedBlockhashL1 common.Hash,
) common.Hash {
	v1 := oldAccInputHash.Bytes()
	v2 := batchData
	v3 := l1InfoRoot.Bytes()
	v4 := big.NewInt(0).SetUint64(timestampLimit).Bytes()
	v5 := sequencerAddr.Bytes()
	v6 := forcedBlockhashL1.Bytes()

	// Add 0s to make values 32 bytes long
	for len(v1) < 32 {
		v1 = append([]byte{0}, v1...)
	}

	for len(v3) < 32 {
		v3 = append([]byte{0}, v3...)
	}

	for len(v4) < 8 {
		v4 = append([]byte{0}, v4...)
	}

	for len(v5) < 20 {
		v5 = append([]byte{0}, v5...)
	}

	for len(v6) < 32 {
		v6 = append([]byte{0}, v6...)
	}

	v2 = keccak256.Hash(v2)
	calculatedAccInputHash := common.BytesToHash(keccak256.Hash(v1, v2, v3, v4, v5, v6))

	logger.Debugf("OldAccInputHash: %v", oldAccInputHash)
	logger.Debugf("BatchHashData: %v", common.Bytes2Hex(v2))
	logger.Debugf("L1InfoRoot: %v", l1InfoRoot)
	logger.Debugf("TimeStampLimit: %v", timestampLimit)
	logger.Debugf("Sequencer Address: %v", sequencerAddr)
	logger.Debugf("Forced BlockHashL1: %v", forcedBlockhashL1)
	logger.Debugf("CalculatedAccInputHash: %v", calculatedAccInputHash)

	return calculatedAccInputHash
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

var ErrNonRetryable = errors.New("non-retryable")

// RetryWithExponentialBackoff retries the given function up to maxRetries with exponential backoff.
// Use `context.Canceled` or `context.DeadlineExceeded` to cancel early.
// Wrap return with `fmt.Errorf("%w: your error", ErrNonRetryable)` to avoid retries.
func RetryWithExponentialBackoff(ctx context.Context, maxRetries int,
	initialDelay time.Duration, fn func() error) error {
	if fn == nil {
		return errors.New("retry function cannot be nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	delay := initialDelay
	var lastErr error

	for attempt := range maxRetries {
		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled after %d attempt(s): %w", attempt, ctx.Err())
		default:
		}

		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err

		// Exit early if the error is marked non-retryable
		if errors.Is(err, ErrNonRetryable) {
			return fmt.Errorf("non-retryable error after %d attempt(s): %w", attempt+1, err)
		}

		if attempt < maxRetries-1 {
			time.Sleep(delay)
			delay *= 2
		}
	}

	return fmt.Errorf("operation failed after %d retries: %w", maxRetries, lastErr)
}
