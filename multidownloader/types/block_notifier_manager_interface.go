package types

import (
	"context"

	ethermantypes "github.com/agglayer/aggkit/etherman/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type BlockNotifierManagerGetter interface {
	GetBlockNotifier(ctx context.Context, finality aggkittypes.BlockNumberFinality) (ethermantypes.BlockNotifier, error)
}
