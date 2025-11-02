package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

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

type MdrSQLStorage struct {
	dbtypes.KeyValueStorager
	logger aggkitcommon.Logger
	db     *sql.DB
	cfg    MultidownloaderStorageConfig
}

type logDBRow struct {
	Address     common.Address `meddler:"address,address"`
	Topics      string         `meddler:"topics"`
	Data        []byte         `meddler: "data"`
	BlockNumber uint64         `meddler:"block_number"`
	TxHash      common.Hash    `meddler:"tx_hash,hash"`
	TxIndex     uint           `meddler:"tx_index"`
	Index       uint           `meddler:"log_index"`
}

type syncStatusRow struct {
	Address         common.Address `meddler:"contract_address,address"`
	TargetFromBlock uint64         `meddler:"target_from_block"`
	TargetToBlock   string         `meddler:"target_to_block"`
	SyncedFromBlock uint64         `meddler:"synced_from_block"`
	SyncedToBlock   uint64         `meddler:"synced_to_block"`
	SyncersIDs      string         `meddler:"syncers_id"`
}

func NewLogDBRowFromEthLog(log types.Log) *logDBRow {
	topicsJSON, err := json.Marshal(log.Topics)
	if err != nil {
		// If marshaling fails, fallback to empty string or handle error as needed
		topicsJSON = []byte("[]")
	}
	return &logDBRow{
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

type BlockBaseRow struct {
	BlockNumber    uint64      `meddler:"block_number"`
	BlockHash      common.Hash `meddler:"block_hash,hash"`
	BlockTimestamp uint64      `meddler:"block_timestamp"`
	IsFinal        bool        `meddler:"is_final"`
}

type BlockHeaderRow struct {
	BlockNumber uint64 `meddler:"block_number"`
	// `meddler:"tx_hash,hash"`
	BlockParentHash common.Hash `meddler:"block_parent_hash,hash"`
}

func NewBlockRowFromEthLog(log types.Log, isFinal bool) *BlockBaseRow {
	return &BlockBaseRow{
		BlockNumber:    log.BlockNumber,
		BlockHash:      log.BlockHash,
		BlockTimestamp: log.BlockTimestamp,
		IsFinal:        isFinal,
	}
}

func NewMdrSQLStorage(logger aggkitcommon.Logger, cfg MultidownloaderStorageConfig) (*MdrSQLStorage, error) {
	database, err := db.NewSQLiteDB(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := migrations.RunMigrations(logger, database); err != nil {
		return nil, err
	}

	return &MdrSQLStorage{
		db:               database,
		logger:           logger,
		cfg:              cfg,
		KeyValueStorager: db.NewKeyValueStorage(database),
	}, nil
}

func (a *MdrSQLStorage) NewTx(ctx context.Context) (dbtypes.Txer, error) {
	tx, err := db.NewTx(ctx, a.db)
	if err != nil {
		return nil, fmt.Errorf("error starting transaction: %w", err)
	}
	return tx, nil
}

func (a *MdrSQLStorage) ExistsBlockHeader(tx dbtypes.Querier, blockNumber uint64) (bool, error) {
	var count int
	query := "SELECT COUNT(1) FROM block_header WHERE block_number = ?"
	row := tx.QueryRow(query, blockNumber)
	if err := row.Scan(&count); err != nil {
		return false, fmt.Errorf("error checking block existence: %w", err)
	}
	return count > 0, nil
}

type logAndBlockRow struct {
	Address        common.Address `meddler:"address,address"`
	Topics         string         `meddler:"topics"`
	Data           []byte         `meddler:"data"`
	BlockNumber    uint64         `meddler:"block_number"`
	TxHash         common.Hash    `meddler:"tx_hash,hash"`
	TxIndex        uint           `meddler:"tx_index"`
	Index          uint           `meddler:"log_index"`
	BlockHash      common.Hash    `meddler:"block_hash,hash"`
	BlockTimestamp uint64         `meddler:"block_timestamp"`
}

func (a *MdrSQLStorage) GetEthLogs(tx dbtypes.Querier, query mdrtypes.LogQuery) ([]types.Log, error) {
	if tx == nil {
		tx = a.db
	}

	logs := make([]types.Log, 0)
	dbRows := make([]*logAndBlockRow, 0)
	sqlQuery := `
	SELECT * FROM logs
	LEFT JOIN block_base ON logs.block_number = block_base.block_number
	WHERE address IN (?)
	AND logs.block_number>=? AND logs.block_number<=?
	ORDER BY logs.block_number ASC, log_index ASC
	`
	addrs := []string{}
	for _, addr := range query.Addrs {
		addrs = append(addrs, addr.Hex())
	}
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
func (a *MdrSQLStorage) SaveEthLogs(tx dbtypes.Querier, logs []types.Log, isFinal bool) error {
	if tx == nil {
		tx = a.db
	}

	for _, log := range logs {
		// TODO: this don't work in all cases use INSERT OR IGNORE"
		exists, err := a.ExistsBlockBase(tx, log.BlockNumber)
		if err != nil {
			return fmt.Errorf("error checking block existence: %w", err)
		}
		if !exists {
			block := NewBlockRowFromEthLog(log, isFinal)
			err = a.SaveBlockBase(tx, aggkittypes.NewBlockBase(
				block.BlockNumber,
				block.BlockHash,
				block.BlockTimestamp,
			), isFinal)
			if err != nil {
				return fmt.Errorf("error saving block base: %w", err)
			}
		}

		log := NewLogDBRowFromEthLog(log)
		if err := meddler.Insert(tx, "logs", log); err != nil {
			return fmt.Errorf("error inserting eth log: %w", err)
		}
	}
	return nil
}

func (a *MdrSQLStorage) SaveEthLogsWithHeaders(tx dbtypes.Querier, blockHeaders []*types.Header, logs []types.Log, isFinal bool) error {
	if tx == nil {
		tx = a.db
	}

	// This populate block_base and logs tables
	err := a.SaveEthLogs(tx, logs, false)
	if err != nil {
		return fmt.Errorf("SaveEthLogsWithHeaders: error saving eth logs: %w", err)
	}
	for _, blockHeader := range blockHeaders {
		header := aggkittypes.NewBlockHeader(
			blockHeader.Number.Uint64(),
			blockHeader.Hash(),
			blockHeader.Time,
			blockHeader.ParentHash,
		)
		err := a.SaveBlockHeader(tx, header, isFinal)

		if err != nil {
			return fmt.Errorf("SaveEthLogsWithHeaders: error saving block header [%s]: %w", header.String(), err)
		}
	}
	// TODO: Sanity check logs match blockHash match with headers
	return nil
}

func (a *MdrSQLStorage) SaveUnsafeBlock(tx dbtypes.Querier, block *types.Header, logs []types.Log) error {
	if tx == nil {
		tx = a.db
	}
	blockRow := &BlockBaseRow{
		BlockNumber:    block.Number.Uint64(),
		BlockHash:      block.Hash(),
		BlockTimestamp: block.Time,
		IsFinal:        false,
	}
	if err := meddler.Insert(tx, "block", blockRow); err != nil {
		return fmt.Errorf("SaveUnsafeBlock: error inserting unsafe block: %w", err)
	}
	unsafeBlockRow := &BlockHeaderRow{
		BlockNumber:     block.Number.Uint64(),
		BlockParentHash: block.ParentHash,
	}
	if err := meddler.Insert(tx, "block_unsafe", unsafeBlockRow); err != nil {
		return fmt.Errorf("SaveUnsafeBlock: error inserting unsafe block row: %w", err)
	}
	for _, log := range logs {
		if log.BlockHash != block.Hash() {
			return fmt.Errorf("SaveUnsafeBlock: log block hash %s does not match header block hash %s",
				log.BlockHash.Hex(), block.Hash().Hex())
		}
		log := NewLogDBRowFromEthLog(log)
		if err := meddler.Insert(tx, "logs", log); err != nil {
			return fmt.Errorf("SaveUnsafeBlock: error inserting eth log: %w", err)
		}
	}
	return nil
}

func (r *syncStatusRow) ToSyncSegment() mdrtypes.SyncSegment {
	return mdrtypes.SyncSegment{
		ContractAddr: r.Address,
		BlockRange:   aggkitcommon.NewBlockRange(r.SyncedFromBlock, r.SyncedToBlock),
	}
}

func (a *MdrSQLStorage) GetSyncedBlockRangePerContract(tx dbtypes.Querier) (mdrtypes.SetSyncSegment, error) {
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
		setSegments.Add(row.ToSyncSegment())
	}
	return setSegments, nil
}

func (a *MdrSQLStorage) UpdateSyncingStatus(tx dbtypes.Querier, logQuery *mdrtypes.LogQuery) error {
	if tx == nil {
		tx = a.db
	}
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
		_, err := tx.Exec(query, from, from, from, logQuery.BlockRange.ToBlock, addr.Hex())
		if err != nil {
			return fmt.Errorf("error updating sync status: %w", err)
		}
	}
	return nil
}

func (a *MdrSQLStorage) UpdateSyncerConfigs(tx dbtypes.Querier, configs []mdrtypes.ContractConfig) error {
	if tx == nil {
		tx = a.db
	}
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
		INSERT INTO sync_status (contract_address, target_from_block, target_to_block, synced_from_block, synced_to_block, syncers_id)
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
