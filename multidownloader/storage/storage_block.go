package storage

import (
	"database/sql"
	"fmt"

	dbtypes "github.com/agglayer/aggkit/db/types"
	aggkittypes "github.com/agglayer/aggkit/types"

	"github.com/russross/meddler"
)

func (a *MultidownloaderStorage) SaveBlockAggkitBlock(tx dbtypes.Querier, header *aggkittypes.BlockHeader, isFinal bool) error {
	blockRows := map[uint64]*BlockRow{
		header.Number: newBlockRowFromAggkitBlock(header, isFinal),
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.saveBlocksNoMutex(tx, blockRows)
}

func (a *MultidownloaderStorage) UpdateIsFinal(tx dbtypes.Querier, blockNumbers []uint64) error {
	if tx == nil {
		tx = a.db
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	query := "UPDATE block SET is_final = 1 WHERE block_number IN (?)"
	_, err := tx.Exec(query, blockNumbers)
	if err != nil {
		return fmt.Errorf("UpdateIsFinal: error updating block bases: %w", err)
	}
	return nil
}
func (a *MultidownloaderStorage) GetBlockHeaderByNumber(tx dbtypes.Querier, blockNumber uint64) (*aggkittypes.BlockHeader, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	blocks, err := a.getBlockHeadersNoMutex(tx, "SELECT * FROM block WHERE block_number = ?", blockNumber)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, nil
	}
	return blocks[0], nil
}

func (a *MultidownloaderStorage) GetBlockHeaderNotFinal(tx dbtypes.Querier, finalizedBlockNumber uint64) ([]*aggkittypes.BlockHeader, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.getBlockHeadersNoMutex(tx, "SELECT * FROM block WHERE is_final = 0 AND block_number = ? ORDER BY block_number ASC", finalizedBlockNumber)
}

func (a *MultidownloaderStorage) getBlockHeadersNoMutex(tx dbtypes.Querier, query string, args ...interface{}) ([]*aggkittypes.BlockHeader, error) {
	if tx == nil {
		tx = a.db
	}
	var blocks []*BlockRow
	err := meddler.QueryAll(tx, &blocks, query, args...)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("GetBlockHeaderByNumber: error querying block by number: %w", err)
	}
	result := make([]*aggkittypes.BlockHeader, 0, len(blocks))

	for _, block := range blocks {
		blockResult := &aggkittypes.BlockHeader{
			Number:     block.BlockNumber,
			ParentHash: block.BlockParentHash,
			Time:       block.BlockTimestamp,
			Hash:       block.BlockHash,
		}
		result = append(result, blockResult)
	}
	return result, nil
}

/*
// saveBlockBaseNoMutex saves a BlockBase without acquiring the mutex (it must be held by the caller)
func (a *MultidownloaderStorage) saveBlockBaseNoMutex(tx dbtypes.Querier, base *aggkittypes.BlockBase, isFinal bool) error {
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

func (a *MultidownloaderStorage) SaveBlockHeaders(tx dbtypes.Querier, headers map[uint64]*aggkittypes.BlockHeader, finalBlockNumber uint64) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	for _, header := range headers {
		isFinal := header.Number <= finalBlockNumber
		if err := a.saveBlockHeaderNoMutex(tx, header, isFinal); err != nil {
			return fmt.Errorf("SaveBlockHeaders: error saving block header %d: %w", header.Number, err)
		}
	}
	return nil
}
func (a *MultidownloaderStorage) SaveBlockHeader(tx dbtypes.Querier, header *aggkittypes.BlockHeader, isFinal bool) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.saveBlockHeaderNoMutex(tx, header, isFinal)

}

// saveBlockHeaderNoMutex saves a BlockHeader without acquiring the mutex (it must be held by the caller)
func (a *MultidownloaderStorage) saveBlockHeaderNoMutex(tx dbtypes.Querier, header *aggkittypes.BlockHeader, isFinal bool) error {
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

func (a *MultidownloaderStorage) ExistsBlockHeader(tx dbtypes.Querier, blockNumber uint64) (bool, error) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.existsBlockHeaderNoMutex(tx, blockNumber)
}

func (a *MultidownloaderStorage) existsBlockBaseNoMutex(tx dbtypes.Querier, blockNumber uint64) (bool, error) {
	blockBase, err := a.getBlockBaseByNumberNoMutex(tx, blockNumber)
	if err != nil {
		return false, fmt.Errorf("error getting block base %d: %w", blockNumber, err)
	}
	if blockBase == nil {
		return false, nil
	}

	return true, nil
}

func (a *MultidownloaderStorage) existsBlockHeaderNoMutex(tx dbtypes.Querier, blockNumber uint64) (bool, error) {
	var count int
	query := "SELECT COUNT(1) FROM block_header WHERE block_number = ?"
	row := tx.QueryRow(query, blockNumber)
	if err := row.Scan(&count); err != nil {
		return false, fmt.Errorf("error checking block existence: %w", err)
	}
	return count > 0, nil
}

// GetBlockBaseUnsafes retrieves all non-final block bases up to the specified finalized block number.
func (a *MultidownloaderStorage) GetBlockBaseUnsafes(tx dbtypes.Querier, finalizedBlockNumber uint64) ([]*aggkittypes.BlockBase, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	var blockBases []*aggkittypes.BlockBase
	query := "SELECT * FROM block_base WHERE is_final = 0 AND block_number <= ? ORDER BY block_number ASC"
	err := meddler.QueryAll(tx, &blockBases, query, finalizedBlockNumber)
	if err != nil {
		return nil, fmt.Errorf("GetBlockBaseUnsafes: error querying block bases: %w", err)
	}

	return blockBases, nil
}

func (a *MultidownloaderStorage) getBlockBaseByNumberNoMutex(tx dbtypes.Querier, blockNumber uint64) (*aggkittypes.BlockBase, error) {

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
*/
