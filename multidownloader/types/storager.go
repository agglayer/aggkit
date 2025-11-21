package types

import (
	"context"

	dbtypes "github.com/agglayer/aggkit/db/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/core/types"
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

	GetBlockHeaderNotFinal(tx dbtypes.Querier, finalizedBlockNumber uint64) ([]*aggkittypes.BlockHeader, error)
	NewTx(ctx context.Context) (dbtypes.Txer, error)
}
