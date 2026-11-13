package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	sqlite3 "github.com/mattn/go-sqlite3"
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
	Data        []byte         `meddler:"data"`
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

type blockRow struct {
	BlockNumber    uint64      `meddler:"block_number"`
	BlockHash      common.Hash `meddler:"block_hash,hash"`
	BlockTimestamp uint64      `meddler:"block_timestamp"`
	// BlockParentHash can be nil (the ethLogs doesn't include it)
	BlockParentHash *common.Hash `meddler:"block_parent_hash,hash"`
	IsFinal         bool         `meddler:"is_final"`
}

func (br *blockRow) String() string {
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

func NewBlockRowFromEthLog(log types.Log, isFinal bool) *blockRow {
	return &blockRow{
		BlockNumber:     log.BlockNumber,
		BlockHash:       log.BlockHash,
		BlockTimestamp:  log.BlockTimestamp,
		BlockParentHash: nil,
		IsFinal:         isFinal,
	}
}

func newBlockRowFromAggkitBlock(block *aggkittypes.BlockHeader, isFinal bool) *blockRow {
	return &blockRow{
		BlockNumber:     block.Number,
		BlockHash:       block.Hash,
		BlockTimestamp:  block.Time,
		BlockParentHash: block.ParentHash,
		IsFinal:         isFinal,
	}
}

func NewBlockRowsFromLogs(logs []types.Log, isFinal bool) map[uint64]*blockRow {
	blockMap := make(map[uint64]*blockRow)
	for _, log := range logs {
		if _, exists := blockMap[log.BlockNumber]; !exists {
			blockMap[log.BlockNumber] = NewBlockRowFromEthLog(log, isFinal)
		}
	}
	return blockMap
}

func NewBlockRowsFromAggkitBlock(blockHeaders aggkittypes.ListBlockHeaders, isFinal bool) map[uint64]*blockRow {
	blockMap := make(map[uint64]*blockRow)
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

// NewTx starts a new storage transaction.
//
// Lock ordering matters: with the SQLite `_txlock=immediate` DSN, db.NewTx
// issues BEGIN IMMEDIATE and takes the SQLite writer lock. It must therefore
// run BEFORE any storage mutex is taken. Otherwise a concurrent ingestion
// transaction that already holds the writer lock could block on the storage
// mutex while this NewTx blocks on the writer lock, deadlocking until the
// SQLite busy timeout. a.db is an immutable handle whose BEGIN does not touch
// any mutex-protected storage state, so no storage lock is needed to start the
// transaction; the write helpers that operate on the returned transaction take
// the storage mutex after the transaction has begun.
func (a *MultidownloaderStorage) NewTx(ctx context.Context) (dbtypes.Txer, error) {
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
	IsFinal         bool           `meddler:"is_final"`
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
	LEFT JOIN blocks ON logs.block_number = blocks.block_number
	WHERE address IN (?)
	AND logs.block_number>=? AND logs.block_number<=?
	ORDER BY logs.block_number ASC, log_index ASC
	`
	addrs := make([]string, 0, len(query.Addrs))
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

func (a *MultidownloaderStorage) LogQuery(tx dbtypes.Querier,
	query mdrtypes.LogQuery) (mdrtypes.LogQueryResponse, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	dbRows := make([]*logAndBlockRow, 0)
	sqlQuery := `
	SELECT * FROM logs
	LEFT JOIN blocks ON logs.block_number = blocks.block_number
	WHERE address IN (?)
	AND logs.block_number>=? AND logs.block_number<=?
	ORDER BY logs.block_number ASC, log_index ASC
	`
	addrs := make([]string, 0, len(query.Addrs))
	for _, addr := range query.Addrs {
		addrs = append(addrs, addr.Hex())
	}
	// This is used to extend the address slice into the query
	queryStr, args, err := sqlx.In(sqlQuery, addrs, query.BlockRange.FromBlock, query.BlockRange.ToBlock)
	if err != nil {
		return mdrtypes.LogQueryResponse{}, fmt.Errorf("error building SQL query: %w", err)
	}
	err = meddler.QueryAll(tx, &dbRows, queryStr, args...)
	if err != nil {
		return mdrtypes.LogQueryResponse{}, fmt.Errorf("error querying logs: %w", err)
	}

	// Group logs by block number
	blockLogsMap := make(map[uint64]*mdrtypes.BlockWithLogs)
	blockOrder := make([]uint64, 0)

	for _, dbRow := range dbRows {
		var topics []common.Hash
		if err := json.Unmarshal([]byte(dbRow.Topics), &topics); err != nil {
			return mdrtypes.LogQueryResponse{}, fmt.Errorf("error unmarshaling topics: %w", err)
		}
		log := mdrtypes.Log{
			Address:        dbRow.Address,
			Topics:         topics,
			Data:           dbRow.Data,
			BlockNumber:    dbRow.BlockNumber,
			TxHash:         dbRow.TxHash,
			TxIndex:        dbRow.TxIndex,
			Index:          dbRow.Index,
			BlockTimestamp: dbRow.BlockTimestamp,
			Removed:        false,
		}

		// Add block to map if not already present
		if _, exists := blockLogsMap[dbRow.BlockNumber]; !exists {
			blockLogsMap[dbRow.BlockNumber] = &mdrtypes.BlockWithLogs{
				Header: aggkittypes.BlockHeader{
					Number:     dbRow.BlockNumber,
					Hash:       dbRow.BlockHash,
					Time:       dbRow.BlockTimestamp,
					ParentHash: dbRow.BlockParentHash,
				},
				IsFinal: dbRow.IsFinal,
				Logs:    make([]mdrtypes.Log, 0),
			}
			blockOrder = append(blockOrder, dbRow.BlockNumber)
		}

		blockLogsMap[dbRow.BlockNumber].Logs = append(blockLogsMap[dbRow.BlockNumber].Logs, log)
	}

	// Build response maintaining block order
	blocks := make([]mdrtypes.BlockWithLogs, 0, len(blockOrder))
	for _, blockNum := range blockOrder {
		blocks = append(blocks, *blockLogsMap[blockNum])
	}

	return mdrtypes.LogQueryResponse{
		Blocks:        blocks,
		ResponseRange: query.BlockRange,
	}, nil
}

// tx dbtypes.Txer
func (a *MultidownloaderStorage) SaveEthLogs(tx dbtypes.Querier, logs []types.Log, isFinal bool) error {
	return a.saveLogsAndBlocks(tx, NewBlockRowsFromLogs(logs, isFinal), NewLogRowsFromEthLogs(logs))
}

func (a *MultidownloaderStorage) SaveEthLogsWithHeaders(tx dbtypes.Querier,
	blockHeaders aggkittypes.ListBlockHeaders, logs []types.Log, isFinal bool) error {
	return a.saveLogsAndBlocks(tx, NewBlockRowsFromAggkitBlock(blockHeaders, isFinal), NewLogRowsFromEthLogs(logs))
}

func (a *MultidownloaderStorage) saveLogsAndBlocks(tx dbtypes.Querier,
	blockRows map[uint64]*blockRow, logRows []*logRow) error {
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

// ErrBlockHeaderIntegrityConflict is returned when an incoming block header
// shares the immutable identity (block number + block hash) of a stored one
// but its mutable metadata (timestamp / parent hash) contradicts the stored
// value. It is an explicit corruption signal: the caller must surface it rather
// than silently keeping one of the two values.
var ErrBlockHeaderIntegrityConflict = errors.New("block header integrity conflict")

func (a *MultidownloaderStorage) saveBlocksNoMutex(tx dbtypes.Querier, blockRows map[uint64]*blockRow) error {
	if tx == nil {
		tx = a.db
	}
	for _, incomingRow := range blockRows {
		a.logger.Debugf("Inserting block header row: %d %s final=%v", incomingRow.BlockNumber,
			incomingRow.BlockHash.Hex(), incomingRow.IsFinal)
		// Fast path: a fresh block header is inserted with the original single
		// INSERT, so the historical case (one header per new block) adds no
		// extra SELECT cost.
		if err := meddler.Insert(tx, "blocks", incomingRow); err != nil {
			// The only expected benign failure is the primary-key collision on
			// blocks.block_number, which happens when the very same header is
			// saved again for a second address's logs. Any other error is
			// propagated untouched.
			if !isBlockPrimaryKeyConstraint(err) {
				return fmt.Errorf("saveBlocksNoMutex: error inserting block header row (%s): %w", incomingRow.String(), err)
			}
			originalErr := fmt.Errorf("saveBlocksNoMutex: error inserting block header row (%s): %w", incomingRow.String(), err)
			existingRows := make([]*blockRow, 0)
			if queryErr := meddler.QueryAll(tx, &existingRows,
				"SELECT * FROM blocks WHERE block_number = ?", incomingRow.BlockNumber); queryErr != nil {
				return fmt.Errorf("saveBlocksNoMutex: error querying existing block header row (%d): %w",
					incomingRow.BlockNumber, queryErr)
			}
			if len(existingRows) == 0 {
				// The constraint fired but no row is readable: this is not the
				// benign duplicate-header case, so preserve the original error.
				return originalErr
			}
			if mergeErr := a.mergeBlockRowNoMutex(tx, existingRows[0], incomingRow); mergeErr != nil {
				return mergeErr
			}
		}
	}
	return nil
}

// isBlockPrimaryKeyConstraint reports whether err is a SQLite PRIMARY KEY
// (or UNIQUE) constraint violation, i.e. the duplicate block header case.
func isBlockPrimaryKeyConstraint(err error) bool {
	sqliteErr, ok := db.SQLiteErr(err)
	if !ok || sqliteErr == nil {
		return false
	}
	return sqliteErr.ExtendedCode == sqlite3.ErrConstraintPrimaryKey ||
		sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique
}

// mergeBlockRowNoMutex reconciles an incoming block header with the stored row
// that shares its block number. The block hash is the immutable identity:
//   - a different hash is a reorg and yields a typed DetectedReorgError (never
//     overwriting or ignoring the stored header);
//   - the same hash merges only previously unknown metadata (timestamp 0 /
//     nil parent) and monotonically promotes is_final, rejecting contradictory
//     known metadata with ErrBlockHeaderIntegrityConflict.
//
// The caller already holds the storage mutex and an open SQL transaction, so no
// locking or transaction management happens here.
func (a *MultidownloaderStorage) mergeBlockRowNoMutex(tx dbtypes.Querier, existing, incoming *blockRow) error {
	if existing.BlockHash != incoming.BlockHash {
		return fmt.Errorf("saveBlocksNoMutex: %w", mdrtypes.NewDetectedReorgError(
			incoming.BlockNumber,
			mdrtypes.ReorgDetectionReason_BlockHashMismatch,
			existing.BlockHash, incoming.BlockHash,
			"stored block header has a different hash than the incoming header"))
	}

	merged := *existing

	// Timestamp: only merge an unknown (zero) value; contradicting known values
	// are an integrity conflict.
	if existing.BlockTimestamp != 0 && incoming.BlockTimestamp != 0 &&
		existing.BlockTimestamp != incoming.BlockTimestamp {
		return fmt.Errorf("saveBlocksNoMutex: block %d: %w: timestamp %d != incoming %d",
			existing.BlockNumber, ErrBlockHeaderIntegrityConflict,
			existing.BlockTimestamp, incoming.BlockTimestamp)
	}
	if merged.BlockTimestamp == 0 {
		merged.BlockTimestamp = incoming.BlockTimestamp
	}

	// Parent hash: only merge an unknown (nil) value; contradicting known
	// values are an integrity conflict.
	if existing.BlockParentHash != nil && incoming.BlockParentHash != nil &&
		*existing.BlockParentHash != *incoming.BlockParentHash {
		return fmt.Errorf("saveBlocksNoMutex: block %d: %w: parent %s != incoming %s",
			existing.BlockNumber, ErrBlockHeaderIntegrityConflict,
			existing.BlockParentHash.Hex(), incoming.BlockParentHash.Hex())
	}
	if merged.BlockParentHash == nil {
		merged.BlockParentHash = incoming.BlockParentHash
	}

	// Finality is monotonic: once final, it is never downgraded.
	merged.IsFinal = existing.IsFinal || incoming.IsFinal

	if !blockRowMetadataChanged(existing, &merged) {
		// Idempotent replay: nothing to write, so skip the UPDATE entirely.
		return nil
	}

	// Update only mutable metadata. The (block_number, block_hash) pair in the
	// WHERE clause is the identity guard, and the hash is never changed.
	_, err := tx.Exec(
		"UPDATE blocks SET block_timestamp = ?, block_parent_hash = ?, is_final = ? "+
			"WHERE block_number = ? AND block_hash = ?",
		merged.BlockTimestamp, blockParentHashDBValue(merged.BlockParentHash), merged.IsFinal,
		merged.BlockNumber, merged.BlockHash.Hex())
	if err != nil {
		return fmt.Errorf("saveBlocksNoMutex: error updating block header row (%s): %w", merged.String(), err)
	}
	return nil
}

// blockRowMetadataChanged reports whether merged differs from existing in any
// mutable metadata field (timestamp, parent hash or finality).
func blockRowMetadataChanged(existing, merged *blockRow) bool {
	if existing.BlockTimestamp != merged.BlockTimestamp {
		return true
	}
	if existing.IsFinal != merged.IsFinal {
		return true
	}
	if (existing.BlockParentHash == nil) != (merged.BlockParentHash == nil) {
		return true
	}
	if existing.BlockParentHash != nil && *existing.BlockParentHash != *merged.BlockParentHash {
		return true
	}
	return false
}

// blockParentHashDBValue returns the value to bind for block_parent_hash,
// preserving the meddler pointer semantics: a known hash is stored as its hex
// string, an unknown (nil) parent is stored as SQL NULL (which reads back as a
// nil *common.Hash).
func blockParentHashDBValue(parent *common.Hash) interface{} {
	if parent == nil {
		return nil
	}
	return parent.Hex()
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
