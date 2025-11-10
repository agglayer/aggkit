package storage

import (
	"database/sql"
	"errors"
	"fmt"

	dbtypes "github.com/agglayer/aggkit/db/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/russross/meddler"
)

func (a *MultidownloaderStorage) SaveBlockAggkitBlock(tx dbtypes.Querier,
	header *aggkittypes.BlockHeader, isFinal bool) error {
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
func (a *MultidownloaderStorage) GetBlockHeaderByNumber(tx dbtypes.Querier,
	blockNumber uint64) (*aggkittypes.BlockHeader, error) {
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

func (a *MultidownloaderStorage) GetBlockHeaderNotFinal(tx dbtypes.Querier,
	finalizedBlockNumber uint64) ([]*aggkittypes.BlockHeader, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.getBlockHeadersNoMutex(tx, "SELECT * FROM block WHERE is_final = 0 AND block_number = ? "+
		"ORDER BY block_number ASC", finalizedBlockNumber)
}

func (a *MultidownloaderStorage) getBlockHeadersNoMutex(tx dbtypes.Querier,
	query string, args ...interface{}) ([]*aggkittypes.BlockHeader, error) {
	if tx == nil {
		tx = a.db
	}
	var blocks []*BlockRow
	err := meddler.QueryAll(tx, &blocks, query, args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
