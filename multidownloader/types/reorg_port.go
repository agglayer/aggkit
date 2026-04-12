package types

import (
	"context"

	dbtypes "github.com/agglayer/aggkit/db/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type ReorgPorter interface {
	NewTx(ctx context.Context) (dbtypes.Txer, error)
	GetBlockStorageAndRPC(ctx context.Context, tx dbtypes.Querier, blockNumber uint64) (*CompareBlockHeaders, error)
	GetLastBlockNumberInStorage(tx dbtypes.Querier) (uint64, error)
	// Return ChainID of the inserted reorg
	MoveReorgedBlocks(tx dbtypes.Querier, reorgData ReorgData) (uint64, error)
	GetBlockNumberInRPC(ctx context.Context, blockFinality aggkittypes.BlockNumberFinality) (uint64, error)
	TimeNowUnix() uint64
}

type CompareBlockHeaders struct {
	BlockNumber   uint64
	StorageHeader *aggkittypes.BlockHeader
	IsFinalized   FinalizedType
	RpcHeader     *aggkittypes.BlockHeader
}

func (c *CompareBlockHeaders) ExistsRPCBlock() bool {
	if c == nil {
		return false
	}
	return c.RpcHeader != nil
}
func (c *CompareBlockHeaders) ExistsStorageBlock() bool {
	if c == nil {
		return false
	}
	return c.StorageHeader != nil
}
