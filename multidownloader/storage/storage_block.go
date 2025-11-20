package storage

import (
	"database/sql"
	"errors"
	"fmt"

	dbtypes "github.com/agglayer/aggkit/db/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/jmoiron/sqlx"
	"github.com/russross/meddler"
)

type Blocks struct {
	Headers  map[uint64]*aggkittypes.BlockHeader
	AreFinal map[uint64]bool
}

func NewBlocks() Blocks {
	return Blocks{
		Headers:  make(map[uint64]*aggkittypes.BlockHeader),
		AreFinal: make(map[uint64]bool),
	}
}

func (b *Blocks) Add(header *aggkittypes.BlockHeader, isFinal bool) {
	b.Headers[header.Number] = header
	b.AreFinal[header.Number] = isFinal
}

func (b *Blocks) Get(number uint64) (*aggkittypes.BlockHeader, bool, error) {
	header, exists := b.Headers[number]
	if !exists {
		return nil, false, fmt.Errorf("db.blocks.header: block header not found for number %d", number)
	}
	isFinal, exists := b.AreFinal[number]
	if !exists {
		return nil, false, fmt.Errorf("db.blocks.header: block finality not found for number %d", number)
	}
	return header, isFinal, nil
}

func (b *Blocks) ListHeaders() []*aggkittypes.BlockHeader {
	headers := make([]*aggkittypes.BlockHeader, 0, len(b.Headers))
	for _, header := range b.Headers {
		headers = append(headers, header)
	}
	return headers
}

func (b *Blocks) IsEmpty() bool {
	return len(b.Headers) == 0
}

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

	query := "UPDATE blocks SET is_final = 1 WHERE block_number IN (?)"
	queryStr, args, err := sqlx.In(query, blockNumbers)
	if err != nil {
		return fmt.Errorf("error building SQL query: %w", err)
	}

	_, err = tx.Exec(queryStr, args...)
	if err != nil {
		return fmt.Errorf("UpdateIsFinal: error updating block bases: %w", err)
	}
	return nil
}
func (a *MultidownloaderStorage) GetBlockHeaderByNumber(tx dbtypes.Querier,
	blockNumber uint64) (*aggkittypes.BlockHeader, bool, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	blocks, err := a.getBlockHeadersNoMutex(tx, "SELECT * FROM blocks WHERE block_number = ?", blockNumber)
	if err != nil {
		return nil, false, err
	}
	if blocks.IsEmpty() {
		return nil, false, nil
	}
	header, isFinal, err := blocks.Get(blockNumber)
	if err != nil {
		return nil, false, err
	}
	return header, isFinal, nil
}

func (a *MultidownloaderStorage) GetBlockHeaderNotFinal(tx dbtypes.Querier,
	finalizedBlockNumber uint64) ([]*aggkittypes.BlockHeader, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	blocks, err := a.getBlockHeadersNoMutex(tx, "SELECT * FROM blocks WHERE is_final = 0 AND block_number = ? "+
		"ORDER BY block_number ASC", finalizedBlockNumber)
	return blocks.ListHeaders(), err
}

func (a *MultidownloaderStorage) getBlockHeadersNoMutex(tx dbtypes.Querier,
	query string, args ...interface{}) (Blocks, error) {
	if tx == nil {
		tx = a.db
	}
	result := NewBlocks()
	var blocks []*BlockRow
	err := meddler.QueryAll(tx, &blocks, query, args...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, nil
		}
		return result, fmt.Errorf("GetBlockHeaderByNumber: error querying block by number: %w", err)
	}

	for _, block := range blocks {
		blockResult := &aggkittypes.BlockHeader{
			Number:     block.BlockNumber,
			ParentHash: block.BlockParentHash,
			Time:       block.BlockTimestamp,
			Hash:       block.BlockHash,
		}
		result.Add(blockResult, block.IsFinal)
	}
	return result, nil
}
