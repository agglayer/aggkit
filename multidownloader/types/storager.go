package types

import (
	"context"

	dbtypes "github.com/agglayer/aggkit/db/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type FinalizedType = bool

const (
	NotFinalized FinalizedType = false
	Finalized    FinalizedType = true
)

type Storager interface {
	StoragerForReorg
	dbtypes.KeyValueStorager
	// GetSyncedBlockRangePerContract It returns the synced block range stored in DB
	GetSyncedBlockRangePerContract(tx dbtypes.Querier) (SetSyncSegment, error)
	SaveEthLogsWithHeaders(tx dbtypes.Querier, blockHeaders aggkittypes.ListBlockHeaders,
		logs []types.Log, isFinal bool) error
	// TODO: Deprecate GetEthLogs and use LogQuery instead
	GetEthLogs(tx dbtypes.Querier, query LogQuery) ([]types.Log, error)
	LogQuery(tx dbtypes.Querier, query LogQuery) (LogQueryResponse, error)
	UpdateSyncedStatus(tx dbtypes.Querier, segments []SyncSegment) error
	UpsertSyncerConfigs(tx dbtypes.Querier, configs []ContractConfig) error
	GetBlockHeaderByNumber(tx dbtypes.Querier, blockNumber uint64) (*aggkittypes.BlockHeader, FinalizedType, error)
	NewTx(ctx context.Context) (dbtypes.Txer, error)
	// GetBlockHeadersNotFinalized retrieves all block headers that are not finalized <= maxBlock
	// if maxBlock is nil, retrieves all not finalized blocks
	GetBlockHeadersNotFinalized(tx dbtypes.Querier, maxBlock *uint64) (aggkittypes.ListBlockHeaders, error)
	UpdateBlockToFinalized(tx dbtypes.Querier, blockNumbers []uint64) error
	GetRangeBlockHeader(tx dbtypes.Querier, isFinal FinalizedType) (lowest *aggkittypes.BlockHeader,
		highest *aggkittypes.BlockHeader, err error)
	// GetHighestBlockNumber returns the highest block number stored in db
	GetHighestBlockNumber(tx dbtypes.Querier) (uint64, error)
	// GetBlockReorgedReorgID returns the reorgID of the reorged block if exists
	// second return value indicates if the block is reorged
	GetBlockReorgedReorgID(tx dbtypes.Querier,
		blockNumber uint64, blockHash common.Hash) (uint64, bool, error)
	GetReorgedDataByReorgID(tx dbtypes.Querier,
		reorgID uint64) (*ReorgData, error)
}

type StoragerForReorg interface {
	GetBlockHeaderByNumber(tx dbtypes.Querier, blockNumber uint64) (*aggkittypes.BlockHeader, FinalizedType, error)
	InsertReorgAndMoveReorgedBlocksAndLogs(tx dbtypes.Querier, reorgData ReorgData) (uint64, error)
}
