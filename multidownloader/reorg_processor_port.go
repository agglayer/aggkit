package multidownloader

import (
	"context"
	"fmt"

	dbtypes "github.com/agglayer/aggkit/db/types"
	mdtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
)

type compareBlockHeaders struct {
	StorageHeader *aggkittypes.BlockHeader
	IsFinalized   mdtypes.FinalizedType
	RpcHeader     *aggkittypes.BlockHeader
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
		return nil, err
	}
	rpcBlock, err := r.ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber))
	if err != nil {
		return nil, err
	}
	return &compareBlockHeaders{
		StorageHeader: currentStorageBlock,
		IsFinalized:   finalized,
		RpcHeader:     rpcBlock,
	}, nil
}

func (r *ReorgPort) GetLastBlockNumberInStorage(tx dbtypes.Querier) (uint64, error) {
	highestBlock, _, err := r.storage.GetRangeBlockHeader(nil, mdtypes.NotFinalized)
	if err != nil {
		return 0, fmt.Errorf("GetLastBlockNumberInStorage: error getting highest block from storage: %w", err)
	}
	if highestBlock == nil {
		return 0, fmt.Errorf("GetLastBlockNumberInStorage: error getting highest block (=nil) from storage")
	}
	return highestBlock.Number, nil
}

func (r *ReorgPort) MoveReorgedBlocks(tx dbtypes.Querier, reorgData mdtypes.ReorgData) (uint64, error) {
	return r.storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
}

func (r *ReorgPort) GetLatestBlockNumberInRPC(ctx context.Context) (uint64, error) {
	latestBlockNumber, err := r.ethClient.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("GetLatestBlockNumber: error getting latest block number from RPC: %w", err)
	}
	return latestBlockNumber, nil
}
