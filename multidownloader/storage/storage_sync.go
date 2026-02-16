package storage

import (
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	dbtypes "github.com/agglayer/aggkit/db/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

type syncStatusRow struct {
	Address         common.Address `meddler:"contract_address,address"`
	TargetFromBlock uint64         `meddler:"target_from_block"`
	TargetToBlock   string         `meddler:"target_to_block"`
	SyncedFromBlock uint64         `meddler:"synced_from_block"`
	SyncedToBlock   uint64         `meddler:"synced_to_block"`
	SyncersIDs      string         `meddler:"syncers_id"`
}

func (r *syncStatusRow) ToSyncSegment() (mdrtypes.SyncSegment, error) {
	targetToBlock, err := aggkittypes.NewBlockNumberFinality(r.TargetToBlock)
	if err != nil {
		return mdrtypes.SyncSegment{}, fmt.Errorf("ToSyncSegment: error parsing target to block finality (%s): %w",
			r.TargetToBlock, err)
	}
	var blockRange aggkitcommon.BlockRange

	if r.SyncedFromBlock == 0 && r.SyncedToBlock == 0 {
		// We use value {0,0} to represent empty range in the database, but in the code
		// we want to use the IsEmpty() method of BlockRange
		blockRange = aggkitcommon.BlockRangeZero
	} else {
		blockRange = aggkitcommon.NewBlockRange(r.SyncedFromBlock, r.SyncedToBlock)
	}
	return mdrtypes.SyncSegment{
		ContractAddr:  r.Address,
		TargetToBlock: *targetToBlock,
		BlockRange:    blockRange,
	}, nil
}

func (a *MultidownloaderStorage) GetSyncedBlockRangePerContract(tx dbtypes.Querier) (mdrtypes.SetSyncSegment, error) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	result := make([]*syncStatusRow, 0)
	if tx == nil {
		tx = a.db
	}
	err := meddler.QueryAll(tx, &result, "SELECT * FROM sync_status")
	if err != nil {
		return mdrtypes.SetSyncSegment{}, fmt.Errorf("error querying sync status: %w", err)
	}
	setSegments := mdrtypes.NewSetSyncSegment()
	for _, row := range result {
		segment, err := row.ToSyncSegment()
		if err != nil {
			return mdrtypes.SetSyncSegment{},
				fmt.Errorf("GetSyncedBlockRangePerContract: error converting row to sync segment: %w", err)
		}
		setSegments.Add(segment)
	}
	return setSegments, nil
}

func (a *MultidownloaderStorage) UpdateSyncedStatus(tx dbtypes.Querier,
	segments []mdrtypes.SyncSegment) error {
	if tx == nil {
		tx = a.db
	}
	query := `
	UPDATE sync_status SET
		synced_from_block = ?,
		synced_to_block = ?
	WHERE contract_address = ?;
	`
	a.mutex.Lock()
	defer a.mutex.Unlock()
	for _, segment := range segments {
		if !segment.IsValid() {
			return fmt.Errorf("UpdateSyncedStatus: invalid segment %s", segment.String())
		}
		br := segment.BlockRange
		if br.IsEmpty() {
			// We use value {0,0} to represent empty range in the database
			br = aggkitcommon.BlockRangeZero
		}
		result, err := tx.Exec(query, br.FromBlock,
			br.ToBlock, segment.ContractAddr.Hex())
		if err != nil {
			return fmt.Errorf("error updating %s sync status: %w", segment.String(), err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("error getting rows affected for contract %s: %w",
				segment.ContractAddr.Hex(), err)
		}
		if rowsAffected == 0 {
			return fmt.Errorf("no rows updated for contract %s", segment.ContractAddr.Hex())
		}
	}
	return nil
}

func (a *MultidownloaderStorage) UpsertSyncerConfigs(tx dbtypes.Querier, configs []mdrtypes.ContractConfig) error {
	if tx == nil {
		tx = a.db
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	for _, config := range configs {
		row := syncStatusRow{
			Address:         config.Address,
			TargetFromBlock: config.FromBlock,
			TargetToBlock:   config.ToBlock.String(),
			SyncedFromBlock: 0,
			SyncedToBlock:   0,
			SyncersIDs:      fmt.Sprintf("%v", config.Syncers),
		}
		// Upsert logic
		query := `
		INSERT INTO sync_status (contract_address, target_from_block, 
		     target_to_block, synced_from_block, synced_to_block, syncers_id)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(contract_address) DO UPDATE SET
			target_from_block = excluded.target_from_block,
			target_to_block = excluded.target_to_block,
			syncers_id = excluded.syncers_id
		`
		_, err := tx.Exec(query, row.Address.Hex(), row.TargetFromBlock, row.TargetToBlock,
			row.SyncedFromBlock, row.SyncedToBlock, row.SyncersIDs)
		if err != nil {
			return fmt.Errorf("error updating sync status: %w", err)
		}
	}
	return nil
}
