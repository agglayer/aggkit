package storage

import (
	"database/sql"
	"fmt"

	dbtypes "github.com/agglayer/aggkit/db/types"
	aggkittypes "github.com/agglayer/aggkit/types"

	"github.com/russross/meddler"
)

func (a *MdrSQLStorage) SaveBlockBase(tx dbtypes.Querier, base *aggkittypes.BlockBase, isFinal bool) error {
	if tx == nil {
		tx = a.db
	}
	exists, err := a.ExistsBlockBase(tx, base.Number)
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
func (a *MdrSQLStorage) ExistsBlockBase(tx dbtypes.Querier, blockNumber uint64) (bool, error) {
	blockBase, err := a.GetBlockBaseByNumber(tx, blockNumber)
	if err != nil {
		return false, fmt.Errorf("error getting block base %d: %w", blockNumber, err)
	}
	if blockBase == nil {
		return false, nil
	}

	return true, nil
}

func (a *MdrSQLStorage) SaveBlockHeader(tx dbtypes.Querier, header *aggkittypes.BlockHeader, isFinal bool) error {
	if tx == nil {
		tx = a.db
	}
	// TODO: Sanity check that hash is the same in table block_base
	exists, err := a.ExistsBlockHeader(tx, header.Number)
	if err != nil {
		return fmt.Errorf("SaveBlockHeader: error checking block header existence: %w", err)
	}
	if !exists {
		a.SaveBlockBase(tx, &header.BlockBase, isFinal)
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

func (a *MdrSQLStorage) GetBlockBaseByNumber(tx dbtypes.Querier, blockNumber uint64) (*aggkittypes.BlockBase, error) {

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
	blockBase, err := a.GetBlockBaseByNumber(tx, blockNumber)
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
