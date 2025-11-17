package etherman

import (
	"context"

	aggkittypes "github.com/agglayer/aggkit/types"
)

type BlockNotifierManagerInterface interface {
	GetBlockNotifier(ctx context.Context, finality aggkittypes.BlockNumberFinality) (BlockNotifier, error)
}
