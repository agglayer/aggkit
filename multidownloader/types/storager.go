package types

import (
	"context"

	dbtypes "github.com/agglayer/aggkit/db/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/core/types"
)

type FinalizedType = bool

const (
	NotFinalized FinalizedType = false
	Finalized    FinalizedType = true
)

type Storager interface {
	dbtypes.KeyValueStorager
	// GetSyncedBlockRangePerContract It returns the synced block range stored in DB
	GetSyncedBlockRangePerContract(tx dbtypes.Querier) (SetSyncSegment, error)
	SaveEthLogsWithHeaders(tx dbtypes.Querier, blockHeaders []*aggkittypes.BlockHeader,
		logs []types.Log, isFinal bool) error
	GetEthLogs(tx dbtypes.Querier, query LogQuery) ([]types.Log, error)
	UpdateSyncedStatus(tx dbtypes.Querier, segments []SyncSegment) error
	UpsertSyncerConfigs(tx dbtypes.Querier, configs []ContractConfig) error
	GetBlockHeaderByNumber(tx dbtypes.Querier, blockNumber uint64) (*aggkittypes.BlockHeader, bool, error)
	NewTx(ctx context.Context) (dbtypes.Txer, error)

	GetBlockHeadersNotFinalized(tx dbtypes.Querier, maxBlock uint64) ([]*aggkittypes.BlockHeader, error)
	UpdateBlockToFinalized(tx dbtypes.Querier, blockNumbers []uint64) error
	GetRangeBlockHeader(tx dbtypes.Querier, isFinal FinalizedType) (lowest *aggkittypes.BlockHeader,
		highest *aggkittypes.BlockHeader, err error)
}
