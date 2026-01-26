package storage

import (
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	dbtypes "github.com/agglayer/aggkit/db/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
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
