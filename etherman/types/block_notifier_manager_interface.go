package types

import (
	"context"

	aggkittypes "github.com/agglayer/aggkit/types"
)

type BlockNotifierManager interface {
	GetBlockNotifier(ctx context.Context, finality aggkittypes.BlockNumberFinality) (BlockNotifier, error)
	GetCurrentBlockNumber(ctx context.Context, finality aggkittypes.BlockNumberFinality) (uint64, error)
}
