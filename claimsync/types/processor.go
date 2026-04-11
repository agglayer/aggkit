package types

import (
	"context"

	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/sync"
)

type EmbeddedProcessor interface {
	ProcessBlockWithTx(ctx context.Context, tx dbtypes.Querier, block sync.Block, eventRaw any) error
	ReorgWithTx(ctx context.Context, tx dbtypes.Querier, firstReorgedBlock uint64) (int64, error)
}
