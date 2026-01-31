package types

import (
	"errors"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

type ReorgDetectionReason int

const (
	ReorgDetectionReason_BlockHashMismatch ReorgDetectionReason = iota + 1
	ReorgDetectionReason_ParentHashMismatch
	ReorgDetectionReason_MissingBlock
	ReorgDetectionReason_Forced
)

func (r ReorgDetectionReason) String() string {
	switch r {
	case ReorgDetectionReason_BlockHashMismatch:
		return "BlockHashMismatch"
	case ReorgDetectionReason_ParentHashMismatch:
		return "ParentHashMismatch"
	case ReorgDetectionReason_MissingBlock:
		return "MissingBlock"
	case ReorgDetectionReason_Forced:
		return "Forced"
	}
	return fmt.Sprintf("ReorgDetectionReason(%d)", int(r))
}

// DetectedReorgError is an error that is raised when a reorg is detected
// The block is one of the blocks that were reorged, but not necessarily the first one
type DetectedReorgError struct {
	OffendingBlockNumber uint64 // Important: is not the first reorged block, but one of them
	OldHash              common.Hash
	NewHash              common.Hash
	ReorgDetectionReason ReorgDetectionReason
	Message              string
}

// IsDetectedReorgError checks if an error is a DetectedReorgError
func IsDetectedReorgError(err error) bool {
	c := CastDetectedReorgError(err)
	return c != nil
}

// NewDetectedReorgError creates a new DetectedReorgError
func NewDetectedReorgError(offendingBlockNumber uint64,
	reason ReorgDetectionReason,
	oldHash, newHash common.Hash, msg string) *DetectedReorgError {
	return &DetectedReorgError{
		OffendingBlockNumber: offendingBlockNumber,
		OldHash:              oldHash,
		NewHash:              newHash,
		ReorgDetectionReason: reason,
		Message:              msg,
	}
}

func (e *DetectedReorgError) Error() string {
	switch e.ReorgDetectionReason {
	case ReorgDetectionReason_MissingBlock:
		return fmt.Sprintf("reorgError: block number %d is missing: %s",
			e.OffendingBlockNumber, e.Message)
	case ReorgDetectionReason_BlockHashMismatch:
		return fmt.Sprintf("reorgError: block number %d: old hash %s != new hash %s: %s",
			e.OffendingBlockNumber, e.OldHash.String(), e.NewHash.String(), e.Message)
	case ReorgDetectionReason_ParentHashMismatch:
		return fmt.Sprintf("reorgError: block number %d: old parent hash %s != new parent hash %s: %s",
			e.OffendingBlockNumber, e.OldHash.String(), e.NewHash.String(), e.Message)
	case ReorgDetectionReason_Forced:
		return fmt.Sprintf("reorgError: block number %d: forced reason: %s",
			e.OffendingBlockNumber, e.Message)
	default:
		return fmt.Sprintf("reorgError: block number %d: reason %d: %s",
			e.OffendingBlockNumber, e.ReorgDetectionReason, e.Message)
	}
}

func CastDetectedReorgError(err error) *DetectedReorgError {
	var reorgErr *DetectedReorgError
	if errors.As(err, &reorgErr) {
		return reorgErr
	}
	return nil
}

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
