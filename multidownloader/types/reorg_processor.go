package types

import "context"

type ReorgProcessor interface {
	// ProcessReorg processes a detected reorg starting from the offending block number.
	// It identifies the range of blocks affected by the reorg and takes necessary actions
	// to handle the reorganization.
	ProcessReorg(ctx context.Context, offendingBlockNumber uint64) error
}
