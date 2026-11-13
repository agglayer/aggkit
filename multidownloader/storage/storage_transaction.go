package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	dbtypes "github.com/agglayer/aggkit/db/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jmoiron/sqlx"
	"github.com/russross/meddler"
)

// transactionRow is the canonical storage representation of a transaction.
// The (tx_hash, block_hash) pair is the primary key so distinct chain
// inclusions of the same transaction hash are never conflated. to_address is
// nullable (contract creation) and raw preserves the original JSON payload.
type transactionRow struct {
	TxHash      common.Hash     `meddler:"tx_hash,hash"`
	BlockHash   common.Hash     `meddler:"block_hash,hash"`
	BlockNumber uint64          `meddler:"block_number"`
	TxIndex     uint64          `meddler:"tx_index"`
	FromAddress common.Address  `meddler:"from_address,address"`
	ToAddress   *common.Address `meddler:"to_address,address"`
	Input       []byte          `meddler:"input"`
	Raw         string          `meddler:"raw"`
}

// transactionRowFromRPC builds a storage row from a decoded RPC transaction.
// The caller must have validated the transaction.
func transactionRowFromRPC(t *aggkittypes.RPCTransaction) (*transactionRow, error) {
	if t == nil {
		return nil, errors.New("transactionRowFromRPC: nil transaction")
	}
	blockNumber, ok := t.BlockNumber()
	if !ok {
		return nil, fmt.Errorf("transactionRowFromRPC: transaction %s has no block number", t.HashRaw)
	}
	blockHash, ok := t.BlockHash()
	if !ok {
		return nil, fmt.Errorf("transactionRowFromRPC: transaction %s has no block hash", t.HashRaw)
	}
	txIndex, ok := t.TransactionIndex()
	if !ok {
		return nil, fmt.Errorf("transactionRowFromRPC: transaction %s has no transaction index", t.HashRaw)
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("transactionRowFromRPC: cannot marshal transaction %s: %w", t.HashRaw, err)
	}
	return &transactionRow{
		TxHash:      t.Hash(),
		BlockHash:   blockHash,
		BlockNumber: blockNumber,
		TxIndex:     txIndex,
		FromAddress: t.From(),
		ToAddress:   t.ToAddressPtr(),
		Input:       t.Input(),
		Raw:         string(raw),
	}, nil
}

// rowToRPCTransaction reconstructs an RPC transaction from a storage row. The
// preserved raw payload is preferred so no field is lost; if it is missing the
// indexed columns are used as a fallback.
func rowToRPCTransaction(row *transactionRow) (*aggkittypes.RPCTransaction, error) {
	if row == nil {
		return nil, nil
	}
	var tx aggkittypes.RPCTransaction
	if row.Raw != "" {
		if err := json.Unmarshal([]byte(row.Raw), &tx); err != nil {
			return nil, fmt.Errorf("rowToRPCTransaction: cannot unmarshal raw transaction %s: %w",
				row.TxHash.Hex(), err)
		}
		return &tx, nil
	}
	blockHash := row.BlockHash.Hex()
	blockNumber := fmt.Sprintf("0x%x", row.BlockNumber)
	txIndex := fmt.Sprintf("0x%x", row.TxIndex)
	tx = aggkittypes.RPCTransaction{
		FromRaw:             row.FromAddress.Hex(),
		HashRaw:             row.TxHash.Hex(),
		BlockHashRaw:        &blockHash,
		BlockNumberRaw:      &blockNumber,
		TransactionIndexRaw: &txIndex,
	}
	if row.ToAddress != nil {
		to := row.ToAddress.Hex()
		tx.To = &to
	}
	return &tx, nil
}

// getCanonicalBlockHashNoMutex returns the canonical block hash stored for the
// given block number, or nil when the block is not stored. It requires the
// caller to hold the storage mutex (or to pass an isolated transaction).
func (a *MultidownloaderStorage) getCanonicalBlockHashNoMutex(tx dbtypes.Querier,
	blockNumber uint64) (*common.Hash, error) {
	if tx == nil {
		tx = a.db
	}
	var rows []*blockRow
	if err := meddler.QueryAll(tx, &rows,
		"SELECT * FROM blocks WHERE block_number = ? LIMIT 1", blockNumber); err != nil {
		return nil, fmt.Errorf("getCanonicalBlockHashNoMutex: error querying block %d: %w", blockNumber, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	hash := rows[0].BlockHash
	return &hash, nil
}

// SaveTransactions persists canonical transactions.
//
// When tx is nil the whole save runs inside its own SQL transaction that is
// started before the storage mutex is taken (to keep the mutex/writer-lock
// ordering deadlock-free) and rolled back on any failure, so a partial write
// can never be observed. When a caller-provided transaction is given, the
// caller owns atomicity. Every transaction is validated against the canonical
// blocks table on (block_number, block_hash); a mismatch fails the save with
// aggkittypes.ErrTransactionInclusionMismatch. The insert is idempotent per
// (tx_hash, block_hash).
func (a *MultidownloaderStorage) SaveTransactions(tx dbtypes.Querier,
	txs []aggkittypes.RPCTransaction) error {
	if len(txs) == 0 {
		return nil
	}

	// Lock ordering matters: with the SQLite `_txlock=immediate` DSN, BeginTx
	// issues BEGIN IMMEDIATE and takes the SQLite writer lock. When we own the
	// transaction we must therefore start it BEFORE taking the storage mutex.
	// Otherwise a concurrent ingestion transaction that already holds the writer
	// lock (it was begun first) would block on the storage mutex while we block
	// on the writer lock, deadlocking until the SQLite busy timeout. A
	// caller-provided transaction has already begun, so its owner acquired the
	// writer lock before calling us and no ordering constraint applies here.
	var sqlTx dbtypes.SQLTxer
	if tx == nil {
		var err error
		sqlTx, err = a.db.BeginTx(context.Background(), nil)
		if err != nil {
			return fmt.Errorf("SaveTransactions: cannot begin transaction: %w", err)
		}
		// Roll back on every path that does not commit. The deferred rollback is
		// disabled by clearing sqlTx once the transaction has been committed.
		defer func() {
			if sqlTx != nil {
				if rbErr := sqlTx.Rollback(); rbErr != nil {
					a.logger.Errorf("SaveTransactions: cannot roll back transaction: %v", rbErr)
				}
			}
		}()
		tx = sqlTx
	}

	// The storage mutex serializes the mutex-protected writes. It is taken only
	// after the transaction has begun (owned here or by the caller) so that we
	// never hold it while waiting on the SQLite writer lock.
	a.mutex.Lock()
	defer a.mutex.Unlock()

	// The canonical (block_number, block_hash) recheck and the row insert run on
	// the same transaction while we hold the writer lock, so a reorg cannot
	// commit between the recheck and the insert: no stale inclusion is written.
	if err := a.saveTransactionsNoMutex(tx, txs); err != nil {
		return err
	}
	if sqlTx != nil {
		if err := sqlTx.Commit(); err != nil {
			return fmt.Errorf("SaveTransactions: cannot commit transaction: %w", err)
		}
		sqlTx = nil
	}
	return nil
}

// saveTransactionsNoMutex validates and inserts the given transactions using
// the provided querier. The caller must hold the storage mutex.
func (a *MultidownloaderStorage) saveTransactionsNoMutex(tx dbtypes.Querier,
	txs []aggkittypes.RPCTransaction) error {
	insertQuery := `INSERT INTO transactions
		(tx_hash, block_hash, block_number, tx_index, from_address, to_address, input, raw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tx_hash, block_hash) DO NOTHING;`

	for i := range txs {
		t := txs[i]
		if err := t.Validate(); err != nil {
			return fmt.Errorf("saveTransactionsNoMutex: invalid transaction: %w", err)
		}
		blockNumber, _ := t.BlockNumber()
		blockHash, _ := t.BlockHash()

		canonicalHash, err := a.getCanonicalBlockHashNoMutex(tx, blockNumber)
		if err != nil {
			return err
		}
		if canonicalHash == nil {
			return fmt.Errorf(
				"%w: transaction %s references block %d which is not stored",
				aggkittypes.ErrTransactionInclusionMismatch, t.HashRaw, blockNumber)
		}
		if *canonicalHash != blockHash {
			return fmt.Errorf(
				"%w: transaction %s expected canonical block %d hash %s but found %s",
				aggkittypes.ErrTransactionInclusionMismatch,
				t.HashRaw, blockNumber, blockHash.Hex(), canonicalHash.Hex())
		}

		row, err := transactionRowFromRPC(&t)
		if err != nil {
			return err
		}
		var toAddress interface{}
		if row.ToAddress != nil {
			toAddress = row.ToAddress.Hex()
		}
		if _, err := tx.Exec(insertQuery,
			row.TxHash.Hex(), row.BlockHash.Hex(), row.BlockNumber, row.TxIndex,
			row.FromAddress.Hex(), toAddress, row.Input, row.Raw); err != nil {
			return fmt.Errorf("saveTransactionsNoMutex: error inserting transaction %s/%s: %w",
				row.TxHash.Hex(), row.BlockHash.Hex(), err)
		}
	}
	return nil
}

// GetTransaction returns the canonical transaction identified by
// (txHash, blockHash). It returns nil when the transaction is not stored or is
// no longer canonical. The canonical check joins the blocks table on both the
// block number and the block hash, so orphaned or reorged transactions are
// never returned.
func (a *MultidownloaderStorage) GetTransaction(tx dbtypes.Querier,
	txHash common.Hash, blockHash common.Hash) (*aggkittypes.RPCTransaction, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	var rows []*transactionRow
	query := `SELECT t.* FROM transactions t
		INNER JOIN blocks b
			ON t.block_number = b.block_number AND t.block_hash = b.block_hash
		WHERE t.tx_hash = ? AND t.block_hash = ?
		LIMIT 1;`
	if err := meddler.QueryAll(tx, &rows, query, txHash.Hex(), blockHash.Hex()); err != nil {
		return nil, fmt.Errorf("GetTransaction: error querying transaction %s/%s: %w",
			txHash.Hex(), blockHash.Hex(), err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rowToRPCTransaction(rows[0])
}

// GetTransactionsByBlock returns the canonical transactions stored for a given
// block, only when the provided block hash still matches the canonical block.
func (a *MultidownloaderStorage) GetTransactionsByBlock(tx dbtypes.Querier,
	blockNumber uint64, blockHash common.Hash) ([]aggkittypes.RPCTransaction, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	var rows []*transactionRow
	query := `SELECT t.* FROM transactions t
		INNER JOIN blocks b
			ON t.block_number = b.block_number AND t.block_hash = b.block_hash
		WHERE t.block_number = ? AND t.block_hash = ?
		ORDER BY t.tx_index ASC;`
	if err := meddler.QueryAll(tx, &rows, query, blockNumber, blockHash.Hex()); err != nil {
		return nil, fmt.Errorf("GetTransactionsByBlock: error querying block %d/%s: %w",
			blockNumber, blockHash.Hex(), err)
	}
	result := make([]aggkittypes.RPCTransaction, 0, len(rows))
	for _, row := range rows {
		t, err := rowToRPCTransaction(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *t)
	}
	return result, nil
}

// missingTxLogRow is a canonical log row projected for the missing-transaction
// computation.
type missingTxLogRow struct {
	TxHash    common.Hash `meddler:"tx_hash,hash"`
	BlockHash common.Hash `meddler:"block_hash,hash"`
	Topics    string      `meddler:"topics"`
}

// MissingTransactionsForLogQuery returns the distinct tx hashes of canonical
// stored logs matching the query that do not yet have a canonical transaction
// row. Availability is derived from the current log coverage and per-tx row
// existence (no per-syncer interval cache, no negative cache): a log is relevant
// when its block is canonical, it is within the query range/hash and its topic0
// is in topic0 (an empty topic0 matches every log, including topicless). Logs
// whose transaction is stored under a stale, non-canonical block hash are still
// reported as missing.
func (a *MultidownloaderStorage) MissingTransactionsForLogQuery(tx dbtypes.Querier,
	query mdrtypes.LogQuery, topic0 []common.Hash) ([]common.Hash, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.missingTransactionsForLogQueryNoMutex(tx, query, topic0)
}

// missingTransactionsForLogQueryNoMutex is the lock-free implementation of
// MissingTransactionsForLogQuery. The caller must hold the storage mutex.
func (a *MultidownloaderStorage) missingTransactionsForLogQueryNoMutex(tx dbtypes.Querier,
	query mdrtypes.LogQuery, topic0 []common.Hash) ([]common.Hash, error) {
	if tx == nil {
		tx = a.db
	}
	if !query.IsValid() {
		return nil, fmt.Errorf("MissingTransactionsForLogQuery: invalid query %s", query.String())
	}

	sqlQuery := `SELECT l.tx_hash, b.block_hash, l.topics FROM logs l
		INNER JOIN blocks b ON l.block_number = b.block_number
		LEFT JOIN transactions t
			ON t.tx_hash = l.tx_hash AND t.block_hash = b.block_hash
		WHERE t.tx_hash IS NULL`

	args := make([]interface{}, 0)
	if query.IsBlockHashQuery() {
		sqlQuery += " AND b.block_hash = ?"
		args = append(args, query.BlockHash.Hex())
		if !query.BlockRange.IsEmpty() {
			sqlQuery += " AND l.block_number >= ? AND l.block_number <= ?"
			args = append(args, query.BlockRange.FromBlock, query.BlockRange.ToBlock)
		}
	} else {
		sqlQuery += " AND l.block_number >= ? AND l.block_number <= ?"
		args = append(args, query.BlockRange.FromBlock, query.BlockRange.ToBlock)
	}

	// An empty address list means "all addresses" instead of an invalid IN ().
	if len(query.Addrs) > 0 {
		addrs := make([]string, 0, len(query.Addrs))
		for _, addr := range query.Addrs {
			addrs = append(addrs, addr.Hex())
		}
		var err error
		sqlQuery, args, err = sqlx.In(sqlQuery+" AND l.address IN (?)", append(args, addrs)...)
		if err != nil {
			return nil, fmt.Errorf("MissingTransactionsForLogQuery: error building SQL query: %w", err)
		}
	}
	sqlQuery += " ORDER BY l.block_number ASC, l.log_index ASC;"

	var rows []*missingTxLogRow
	if err := meddler.QueryAll(tx, &rows, sqlQuery, args...); err != nil {
		return nil, fmt.Errorf("MissingTransactionsForLogQuery: error querying logs: %w", err)
	}

	seen := make(map[string]struct{}, len(rows))
	result := make([]common.Hash, 0, len(rows))
	for _, row := range rows {
		var logTopics []common.Hash
		if err := json.Unmarshal([]byte(row.Topics), &logTopics); err != nil {
			return nil, fmt.Errorf("MissingTransactionsForLogQuery: error unmarshaling topics: %w", err)
		}
		if !mdrtypes.Topic0Match(topic0, logTopics) {
			continue
		}
		key := row.TxHash.Hex() + ":" + row.BlockHash.Hex()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, row.TxHash)
	}
	return result, nil
}

// TransactionAvailabilityForLogQuery returns the enriched transaction
// availability for a log query. It reads the missing transactions and the
// persisted syncer coverage from a single coherent snapshot while holding the
// storage read lock (it never calls a mutex-locking helper recursively, which
// would deadlock against a waiting writer). Complete is true only when the
// query range/addresses are fully covered AND no relevant log misses its
// transaction.
func (a *MultidownloaderStorage) TransactionAvailabilityForLogQuery(tx dbtypes.Querier,
	query mdrtypes.LogQuery, topic0 []common.Hash) (mdrtypes.TransactionAvailability, error) {
	if tx == nil {
		tx = a.db
	}
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	missing, err := a.missingTransactionsForLogQueryNoMutex(tx, query, topic0)
	if err != nil {
		return mdrtypes.TransactionAvailability{}, err
	}
	covered, err := a.logQueryCoveredNoMutex(tx, query)
	if err != nil {
		return mdrtypes.TransactionAvailability{}, err
	}
	return mdrtypes.TransactionAvailability{
		MissingTxHashes: missing,
		LogCoverage:     covered,
		Complete:        covered && len(missing) == 0,
	}, nil
}

// syncStatusCoverageRow projects the persisted synced log coverage of a single
// contract.
type syncStatusCoverageRow struct {
	Address         common.Address `meddler:"contract_address,address"`
	SyncedFromBlock uint64         `meddler:"synced_from_block"`
	SyncedToBlock   uint64         `meddler:"synced_to_block"`
}

// syncStatusCoverageNoMutex returns the persisted synced log coverage per
// contract address. The range {0,0} is the DB sentinel for "no coverage" and is
// mapped to an empty range. The caller must hold the storage mutex.
func (a *MultidownloaderStorage) syncStatusCoverageNoMutex(
	tx dbtypes.Querier) (map[common.Address]aggkitcommon.BlockRange, error) {
	if tx == nil {
		tx = a.db
	}
	var rows []*syncStatusCoverageRow
	if err := meddler.QueryAll(tx, &rows,
		"SELECT contract_address, synced_from_block, synced_to_block FROM sync_status"); err != nil {
		return nil, fmt.Errorf("syncStatusCoverageNoMutex: error querying sync_status: %w", err)
	}
	result := make(map[common.Address]aggkitcommon.BlockRange, len(rows))
	for _, row := range rows {
		if row.SyncedFromBlock == 0 && row.SyncedToBlock == 0 {
			result[row.Address] = aggkitcommon.BlockRangeZero
			continue
		}
		result[row.Address] = aggkitcommon.NewBlockRange(row.SyncedFromBlock, row.SyncedToBlock)
	}
	return result, nil
}

// blockNumberByHashNoMutex returns the block number stored for the given block
// hash, and whether it was found. The caller must hold the storage mutex.
func (a *MultidownloaderStorage) blockNumberByHashNoMutex(tx dbtypes.Querier,
	blockHash common.Hash) (uint64, bool, error) {
	if tx == nil {
		tx = a.db
	}
	var rows []*blockRow
	if err := meddler.QueryAll(tx, &rows,
		"SELECT * FROM blocks WHERE block_hash = ? LIMIT 1", blockHash.Hex()); err != nil {
		return 0, false, fmt.Errorf("blockNumberByHashNoMutex: error querying block %s: %w", blockHash.Hex(), err)
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	return rows[0].BlockNumber, true, nil
}

// logQueryCoveredNoMutex reports whether the requested log query is fully
// covered by the persisted syncer coverage. A block-range query requires every
// requested address (or, when no address is given, every registered address) to
// cover the whole range. A block-hash query additionally requires the canonical
// block at that height to match the requested hash. An unknown address (no
// sync_status row) or a range that is only partially covered is never reported
// as covered. The caller must hold the storage mutex.
func (a *MultidownloaderStorage) logQueryCoveredNoMutex(tx dbtypes.Querier,
	query mdrtypes.LogQuery) (bool, error) {
	if tx == nil {
		tx = a.db
	}
	if !query.IsValid() {
		return false, fmt.Errorf("logQueryCoveredNoMutex: invalid query %s", query.String())
	}
	coverage, err := a.syncStatusCoverageNoMutex(tx)
	if err != nil {
		return false, err
	}

	addrs := query.Addrs
	if len(addrs) == 0 {
		// Empty address list means "all registered addresses". When nothing is
		// registered, nothing is known to be covered.
		addrs = make([]common.Address, 0, len(coverage))
		for addr := range coverage {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		return false, nil
	}

	if query.IsBlockHashQuery() {
		blockNumber := query.BlockRange.FromBlock
		if query.BlockRange.IsEmpty() {
			n, found, err := a.blockNumberByHashNoMutex(tx, *query.BlockHash)
			if err != nil {
				return false, err
			}
			if !found {
				return false, nil
			}
			blockNumber = n
		}
		// The requested hash must still be the canonical block at that height.
		canonical, err := a.getCanonicalBlockHashNoMutex(tx, blockNumber)
		if err != nil {
			return false, err
		}
		if canonical == nil || *canonical != *query.BlockHash {
			return false, nil
		}
		for _, addr := range addrs {
			br, ok := coverage[addr]
			if !ok || !br.ContainsBlockNumber(blockNumber) {
				return false, nil
			}
		}
		return true, nil
	}

	for _, addr := range addrs {
		br, ok := coverage[addr]
		if !ok {
			return false, nil
		}
		if !br.Contains(query.BlockRange) {
			return false, nil
		}
	}
	return true, nil
}

var _ mdrtypes.TransactionStorage = (*MultidownloaderStorage)(nil)
