package types

import (
	"errors"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

// DetectedReorgError is an error that is raised when a reorg is detected
// The block is one of the blocks that were reorged, but not necessarily the first one
type DetectedReorgError struct {
	OffendingBlockNumber uint64 // Important: is not the first reorged block, but one of them
	OldHash              common.Hash
	NewHash              common.Hash
	Message              string
}

// IsDetectedReorgError checks if an error is a DetectedReorgError
func IsDetectedReorgError(err error) bool {
	c := CastDetectedReorgError(err)
	return c != nil
}

// NewDetectedReorgError creates a new DetectedReorgError
func NewDetectedReorgError(offendingBlockNumber uint64,
	oldHash, newHash common.Hash, msg string) *DetectedReorgError {
	return &DetectedReorgError{
		OffendingBlockNumber: offendingBlockNumber,
		OldHash:              oldHash,
		NewHash:              newHash,
		Message:              msg,
	}
}

func (e *DetectedReorgError) Error() string {
	return fmt.Sprintf("reorgError: block number %d: old hash %s != new hash %s: %s",
		e.OffendingBlockNumber, e.OldHash.String(), e.NewHash.String(), e.Message)
}

func CastDetectedReorgError(err error) *DetectedReorgError {
	var reorgErr *DetectedReorgError
	if errors.As(err, &reorgErr) {
		return reorgErr
	}
	return nil
}

// // GetDetectedReorgErrorBlockNumber returns the block number that caused the reorg
// func GetDetectedReorgErrorBlockNumber(err error) uint64 {
// 	if reorgErr, ok := err.(*DetectedReorgError); ok {
// 		return reorgErr.BlockNumber
// 	}
// 	return 0
// }

// // GetDetectedReorgErrorWrappedError returns the wrapped error that caused the reorg
// func GetDetectedReorgErrorWrappedError(err error) error {
// 	if reorgErr, ok := err.(*DetectedReorgError); ok {
// 		return reorgErr.Err
// 	}
// 	return nil
// }

type ReorgedError struct {
	Message           string
	BlockRangeReorged aggkitcommon.BlockRange
	ReorgedChainID    uint64
}

func NewReorgedError(blockRangeReorged aggkitcommon.BlockRange,
	reorgedChainID uint64,
	msg string) *ReorgedError {
	return &ReorgedError{
		Message:           msg,
		BlockRangeReorged: blockRangeReorged,
		ReorgedChainID:    reorgedChainID,
	}
}

func (e *ReorgedError) Error() string {
	return fmt.Sprintf("reorgedError: chainID=%d blockRangeReorged=%s: %s",
		e.ReorgedChainID, e.BlockRangeReorged.String(), e.Message)
}

// IsReorgedError checks if an error is a ReorgedError
func IsReorgedError(err error) bool {
	c := CastReorgedError(err)
	return c != nil
}

func CastReorgedError(err error) *ReorgedError {
	var reorgErr *ReorgedError
	if errors.As(err, &reorgErr) {
		return reorgErr
	}
	return nil
}
