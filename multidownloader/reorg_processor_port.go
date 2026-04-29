package multidownloader

import (
	"context"
	"fmt"
	"time"

	dbtypes "github.com/agglayer/aggkit/db/types"
	mdtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type ReorgPort struct {
	ethClient aggkittypes.BaseEthereumClienter
	rpcClient aggkittypes.RPCClienter
	storage   mdtypes.Storager
}

func (r *ReorgPort) NewTx(ctx context.Context) (dbtypes.Txer, error) {
	return r.storage.NewTx(ctx)
}

func (r *ReorgPort) GetBlockStorageAndRPC(ctx context.Context, tx dbtypes.Querier,
	blockNumber uint64) (*mdtypes.CompareBlockHeaders, error) {
	currentStorageBlock, finalized, err := r.storage.GetBlockHeaderByNumber(tx, blockNumber)
	if err != nil {
		return nil, fmt.Errorf("error getting block in storage: %w", err)
	}
	rpcBlock, err := r.ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber))
	if err != nil && !aggkittypes.IsErrNotFound(err) {
		return nil, fmt.Errorf("error getting block in RPC: %w", err)
	}
	return &mdtypes.CompareBlockHeaders{
		BlockNumber:   blockNumber,
		StorageHeader: currentStorageBlock,
		IsFinalized:   finalized,
		RpcHeader:     rpcBlock,
	}, nil
}

func (r *ReorgPort) GetLastBlockNumberInStorage(tx dbtypes.Querier) (uint64, error) {
	highestBlock, err := r.storage.GetHighestBlockNumber(tx)
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

func (r *ReorgPort) TimeNowUnix() uint64 {
	return uint64(time.Now().Unix())
}
