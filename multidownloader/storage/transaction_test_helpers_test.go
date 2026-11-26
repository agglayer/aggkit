package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"path"
	"sort"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage/migrations"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// txTestLog builds a log with the given fields. A nil topics slice produces a
// topicless log.
func txTestLog(addr common.Address, blockNumber uint64, blockHash, txHash common.Hash,
	logIndex uint, topics []common.Hash) ethtypes.Log {
	return ethtypes.Log{
		Address:        addr,
		BlockNumber:    blockNumber,
		BlockHash:      blockHash,
		BlockTimestamp: 1630000000,
		Topics:         topics,
		TxHash:         txHash,
		TxIndex:        0,
		Index:          logIndex,
	}
}

// txTestRPC builds a mined RPC transaction. A nil to address produces a
// contract-creation transaction (nullable `to`).
func txTestRPC(blockNumber uint64, blockHash, txHash common.Hash, to *common.Address) aggkittypes.RPCTransaction {
	blockNumberHex := fmt.Sprintf("0x%x", blockNumber)
	blockHashHex := blockHash.Hex()
	txIndexHex := "0x0"
	tx := aggkittypes.RPCTransaction{
		FromRaw:             exampleAddr1.Hex(),
		HashRaw:             txHash.Hex(),
		BlockHashRaw:        &blockHashHex,
		BlockNumberRaw:      &blockNumberHex,
		TransactionIndexRaw: &txIndexHex,
		InputRaw:            "0x",
	}
	if to != nil {
		toHex := to.Hex()
		tx.To = &toHex
	}
	return tx
}

func saveLogsForTest(t *testing.T, storage *MultidownloaderStorage, logs ...ethtypes.Log) {
	t.Helper()
	require.NoError(t, storage.SaveEthLogs(nil, logs, true))
}

// setSyncedCoverageForTest persists the synced log coverage for an address so
// that log-coverage queries can be exercised. It upserts the contract row and
// then sets its synced range.
func setSyncedCoverageForTest(t *testing.T, storage *MultidownloaderStorage,
	addr common.Address, from, to uint64) {
	t.Helper()
	require.NoError(t, storage.UpsertSyncerConfigs(nil, []mdrtypes.ContractConfig{
		{
			Address:   addr,
			FromBlock: from,
			ToBlock:   aggkittypes.LatestBlock,
			Syncers:   []mdrtypes.SyncerID{"test"},
		},
	}))
	require.NoError(t, storage.UpdateSyncedStatus(nil, []mdrtypes.SyncSegment{
		{
			ContractAddr:  addr,
			TargetToBlock: aggkittypes.LatestBlock,
			BlockRange:    aggkitcommon.NewBlockRange(from, to),
		},
	}))
}

// newStorageForTestWithBusyTimeout builds a storage backed by a SQLite database
// whose busy timeout is short, so a lock-ordering regression fails fast instead
// of stalling for the production 30s busy timeout.
func newStorageForTestWithBusyTimeout(t *testing.T, busyTimeoutMs int) *MultidownloaderStorage {
	t.Helper()
	logger := log.WithFields("module", "test")
	dbPath := path.Join(t.TempDir(), "multidownloader_Storage.sqlite")
	database, err := sql.Open("sqlite3", fmt.Sprintf(
		"file:%s?_txlock=immediate&_foreign_keys=on&_journal_mode=WAL&_busy_timeout=%d", dbPath, busyTimeoutMs))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	require.NoError(t, migrations.RunMigrations(logger, database))
	return &MultidownloaderStorage{
		db:               database,
		logger:           logger,
		cfg:              MultidownloaderStorageConfig{DBPath: dbPath},
		KeyValueStorager: db.NewKeyValueStorage(database),
	}
}

// assertStorageMutexFreeWhileBlocked asserts that, for the given window, the
// storage mutex stays acquirable while the blocked write has not completed. It
// fails as soon as the mutex is observed held (the lock-ordering bug) or the
// write completes early (the writer lock was not actually held).
func assertStorageMutexFreeWhileBlocked(t *testing.T, storage *MultidownloaderStorage,
	blocked <-chan error, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		select {
		case err := <-blocked:
			t.Fatalf("blocked write completed before the writer lock was released: %v", err)
		default:
		}
		if !storage.mutex.TryLock() {
			t.Fatal("storage mutex is held while a write waits for the SQLite writer lock")
		}
		storage.mutex.Unlock()
		time.Sleep(time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// Aggregate expectations built only from the remaining production primitives
// ---------------------------------------------------------------------------

// transactionAvailability is the test-only local mirror of the removed
// production mdrtypes.TransactionAvailability. Complete is true only when the
// requested log query is fully covered by the persisted syncer coverage
// (LogCoverage) AND every relevant canonical log has its transaction stored.
type transactionAvailability struct {
	MissingTxHashes []common.Hash
	LogCoverage     bool
	Complete        bool
}

// transactionAvailabilityForTest reproduces the removed
// TransactionAvailabilityForLogQuery aggregate from the remaining primitives.
func transactionAvailabilityForTest(t *testing.T, storage *MultidownloaderStorage,
	tx dbtypes.Querier, query mdrtypes.LogQuery, topic0 []common.Hash) transactionAvailability {
	t.Helper()
	missing := missingTxHashesForLogQueryForTest(t, storage, tx, query, topic0)
	covered := logQueryCoveredForTest(t, storage, tx, query)
	return transactionAvailability{
		MissingTxHashes: missing,
		LogCoverage:     covered,
		Complete:        covered && len(missing) == 0,
	}
}

// missingTxHashesForLogQueryForTest reproduces the removed
// MissingTransactionsForLogQuery aggregate from the remaining primitives: it
// scopes the query to the exact canonical logs (GetEthLogs), applies the OR-ed
// topic0 selector and reports the distinct tx hashes that have no canonical
// transaction row. Entries are deduplicated by (tx_hash, block_hash) and the
// result is stable-sorted by (block_number, log_index), matching the removed
// SQL `ORDER BY l.block_number ASC, l.log_index ASC`.
func missingTxHashesForLogQueryForTest(t *testing.T, storage *MultidownloaderStorage,
	tx dbtypes.Querier, query mdrtypes.LogQuery, topic0 []common.Hash) []common.Hash {
	t.Helper()
	logs := scopedLogsForTest(t, storage, tx, query)

	type missingEntry struct {
		hash        common.Hash
		blockNumber uint64
		logIndex    uint
	}
	seen := make(map[string]struct{}, len(logs))
	entries := make([]missingEntry, 0, len(logs))
	for _, lg := range logs {
		if !mdrtypes.Topic0Match(topic0, lg.Topics) {
			continue
		}
		got, err := storage.GetTransaction(tx, lg.TxHash, lg.BlockHash)
		require.NoError(t, err)
		if got != nil {
			continue
		}
		key := lg.TxHash.Hex() + ":" + lg.BlockHash.Hex()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		entries = append(entries, missingEntry{hash: lg.TxHash, blockNumber: lg.BlockNumber, logIndex: lg.Index})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].blockNumber != entries[j].blockNumber {
			return entries[i].blockNumber < entries[j].blockNumber
		}
		return entries[i].logIndex < entries[j].logIndex
	})
	missing := make([]common.Hash, len(entries))
	for i, e := range entries {
		missing[i] = e.hash
	}
	return missing
}

// scopedLogsForTest returns the canonical logs matching the query using the
// production GetEthLogs primitive. A block-hash query is resolved through the
// canonical header first and then expressed as a single-block number filter,
// and the returned logs are restricted to the requested block hash so a
// non-canonical hash yields no logs.
func scopedLogsForTest(t *testing.T, storage *MultidownloaderStorage,
	tx dbtypes.Querier, query mdrtypes.LogQuery) []ethtypes.Log {
	t.Helper()
	addrs := logQueryAddressesForTest(t, storage, tx, query)
	if len(addrs) == 0 {
		return nil
	}

	if query.IsBlockHashQuery() {
		blockNumber := query.BlockRange.FromBlock
		if query.BlockRange.IsEmpty() {
			number, found := blockNumberByHashForTest(t, storage, tx, *query.BlockHash)
			if !found {
				return nil
			}
			blockNumber = number
		}
		logs, err := storage.GetEthLogs(tx, mdrtypes.NewLogQuery(blockNumber, blockNumber, addrs))
		require.NoError(t, err)
		scoped := make([]ethtypes.Log, 0, len(logs))
		for _, lg := range logs {
			if lg.BlockHash == *query.BlockHash {
				scoped = append(scoped, lg)
			}
		}
		return scoped
	}

	logs, err := storage.GetEthLogs(tx,
		mdrtypes.NewLogQuery(query.BlockRange.FromBlock, query.BlockRange.ToBlock, addrs))
	require.NoError(t, err)
	return logs
}

// logQueryAddressesForTest returns the address list GetEthLogs must be called
// with. GetEthLogs expands the addresses into `address IN (?)` and therefore
// cannot express "all addresses": when the query names none, the distinct log
// addresses are discovered directly from the logs table instead (read-only,
// same package).
func logQueryAddressesForTest(t *testing.T, storage *MultidownloaderStorage,
	tx dbtypes.Querier, query mdrtypes.LogQuery) []common.Address {
	t.Helper()
	if len(query.Addrs) > 0 {
		return query.Addrs
	}
	rows, err := querierForTest(storage, tx).Query("SELECT DISTINCT address FROM logs ORDER BY address")
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	addrs := make([]common.Address, 0)
	for rows.Next() {
		var addr string
		require.NoError(t, rows.Scan(&addr))
		addrs = append(addrs, common.HexToAddress(addr))
	}
	require.NoError(t, rows.Err())
	return addrs
}

// logQueryCoveredForTest reproduces the removed coverage check from the
// remaining primitives: a block-range query requires every requested address
// (or, when none is given, every registered address) to cover the whole range,
// and a block-hash query additionally requires the canonical header at that
// height to match the requested hash. An unknown address, a partially covered
// range or a non-canonical hash is never reported as covered.
func logQueryCoveredForTest(t *testing.T, storage *MultidownloaderStorage,
	tx dbtypes.Querier, query mdrtypes.LogQuery) bool {
	t.Helper()
	coverage, err := storage.GetSyncedBlockRangePerContract(tx)
	require.NoError(t, err)

	addrs := query.Addrs
	if len(addrs) == 0 {
		// Empty address list means "all registered addresses". When nothing is
		// registered, nothing is known to be covered.
		addrs = coverage.GetContracts()
	}
	if len(addrs) == 0 {
		return false
	}

	if query.IsBlockHashQuery() {
		blockNumber := query.BlockRange.FromBlock
		if query.BlockRange.IsEmpty() {
			number, found := blockNumberByHashForTest(t, storage, tx, *query.BlockHash)
			if !found {
				return false
			}
			blockNumber = number
		}
		// The requested hash must still be the canonical block at that height.
		header, _, err := storage.GetBlockHeaderByNumber(tx, blockNumber)
		require.NoError(t, err)
		if header == nil || header.Hash != *query.BlockHash {
			return false
		}
		for _, addr := range addrs {
			segment, exists := coverage.GetByContract(addr)
			if !exists || !segment.BlockRange.ContainsBlockNumber(blockNumber) {
				return false
			}
		}
		return true
	}

	for _, addr := range addrs {
		segment, exists := coverage.GetByContract(addr)
		if !exists || !segment.BlockRange.Contains(query.BlockRange) {
			return false
		}
	}
	return true
}

// blockNumberByHashForTest resolves a stored canonical block number from its
// hash, mirroring the removed blockNumberByHashNoMutex helper.
func blockNumberByHashForTest(t *testing.T, storage *MultidownloaderStorage,
	tx dbtypes.Querier, blockHash common.Hash) (uint64, bool) {
	t.Helper()
	var number uint64
	err := querierForTest(storage, tx).
		QueryRow("SELECT block_number FROM blocks WHERE block_hash = ? LIMIT 1", blockHash.Hex()).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false
	}
	require.NoError(t, err)
	return number, true
}

// querierForTest returns the querier the test-only helpers should read through:
// the caller-owned transaction when present, otherwise the storage connection.
func querierForTest(storage *MultidownloaderStorage, tx dbtypes.Querier) dbtypes.Querier {
	if tx != nil {
		return tx
	}
	return storage.db
}

// countTransactionsForTest returns the number of rows physically present in the
// transactions table, used to assert the stored inclusion count directly.
func countTransactionsForTest(t *testing.T, storage *MultidownloaderStorage) int {
	t.Helper()
	var count int
	require.NoError(t, storage.db.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&count))
	return count
}
