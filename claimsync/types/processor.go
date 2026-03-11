package types

import (
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/sync"
)

type EmbeddedProcessor interface {
	ProcessBlockWithTx(tx dbtypes.Querier, block *sync.Block, insertBlock bool) error
	ReorgWithTx(tx dbtypes.Querier, firstReorgedBlock uint64) (int64, error)
}
