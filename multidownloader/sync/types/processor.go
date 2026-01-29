package types

import (
	"context"

	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type ProcessorInterface interface {
	GetLastProcessedBlockHeader(ctx context.Context) (*aggkittypes.BlockHeader, error)
	ProcessBlock(ctx context.Context, block sync.Block) error
	Reorg(ctx context.Context, firstReorgedBlock uint64) error
}
