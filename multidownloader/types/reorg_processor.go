package types

import (
	"context"

	aggkittypes "github.com/agglayer/aggkit/types"
)

type ReorgProcessor interface {
	// ProcessReorg processes a detected reorg starting from the offending block number.
	// It identifies the range of blocks affected by the reorg and takes necessary actions
	// to handle the reorganization.
	// input paramaeters:
	// - ctx: the context for managing cancellation and timeouts
	// - detectedReorgError: the error returned by the reorg detection logic, containing
	//   the offending block number and the reason for the reorg detection
	// - finalizedBlockTag: the block tag to consider as finalized (typically finalizedBlock)
	ProcessReorg(ctx context.Context, detectedReorgError DetectedReorgError,
		finalizedBlockTag aggkittypes.BlockNumberFinality) error
}
