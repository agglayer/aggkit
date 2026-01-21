package types

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

// ReorgError is an error that is raised when a reorg is detected
// The block is one of the blocks that were reorged, but not necessarily the first one
type ReorgError struct {
	OffendingBlockNumber uint64 // Important: is not the first reorged block, but one of them
	OldHash              common.Hash
	NewHash              common.Hash
	Message              string
}

// IsReorgError checks if an error is a ReorgError
func IsReorgError(err error) bool {
	c := CastReorgError(err)
	return c != nil
}

// NewReorgError creates a new ReorgError
func NewReorgError(offendingBlockNumber uint64,
	oldHash, newHash common.Hash, msg string) *ReorgError {
	return &ReorgError{
		OffendingBlockNumber: offendingBlockNumber,
		OldHash:              oldHash,
		NewHash:              newHash,
		Message:              msg,
	}
}

func (e *ReorgError) Error() string {
	return fmt.Sprintf("reorgError: block number %d: old hash %s != new hash %s: %s",
		e.OffendingBlockNumber, e.OldHash.String(), e.NewHash.String(), e.Message)
}

func CastReorgError(err error) *ReorgError {
	var reorgErr *ReorgError
	if errors.As(err, &reorgErr) {
		return reorgErr
	}
	return nil
}

// // GetReorgErrorBlockNumber returns the block number that caused the reorg
// func GetReorgErrorBlockNumber(err error) uint64 {
// 	if reorgErr, ok := err.(*ReorgError); ok {
// 		return reorgErr.BlockNumber
// 	}
// 	return 0
// }

// // GetReorgErrorWrappedError returns the wrapped error that caused the reorg
// func GetReorgErrorWrappedError(err error) error {
// 	if reorgErr, ok := err.(*ReorgError); ok {
// 		return reorgErr.Err
// 	}
// 	return nil
// }
