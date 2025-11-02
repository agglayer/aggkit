package types

import (
	"context"

	aggkittypes "github.com/agglayer/aggkit/types"
)

type BlockNotifierManagerGetter interface {
	GetBlockNotifier(ctx context.Context, finality aggkittypes.BlockNumberFinality) (BlockNotifier, error)
}
