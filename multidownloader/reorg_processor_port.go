package multidownloader

import (
	"context"
	"fmt"

	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/etherman"
	mdtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type compareBlockHeaders struct {
	BlockNumber   uint64
	StorageHeader *aggkittypes.BlockHeader
	IsFinalized   mdtypes.FinalizedType
	RpcHeader     *aggkittypes.BlockHeader
}

func (c *compareBlockHeaders) ExistsRPCBlock() bool {
	if c == nil {
		return false
	}
	return c.RpcHeader != nil
}
func (c *compareBlockHeaders) ExistsStorageBlock() bool {
	if c == nil {
		return false
	}
	return c.StorageHeader != nil
}

type ReorgPort struct {
	ethClient aggkittypes.BaseEthereumClienter
	rpcClient aggkittypes.RPCClienter
	storage   mdtypes.Storager
}

func (r *ReorgPort) NewTx(ctx context.Context) (dbtypes.Txer, error) {
	return r.storage.NewTx(ctx)
}

func (r *ReorgPort) GetBlockStorageAndRPC(ctx context.Context, tx dbtypes.Querier,
	blockNumber uint64) (*compareBlockHeaders, error) {
	currentStorageBlock, finalized, err := r.storage.GetBlockHeaderByNumber(tx, blockNumber)
	if err != nil {
		return nil, fmt.Errorf("error getting block in storage: %w", err)
	}
	rpcBlock, err := r.ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber))
	if err != nil && !etherman.IsErrNotFound(err) {
		return nil, fmt.Errorf("error getting block in RPC: %w", err)
	}
	return &compareBlockHeaders{
		BlockNumber:   blockNumber,
		StorageHeader: currentStorageBlock,
		IsFinalized:   finalized,
		RpcHeader:     rpcBlock,
	}, nil
}

func (r *ReorgPort) GetLastBlockNumberInStorage(tx dbtypes.Querier) (uint64, error) {
	highestBlock, err := r.storage.GetHighestBlockNumber(nil)
	if err != nil {
		return 0, fmt.Errorf("GetLastBlockNumberInStorage: error getting highest block from storage: %w", err)
	}
	return highestBlock, nil
}
func (r *ReorgPort) MoveReorgedBlocks(tx dbtypes.Querier, reorgData mdtypes.ReorgData) (uint64, error) {
	return r.storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
}

func (r *ReorgPort) GetBlockNumberInRPC(
	ctx context.Context, blockFinality aggkittypes.BlockNumberFinality,
) (uint64, error) {
	blockNumber, err := r.ethClient.CustomHeaderByNumber(ctx, &blockFinality)
	if err != nil {
		return 0, fmt.Errorf("GetBlockNumberInRPC: error getting block number for %s from RPC: %w",
			blockFinality.String(), err)
	}
	return blockNumber.Number, nil
}
