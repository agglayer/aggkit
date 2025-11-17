package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/multidownloader/storage/migrations"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/jmoiron/sqlx"
	"github.com/russross/meddler"
)

type MultidownloaderStorageConfig struct {
	DBPath string
}

type MultidownloaderStorage struct {
	dbtypes.KeyValueStorager
	logger aggkitcommon.Logger
	db     *sql.DB
	cfg    MultidownloaderStorageConfig

	mutex sync.RWMutex
}

type logRow struct {
	Address     common.Address `meddler:"address,address"`
	Topics      string         `meddler:"topics"`
	Data        []byte         `meddler: "data"`
	BlockNumber uint64         `meddler:"block_number"`
	TxHash      common.Hash    `meddler:"tx_hash,hash"`
	TxIndex     uint           `meddler:"tx_index"`
	Index       uint           `meddler:"log_index"`
}

func (l *logRow) String() string {
	if l == nil {
		return "logRow{nil}"
	}
	return fmt.Sprintf("logRow{Address: %s, Topics: %s, DataLen: %d, BlockNumber: %d, "+
		"TxHash: %s, TxIndex: %d, Index: %d}",
		l.Address.Hex(), l.Topics, len(l.Data), l.BlockNumber, l.TxHash.Hex(), l.TxIndex, l.Index)
}

func NewLogRowsFromEthLogs(logs []types.Log) []*logRow {
	rows := make([]*logRow, 0, len(logs))
	for _, log := range logs {
		row := NewLogRowFromEthLog(log)
		rows = append(rows, row)
	}
	return rows
}

type syncStatusRow struct {
	Address         common.Address `meddler:"contract_address,address"`
	TargetFromBlock uint64         `meddler:"target_from_block"`
	TargetToBlock   string         `meddler:"target_to_block"`
	SyncedFromBlock uint64         `meddler:"synced_from_block"`
	SyncedToBlock   uint64         `meddler:"synced_to_block"`
	SyncersIDs      string         `meddler:"syncers_id"`
}

func NewLogRowFromEthLog(log types.Log) *logRow {
	topicsJSON, err := json.Marshal(log.Topics)
	if err != nil {
		// If marshaling fails, fallback to empty string or handle error as needed
		topicsJSON = []byte("[]")
	}
	return &logRow{
		Address:     log.Address,
		Topics:      string(topicsJSON),
		Data:        log.Data,
		BlockNumber: log.BlockNumber,
		TxHash:      log.TxHash,
		TxIndex:     log.TxIndex,
		Index:       log.Index,
	}
}

const SqliteBoolTrue = 1
const SqliteBoolFalse = 0

type BlockRow struct {
	BlockNumber    uint64      `meddler:"block_number"`
	BlockHash      common.Hash `meddler:"block_hash,hash"`
	BlockTimestamp uint64      `meddler:"block_timestamp"`
	// BlockParentHash can be nil (the ethLogs doesn't include it)
	BlockParentHash *common.Hash `meddler:"block_parent_hash,hash"`
	IsFinal         bool         `meddler:"is_final"`
}

func (br *BlockRow) String() string {
	if br == nil {
		return "<nil>"
	}
	blockParentHashString := func(parentHash *common.Hash) string {
		if parentHash == nil {
			return "<nil>"
		}
		return parentHash.Hex()
	}
	return fmt.Sprintf("BlockRow{BlockNumber: %d, BlockHash: %s, BlockTimestamp: %d, BlockParentHash: %s, IsFinal: %t}",
		br.BlockNumber, br.BlockHash.String(), br.BlockTimestamp, blockParentHashString(br.BlockParentHash), br.IsFinal)
}

func NewBlockRowFromEthLog(log types.Log, isFinal bool) *BlockRow {
	return &BlockRow{
		BlockNumber:     log.BlockNumber,
		BlockHash:       log.BlockHash,
		BlockTimestamp:  log.BlockTimestamp,
		BlockParentHash: nil,
		IsFinal:         isFinal,
	}
}

func newBlockRowFromAggkitBlock(block *aggkittypes.BlockHeader, isFinal bool) *BlockRow {
	return &BlockRow{
		BlockNumber:     block.Number,
		BlockHash:       block.Hash,
		BlockTimestamp:  block.Time,
		BlockParentHash: block.ParentHash,
		IsFinal:         isFinal,
	}
}

func NewBlockRowsFromLogs(logs []types.Log, isFinal bool) map[uint64]*BlockRow {
	blockMap := make(map[uint64]*BlockRow)
	for _, log := range logs {
		if _, exists := blockMap[log.BlockNumber]; !exists {
			blockMap[log.BlockNumber] = NewBlockRowFromEthLog(log, isFinal)
		}
	}
	return blockMap
}

func NewBlockRowsFromAggkitBlock(blockHeaders []*aggkittypes.BlockHeader, isFinal bool) map[uint64]*BlockRow {
	blockMap := make(map[uint64]*BlockRow)
	for _, header := range blockHeaders {
		blockMap[header.Number] = newBlockRowFromAggkitBlock(header, isFinal)
	}
	return blockMap
}

func NewMultidownloaderStorage(logger aggkitcommon.Logger,
	cfg MultidownloaderStorageConfig) (*MultidownloaderStorage, error) {
	database, err := db.NewSQLiteDB(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := migrations.RunMigrations(logger, database); err != nil {
		return nil, err
	}

	return &MultidownloaderStorage{
		db:               database,
		logger:           logger,
		cfg:              cfg,
		KeyValueStorager: db.NewKeyValueStorage(database),
	}, nil
}

func (a *MultidownloaderStorage) NewTx(ctx context.Context) (dbtypes.Txer, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	tx, err := db.NewTx(ctx, a.db)
	if err != nil {
		return nil, fmt.Errorf("MultidownloaderStorage.NewTx. Error starting transaction: %w", err)
	}
	return tx, nil
}

type logAndBlockRow struct {
	Address         common.Address `meddler:"address,address"`
	Topics          string         `meddler:"topics"`
	Data            []byte         `meddler:"data"`
	BlockNumber     uint64         `meddler:"block_number"`
	TxHash          common.Hash    `meddler:"tx_hash,hash"`
	TxIndex         uint           `meddler:"tx_index"`
	Index           uint           `meddler:"log_index"`
	BlockHash       common.Hash    `meddler:"block_hash,hash"`
	BlockTimestamp  uint64         `meddler:"block_timestamp"`
	BlockParentHash *common.Hash   `meddler:"block_parent_hash,hash"`
}

func (a *MultidownloaderStorage) GetEthLogs(tx dbtypes.Querier, query mdrtypes.LogQuery) ([]types.Log, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	logs := make([]types.Log, 0)
	dbRows := make([]*logAndBlockRow, 0)
	sqlQuery := `
	SELECT * FROM logs
	LEFT JOIN block ON logs.block_number = block.block_number
	WHERE address IN (?)
	AND logs.block_number>=? AND logs.block_number<=?
	ORDER BY logs.block_number ASC, log_index ASC
	`
	addrs := []string{}
	for _, addr := range query.Addrs {
		addrs = append(addrs, addr.Hex())
	}
	// This is used to extend the address slice into the query
	queryStr, args, err := sqlx.In(sqlQuery, addrs, query.BlockRange.FromBlock, query.BlockRange.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("error building SQL query: %w", err)
	}
	err = meddler.QueryAll(tx, &dbRows, queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("error querying eth logs: %w", err)
	}
	for _, dbRow := range dbRows {
		var topics []common.Hash
		if err := json.Unmarshal([]byte(dbRow.Topics), &topics); err != nil {
			return nil, fmt.Errorf("error unmarshaling topics: %w", err)
		}
		log := types.Log{
			Address:        dbRow.Address,
			Topics:         topics,
			Data:           dbRow.Data,
			BlockNumber:    dbRow.BlockNumber,
			TxHash:         dbRow.TxHash,
			TxIndex:        dbRow.TxIndex,
			Index:          dbRow.Index,
			BlockHash:      dbRow.BlockHash,
			BlockTimestamp: dbRow.BlockTimestamp,
		}
		logs = append(logs, log)
	}
	return logs, nil
}

// tx dbtypes.Txer
func (a *MultidownloaderStorage) SaveEthLogs(tx dbtypes.Querier, logs []types.Log, isFinal bool) error {
	return a.saveLogsAndBlocks(tx, NewBlockRowsFromLogs(logs, isFinal), NewLogRowsFromEthLogs(logs))
}

func (a *MultidownloaderStorage) SaveEthLogsWithHeaders(tx dbtypes.Querier,
	blockHeaders []*aggkittypes.BlockHeader, logs []types.Log, isFinal bool) error {
	return a.saveLogsAndBlocks(tx, NewBlockRowsFromAggkitBlock(blockHeaders, isFinal), NewLogRowsFromEthLogs(logs))
}

func (a *MultidownloaderStorage) saveLogsAndBlocks(tx dbtypes.Querier,
	blockRows map[uint64]*BlockRow, logRows []*logRow) error {
	if tx == nil {
		tx = a.db
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	// Save blocks headers
	if err := a.saveBlocksNoMutex(tx, blockRows); err != nil {
		return fmt.Errorf("saveLogsAndBlocks: error saving blocks: %w", err)
	}

	if err := a.saveLogsNoMutex(tx, logRows); err != nil {
		return fmt.Errorf("saveLogsAndBlocks: error saving logs: %w", err)
	}
	// TODO: Sanity check logs match blockHash match with headers
	return nil
}

func (a *MultidownloaderStorage) saveBlocksNoMutex(tx dbtypes.Querier, blockRows map[uint64]*BlockRow) error {
	if tx == nil {
		tx = a.db
	}
	for _, blockRow := range blockRows {
		if err := meddler.Insert(tx, "block", blockRow); err != nil {
			return fmt.Errorf("saveBlocksNoMutex: error inserting block header row (%s): %w", blockRow.String(), err)
		}
	}
	return nil
}

func (a *MultidownloaderStorage) saveLogsNoMutex(tx dbtypes.Querier, logRows []*logRow) error {
	if tx == nil {
		tx = a.db
	}
	for _, log := range logRows {
		if err := meddler.Insert(tx, "logs", log); err != nil {
			return fmt.Errorf("saveLogsNoMutex: error inserting eth log (%s): %w", log.String(), err)
		}
	}
	return nil
}

func (r *syncStatusRow) ToSyncSegment() (mdrtypes.SyncSegment, error) {
	targetToBlock, err := aggkittypes.NewBlockNumberFinality(r.TargetToBlock)
	if err != nil {
		return mdrtypes.SyncSegment{}, fmt.Errorf("ToSyncSegment: error parsing target to block finality (%s): %w",
			r.TargetToBlock, err)
	}
	return mdrtypes.SyncSegment{
		ContractAddr:  r.Address,
		TargetToBlock: targetToBlock,
		BlockRange:    aggkitcommon.NewBlockRange(r.SyncedFromBlock, r.SyncedToBlock),
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
			return mdrtypes.SetSyncSegment{}, fmt.Errorf("GetSyncedBlockRangePerContract: error converting row to sync segment: %w", err)
		}
		setSegments.Add(segment)
	}
	return setSegments, nil
}

// UpdateSyncingStatus updates the syncing status after executing a log query
func (a *MultidownloaderStorage) UpdateSyncingStatus(tx dbtypes.Querier, logQuery *mdrtypes.LogQuery) error {
	if tx == nil {
		tx = a.db
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	// This set synced_from_block to first query if zero or if it's lower than current
	from := logQuery.BlockRange.FromBlock
	for _, addr := range logQuery.Addrs {
		query := `
		UPDATE sync_status SET
			synced_from_block = CASE
            WHEN synced_from_block = 0 THEN ?
            WHEN ? < synced_from_block THEN ?
            ELSE synced_from_block
        END,
			synced_to_block = MAX(synced_to_block, ?)
		WHERE contract_address = ?;
		`
		result, err := tx.Exec(query, from, from, from, logQuery.BlockRange.ToBlock, addr.Hex())
		if err != nil {
			return fmt.Errorf("error updating sync status: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("error getting rows affected when updating sync status: %w", err)
		}
		if rowsAffected == 0 {
			return fmt.Errorf("no rows affected when updating sync status for contract %s", addr.Hex())
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
