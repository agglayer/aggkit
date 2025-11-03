package storage

import (
	"database/sql"
	"fmt"

	dbtypes "github.com/agglayer/aggkit/db/types"
	aggkittypes "github.com/agglayer/aggkit/types"

	"github.com/russross/meddler"
)

func (a *MdrSQLStorage) SaveBlockBase(tx dbtypes.Querier, base *aggkittypes.BlockBase, isFinal bool) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.saveBlockBaseNoMutex(tx, base, isFinal)
}

// saveBlockBaseNoMutex saves a BlockBase without acquiring the mutex (it must be held by the caller)
func (a *MdrSQLStorage) saveBlockBaseNoMutex(tx dbtypes.Querier, base *aggkittypes.BlockBase, isFinal bool) error {
	if tx == nil {
		tx = a.db
	}
	exists, err := a.existsBlockBaseNoMutex(tx, base.Number)
	if err != nil {
		return fmt.Errorf("SaveBlockBase: error checking block base existence: %w", err)
	}
	if exists {
		return nil
	}
	blockBaseRow := &BlockBaseRow{
		BlockNumber:    base.Number,
		BlockHash:      base.Hash,
		BlockTimestamp: base.Time,
		IsFinal:        isFinal,
	}
	if err := meddler.Insert(tx, "block_base", blockBaseRow); err != nil {
		return fmt.Errorf("SaveBlockBase: error inserting block base row: %w", err)
	}
	return nil
}

func (a *MdrSQLStorage) existsBlockBaseNoMutex(tx dbtypes.Querier, blockNumber uint64) (bool, error) {
	blockBase, err := a.getBlockBaseByNumberNoMutex(tx, blockNumber)
	if err != nil {
		return false, fmt.Errorf("error getting block base %d: %w", blockNumber, err)
	}
	if blockBase == nil {
		return false, nil
	}

	return true, nil
}

func (a *MdrSQLStorage) SaveBlockHeader(tx dbtypes.Querier, header *aggkittypes.BlockHeader, isFinal bool) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.saveBlockHeaderNoMutex(tx, header, isFinal)

}

func (a *MdrSQLStorage) ExistsBlockHeader(tx dbtypes.Querier, blockNumber uint64) (bool, error) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.existsBlockHeaderNoMutex(tx, blockNumber)
}

func (a *MdrSQLStorage) existsBlockHeaderNoMutex(tx dbtypes.Querier, blockNumber uint64) (bool, error) {
	var count int
	query := "SELECT COUNT(1) FROM block_header WHERE block_number = ?"
	row := tx.QueryRow(query, blockNumber)
	if err := row.Scan(&count); err != nil {
		return false, fmt.Errorf("error checking block existence: %w", err)
	}
	return count > 0, nil
}

// saveBlockHeaderNoMutex saves a BlockHeader without acquiring the mutex (it must be held by the caller)
func (a *MdrSQLStorage) saveBlockHeaderNoMutex(tx dbtypes.Querier, header *aggkittypes.BlockHeader, isFinal bool) error {
	if tx == nil {
		tx = a.db
	}
	// TODO: Sanity check that hash is the same in table block_base
	exists, err := a.existsBlockHeaderNoMutex(tx, header.Number)
	if err != nil {
		return fmt.Errorf("SaveBlockHeader: error checking block header existence: %w", err)
	}
	if !exists {
		a.saveBlockBaseNoMutex(tx, &header.BlockBase, isFinal)
	}
	blockHeaderRow := &BlockHeaderRow{
		BlockNumber:     header.Number,
		BlockParentHash: header.Hash,
	}
	if err := meddler.Insert(tx, "block_header", blockHeaderRow); err != nil {
		return fmt.Errorf("SaveBlockHeader: error inserting block header row: %w", err)
	}
	return nil
}

func (a *MdrSQLStorage) getBlockBaseByNumberNoMutex(tx dbtypes.Querier, blockNumber uint64) (*aggkittypes.BlockBase, error) {

	if tx == nil {
		tx = a.db
	}
	var block BlockBaseRow
	err := meddler.QueryRow(tx, &block, "SELECT * FROM block_base WHERE block_number = ?", blockNumber)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("GetBlockByNumber: error querying block by number: %w", err)
	}
	blockBase := aggkittypes.NewBlockBase(
		block.BlockNumber,
		block.BlockHash,
		block.BlockTimestamp,
	)
	return blockBase, nil
}

func (a *MdrSQLStorage) GetBlockHeaderByNumber(tx dbtypes.Querier, blockNumber uint64) (*aggkittypes.BlockHeader, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	blockBase, err := a.getBlockBaseByNumberNoMutex(tx, blockNumber)
	if err != nil {
		return nil, fmt.Errorf("GetBlockHeaderByNumber: error getting block base: %w", err)
	}
	if blockBase == nil {
		return nil, nil
	}
	var block BlockHeaderRow
	err = meddler.QueryRow(tx, &block, "SELECT * FROM block_header WHERE block_number = ?", blockNumber)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("GetBlockByNumber: error querying block by number: %w", err)
	}

	return aggkittypes.NewBlockHeaderFromBase(blockBase, block.BlockParentHash), nil
}
