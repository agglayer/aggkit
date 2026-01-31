package storage

import (
	"database/sql"
	"errors"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	dbtypes "github.com/agglayer/aggkit/db/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

type reorgRow struct {
	ChainID                   uint64 `meddler:"chain_id"`
	DetectedAtBlock           uint64 `meddler:"detected_at_block"`
	ReorgedFromBlock          uint64 `meddler:"reorged_from_block"`
	ReorgedToBlock            uint64 `meddler:"reorged_to_block"`
	DetectedTimestamp         uint64 `meddler:"detected_timestamp"`
	NetworkLatestBlock        uint64 `meddler:"network_latest_block"`
	NetworkFinalizedBlock     uint64 `meddler:"network_finalized_block"`
	NetworkFinalizedBlockName string `meddler:"network_finalized_block_name"`
}

func newReorgRowFromReorgData(reorgData mdrtypes.ReorgData) *reorgRow {
	return &reorgRow{
		ChainID:                   reorgData.ChainID,
		DetectedAtBlock:           reorgData.DetectedAtBlock,
		ReorgedFromBlock:          reorgData.BlockRangeAffected.FromBlock,
		ReorgedToBlock:            reorgData.BlockRangeAffected.ToBlock,
		DetectedTimestamp:         reorgData.DetectedTimestamp,
		NetworkLatestBlock:        reorgData.NetworkLatestBlock,
		NetworkFinalizedBlock:     reorgData.NetworkFinalizedBlock,
		NetworkFinalizedBlockName: reorgData.NetworkFinalizedBlockName.String(),
	}
}

// returns ChainID of the inserted reorg
func (a *MultidownloaderStorage) InsertReorgAndMoveReorgedBlocksAndLogs(tx dbtypes.Querier,
	reorgData mdrtypes.ReorgData) (uint64, error) {
	if tx == nil {
		return 0, fmt.Errorf("InsertNewReorg: require a tx because it done multiples operations")
	}
	reorgRow := newReorgRowFromReorgData(reorgData)
	a.mutex.Lock()
	defer a.mutex.Unlock()
	// Get Next ChainID from storage using rowid
	lastChainID := struct {
		ChainID *uint64 `meddler:"chain_id"`
	}{}
	err := meddler.QueryRow(tx, &lastChainID, "SELECT MAX(chain_id) as chain_id FROM reorgs")
	if err != nil {
		return 0, fmt.Errorf("InsertNewReorg: error getting last chain_id: %w", err)
	}
	if lastChainID.ChainID == nil {
		reorgRow.ChainID = 1
	} else {
		reorgRow.ChainID = *lastChainID.ChainID + 1
	}

	if err := meddler.Insert(tx, "reorgs", reorgRow); err != nil {
		return 0, fmt.Errorf("InsertNewReorg: error inserting reorgs (%s): %w", reorgData.String(), err)
	}
	if err := a.moveReorgedBlocksAndLogsNoMutex(tx, reorgRow.ChainID,
		reorgData.BlockRangeAffected); err != nil {
		return 0, fmt.Errorf("InsertNewReorg: error moving reorged blocks to block_reorged: %w", err)
	}
	// Adjust sync_status table to reflect the reorg
	err = a.adjustSyncStatusForReorgNoMutex(tx, reorgData)
	if err != nil {
		return 0, fmt.Errorf("InsertNewReorg: error adjusting sync_status for reorg: %w", err)
	}
	return reorgRow.ChainID, nil
}

func (a *MultidownloaderStorage) moveReorgedBlocksAndLogsNoMutex(tx dbtypes.Querier, chainID uint64,
	blockRangeAffected aggkitcommon.BlockRange) error {
	a.logger.Debugf("storage: moving blocks to blocks_reorged - chain_id: %d, range: %s",
		chainID, blockRangeAffected.String())
	query := `INSERT INTO blocks_reorged (chain_id, block_number, block_hash,block_parent_hash, block_timestamp)
	SELECT ?, block_number, block_hash, block_parent_hash, block_timestamp
	FROM blocks
	WHERE block_number >= ? AND block_number <= ?;
	INSERT INTO logs_reorged (chain_id, block_number, address,topics, data, tx_hash, tx_index, log_index)
	SELECT ?, block_number, address, topics, data, tx_hash, tx_index, log_index
	FROM logs
	WHERE block_number >= ? AND block_number <= ?;
	DELETE FROM logs
	WHERE block_number >= ? AND block_number <= ?;
	DELETE FROM blocks
	WHERE block_number >= ? AND block_number <= ?;`
	_, err := tx.Exec(query,
		chainID,
		blockRangeAffected.FromBlock, blockRangeAffected.ToBlock,
		chainID,
		blockRangeAffected.FromBlock, blockRangeAffected.ToBlock,
		blockRangeAffected.FromBlock, blockRangeAffected.ToBlock,
		blockRangeAffected.FromBlock, blockRangeAffected.ToBlock)
	if err != nil {
		return fmt.Errorf("moveReorgedBlocks: error moving reorged blocks to block_reorged: %w", err)
	}
	return nil
}

func (a *MultidownloaderStorage) GetBlockReorgedChainID(tx dbtypes.Querier,
	blockNumber uint64, blockHash common.Hash) (uint64, bool, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	var chainIDRow struct {
		ChainID *uint64 `meddler:"chain_id"`
	}
	query := `SELECT chain_id FROM blocks_reorged
	WHERE block_number = ? AND block_hash = ? LIMIT 1;`
	err := tx.QueryRow(query, blockNumber, blockHash.Hex()).Scan(&chainIDRow.ChainID)
	if err != nil {
		return 0, false, fmt.Errorf("GetBlockReorgedChainID: error querying blocks_reorged: %w", err)
	}
	if chainIDRow.ChainID == nil {
		return 0, false, nil
	}
	return *chainIDRow.ChainID, true, nil
}

func (a *MultidownloaderStorage) GetReorgedDataByChainID(tx dbtypes.Querier,
	reorgedChainID uint64) (*mdrtypes.ReorgData, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	var row reorgRow
	query := `SELECT chain_id, detected_at_block, reorged_from_block, reorged_to_block,
		detected_timestamp, network_latest_block, network_finalized_block, network_finalized_block_name
		FROM reorgs WHERE chain_id = ? LIMIT 1;`

	err := meddler.QueryRow(tx, &row, query, reorgedChainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetReorgedDataByChainID: error querying reorgs table: %w", err)
	}

	// Convert string to BlockNumberFinality
	blockFinality, err := aggkittypes.NewBlockNumberFinality(row.NetworkFinalizedBlockName)
	if err != nil {
		return nil, fmt.Errorf("GetReorgedDataByChainID: error parsing NetworkFinalizedBlockName: %w", err)
	}

	reorgData := &mdrtypes.ReorgData{
		ChainID: row.ChainID,
		BlockRangeAffected: aggkitcommon.BlockRange{
			FromBlock: row.ReorgedFromBlock,
			ToBlock:   row.ReorgedToBlock,
		},
		DetectedAtBlock:           row.DetectedAtBlock,
		DetectedTimestamp:         row.DetectedTimestamp,
		NetworkLatestBlock:        row.NetworkLatestBlock,
		NetworkFinalizedBlock:     row.NetworkFinalizedBlock,
		NetworkFinalizedBlockName: *blockFinality,
	}

	return reorgData, nil
}

// AdjustSyncStatusForReorg adjusts the sync_status table after a reorg by setting
// synced_to_block to the block before the reorg started for all affected contracts
func (a *MultidownloaderStorage) adjustSyncStatusForReorgNoMutex(tx dbtypes.Querier,
	reorgData mdrtypes.ReorgData) error {
	if tx == nil {
		return fmt.Errorf("AdjustSyncStatusForReorg: require a tx to ensure atomicity")
	}
	// Calculate the new synced_to_block (one block before the reorg)
	var newSyncedToBlock uint64
	if reorgData.BlockRangeAffected.FromBlock > 0 {
		newSyncedToBlock = reorgData.BlockRangeAffected.FromBlock - 1
	} else {
		newSyncedToBlock = 0
	}

	// Update all contracts that have synced beyond the reorg point
	query := `UPDATE sync_status
		SET synced_to_block = ?
		WHERE synced_to_block >= ?`

	result, err := tx.Exec(query, newSyncedToBlock, reorgData.BlockRangeAffected.FromBlock)
	if err != nil {
		return fmt.Errorf("AdjustSyncStatusForReorg: error updating sync_status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("AdjustSyncStatusForReorg: error getting rows affected: %w", err)
	}

	a.logger.Infof("AdjustSyncStatusForReorg: adjusted %d contract(s) to synced_to_block=%d "+
		"due to reorg at blocks [%d-%d]",
		rowsAffected, newSyncedToBlock,
		reorgData.BlockRangeAffected.FromBlock, reorgData.BlockRangeAffected.ToBlock)

	return nil
}
