package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"sync"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage/migrations"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

var (
	txTopicA = common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	txTopicB = common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	txTopicC = common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")
)

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

func TestStorage_TransactionStorageExtension(t *testing.T) {
	storage := newStorageForTest(t, nil)
	var storager mdrtypes.Storager = storage
	ts, err := mdrtypes.AsTransactionStorage(storager)
	require.NoError(t, err)
	require.NotNil(t, ts)

	_, err = mdrtypes.AsTransactionStorage(nil)
	require.Error(t, err)
}

func TestStorage_SaveAndGetTransaction(t *testing.T) {
	storage := newStorageForTest(t, nil)
	blockHash := exampleTestHash[0]
	txHash := exampleTestHash[1]
	to := exampleAddr2
	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, 1500, blockHash, txHash, 0, []common.Hash{txTopicA}))

	tx := txTestRPC(1500, blockHash, txHash, &to)
	require.NoError(t, storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{tx}))

	got, err := storage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, txHash, got.Hash())
	require.Equal(t, exampleAddr1, got.From())
	require.Equal(t, &to, got.ToAddressPtr())
	blockNumber, ok := got.BlockNumber()
	require.True(t, ok)
	require.Equal(t, uint64(1500), blockNumber)

	byBlock, err := storage.GetTransactionsByBlock(nil, 1500, blockHash)
	require.NoError(t, err)
	require.Len(t, byBlock, 1)

	// Idempotent per (tx_hash, block_hash).
	require.NoError(t, storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{tx}))
	byBlock, err = storage.GetTransactionsByBlock(nil, 1500, blockHash)
	require.NoError(t, err)
	require.Len(t, byBlock, 1)

	// Missing key returns nil, not an error.
	missing, err := storage.GetTransaction(nil, txHash, exampleTestHash[9])
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestStorage_TransactionDistinctInclusion(t *testing.T) {
	storage := newStorageForTest(t, nil)
	txHash := exampleTestHash[1]
	blockHash1 := exampleTestHash[0]
	blockHash2 := exampleTestHash[2]

	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, 1500, blockHash1, txHash, 0, []common.Hash{txTopicA}),
		txTestLog(exampleAddr1, 1501, blockHash2, txHash, 0, []common.Hash{txTopicA}))

	// Same tx hash, two distinct chain inclusions (block_hash differs).
	require.NoError(t, storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
		txTestRPC(1500, blockHash1, txHash, nil),
		txTestRPC(1501, blockHash2, txHash, nil),
	}))

	got1, err := storage.GetTransaction(nil, txHash, blockHash1)
	require.NoError(t, err)
	require.NotNil(t, got1)
	got2, err := storage.GetTransaction(nil, txHash, blockHash2)
	require.NoError(t, err)
	require.NotNil(t, got2)

	byBlock1, err := storage.GetTransactionsByBlock(nil, 1500, blockHash1)
	require.NoError(t, err)
	require.Len(t, byBlock1, 1)
	byBlock2, err := storage.GetTransactionsByBlock(nil, 1501, blockHash2)
	require.NoError(t, err)
	require.Len(t, byBlock2, 1)
}

func TestStorage_SaveTransactionsStaleCommitRejected(t *testing.T) {
	storage := newStorageForTest(t, nil)
	canonicalHash := exampleTestHash[0]
	staleHash := exampleTestHash[4]
	txHash := exampleTestHash[1]
	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, 1500, canonicalHash, txHash, 0, []common.Hash{txTopicA}))

	// The block number exists but the hash does not match the canonical block.
	err := storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
		txTestRPC(1500, staleHash, txHash, nil),
	})
	require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)

	got, err := storage.GetTransaction(nil, txHash, staleHash)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestStorage_SaveTransactionsUnknownBlockRejected(t *testing.T) {
	storage := newStorageForTest(t, nil)
	err := storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
		txTestRPC(999999, exampleTestHash[0], exampleTestHash[1], nil),
	})
	require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
}

func TestStorage_SaveTransactionsRollbackOnFailure(t *testing.T) {
	storage := newStorageForTest(t, nil)
	goodHash := exampleTestHash[0]
	badHash := exampleTestHash[0] // same block number, wrong hash for the second tx
	txHashGood := exampleTestHash[1]
	txHashBad := exampleTestHash[2]

	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, 1500, goodHash, txHashGood, 0, []common.Hash{txTopicA}),
		txTestLog(exampleAddr1, 1501, exampleTestHash[3], txHashBad, 0, []common.Hash{txTopicA}))

	// The first transaction is valid and would be inserted; the second one
	// references block 1501 with the wrong hash. The whole save must roll back.
	err := storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
		txTestRPC(1500, goodHash, txHashGood, nil),
		txTestRPC(1501, badHash, txHashBad, nil),
	})
	require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)

	got, err := storage.GetTransaction(nil, txHashGood, goodHash)
	require.NoError(t, err)
	require.Nil(t, got, "first insert must have been rolled back")
}

func TestStorage_TransactionOrphanRejectionAndStaleMissing(t *testing.T) {
	storage := newStorageForTest(t, nil)
	canonicalHash := exampleTestHash[0]
	orphanHash := exampleTestHash[5]
	txHash := exampleTestHash[1]
	txHashStale := exampleTestHash[2]

	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, 1500, canonicalHash, txHash, 0, []common.Hash{txTopicA}),
		txTestLog(exampleAddr1, 1501, exampleTestHash[3], txHashStale, 0, []common.Hash{txTopicA}))

	// Insert an orphan row (block hash not present in blocks) directly.
	_, err := storage.db.Exec(`INSERT INTO transactions
		(tx_hash, block_hash, block_number, tx_index, from_address, to_address, input, raw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		txHash.Hex(), orphanHash.Hex(), 1500, 0, exampleAddr1.Hex(), nil, nil,
		`{"from":"`+exampleAddr1.Hex()+`","hash":"`+txHash.Hex()+`"}`)
	require.NoError(t, err)

	// Orphan rows are never returned by canonical reads.
	got, err := storage.GetTransaction(nil, txHash, orphanHash)
	require.NoError(t, err)
	require.Nil(t, got)

	// A stale inclusion stored under a non-canonical block hash does not satisfy
	// the canonical log: the transaction is still reported as missing.
	_, err = storage.db.Exec(`INSERT INTO transactions
		(tx_hash, block_hash, block_number, tx_index, from_address, to_address, input, raw)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		txHashStale.Hex(), orphanHash.Hex(), 1501, 0, exampleAddr1.Hex(), nil, nil,
		`{"from":"`+exampleAddr1.Hex()+`","hash":"`+txHashStale.Hex()+`"}`)
	require.NoError(t, err)

	missing, err := storage.MissingTransactionsForLogQuery(nil,
		mdrtypes.NewLogQuery(1500, 1501, nil), nil)
	require.NoError(t, err)
	require.Contains(t, missing, txHashStale)
}

func TestStorage_MissingTransactionsForLogQuery(t *testing.T) {
	storage := newStorageForTest(t, nil)

	// addr1 has two logs: one with its transaction stored (topic A) and one
	// topicless log with no transaction. addr2 has a topic B log with no tx.
	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, 100, exampleTestHash[0], exampleTestHash[5], 0, []common.Hash{txTopicA}),
		txTestLog(exampleAddr2, 101, exampleTestHash[1], exampleTestHash[6], 0, []common.Hash{txTopicB}),
		txTestLog(exampleAddr1, 102, exampleTestHash[2], exampleTestHash[7], 0, nil),
	)
	require.NoError(t, storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
		txTestRPC(100, exampleTestHash[0], exampleTestHash[5], nil),
	}))

	t.Run("all logs, no topic filter", func(t *testing.T) {
		missing, err := storage.MissingTransactionsForLogQuery(nil,
			mdrtypes.NewLogQuery(100, 200, nil), nil)
		require.NoError(t, err)
		require.Equal(t, []common.Hash{exampleTestHash[6], exampleTestHash[7]}, missing)
	})

	t.Run("address filter", func(t *testing.T) {
		missing, err := storage.MissingTransactionsForLogQuery(nil,
			mdrtypes.NewLogQuery(100, 200, []common.Address{exampleAddr2}), nil)
		require.NoError(t, err)
		require.Equal(t, []common.Hash{exampleTestHash[6]}, missing)
	})

	t.Run("topic0 filter excludes topicless logs", func(t *testing.T) {
		missing, err := storage.MissingTransactionsForLogQuery(nil,
			mdrtypes.NewLogQuery(100, 200, []common.Address{exampleAddr1}), []common.Hash{txTopicA})
		require.NoError(t, err)
		require.Empty(t, missing)
	})

	t.Run("topic0 filter with no matching logs is safe", func(t *testing.T) {
		missing, err := storage.MissingTransactionsForLogQuery(nil,
			mdrtypes.NewLogQuery(100, 200, []common.Address{exampleAddr1}), []common.Hash{txTopicC})
		require.NoError(t, err)
		require.Empty(t, missing)
	})

	t.Run("or-ed topic0 filter", func(t *testing.T) {
		missing, err := storage.MissingTransactionsForLogQuery(nil,
			mdrtypes.NewLogQuery(100, 200, nil), []common.Hash{txTopicA, txTopicB})
		require.NoError(t, err)
		require.Equal(t, []common.Hash{exampleTestHash[6]}, missing)
	})

	t.Run("empty interval has no gaps", func(t *testing.T) {
		missing, err := storage.MissingTransactionsForLogQuery(nil,
			mdrtypes.NewLogQuery(1000, 1100, nil), nil)
		require.NoError(t, err)
		require.Empty(t, missing)
	})
}

func TestStorage_TransactionAvailabilityForLogQuery(t *testing.T) {
	storage := newStorageForTest(t, nil)
	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, 100, exampleTestHash[0], exampleTestHash[5], 0, []common.Hash{txTopicA}),
		txTestLog(exampleAddr1, 101, exampleTestHash[1], exampleTestHash[6], 0, []common.Hash{txTopicB}),
	)
	require.NoError(t, storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
		txTestRPC(100, exampleTestHash[0], exampleTestHash[5], nil),
	}))
	setSyncedCoverageForTest(t, storage, exampleAddr1, 100, 200)

	// Gap on topic B: covered range, but a relevant log misses its tx.
	av, err := storage.TransactionAvailabilityForLogQuery(nil,
		mdrtypes.NewLogQuery(100, 200, []common.Address{exampleAddr1}), nil)
	require.NoError(t, err)
	require.True(t, av.LogCoverage)
	require.False(t, av.Complete)
	require.Equal(t, []common.Hash{exampleTestHash[6]}, av.MissingTxHashes)

	// Toggling the requirement to the topic that is already covered reports
	// completion without any cache invalidation.
	av, err = storage.TransactionAvailabilityForLogQuery(nil,
		mdrtypes.NewLogQuery(100, 200, []common.Address{exampleAddr1}), []common.Hash{txTopicA})
	require.NoError(t, err)
	require.True(t, av.LogCoverage)
	require.True(t, av.Complete)
	require.Empty(t, av.MissingTxHashes)
}

// TestStorage_TransactionAvailabilityCoverage pins the log-coverage semantics
// of TransactionAvailabilityForLogQuery: completeness requires the requested
// addresses/range to be covered, not merely an absence of missing rows.
func TestStorage_TransactionAvailabilityCoverage(t *testing.T) {
	t.Run("zero logs uncovered is not complete", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		// No sync_status coverage at all.
		av, err := storage.TransactionAvailabilityForLogQuery(nil,
			mdrtypes.NewLogQuery(100, 200, []common.Address{exampleAddr1}), nil)
		require.NoError(t, err)
		require.False(t, av.LogCoverage)
		require.False(t, av.Complete)
		require.Empty(t, av.MissingTxHashes)
	})

	t.Run("covered with no pertinent logs is complete", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		setSyncedCoverageForTest(t, storage, exampleAddr1, 100, 200)

		av, err := storage.TransactionAvailabilityForLogQuery(nil,
			mdrtypes.NewLogQuery(150, 160, []common.Address{exampleAddr1}), nil)
		require.NoError(t, err)
		require.True(t, av.LogCoverage)
		require.True(t, av.Complete)
		require.Empty(t, av.MissingTxHashes)
	})

	t.Run("partial query is not complete", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		setSyncedCoverageForTest(t, storage, exampleAddr1, 100, 150)

		av, err := storage.TransactionAvailabilityForLogQuery(nil,
			mdrtypes.NewLogQuery(100, 200, []common.Address{exampleAddr1}), nil)
		require.NoError(t, err)
		require.False(t, av.LogCoverage)
		require.False(t, av.Complete)
	})

	t.Run("unknown address is not complete", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		setSyncedCoverageForTest(t, storage, exampleAddr1, 100, 200)

		av, err := storage.TransactionAvailabilityForLogQuery(nil,
			mdrtypes.NewLogQuery(100, 200, []common.Address{exampleAddr2}), nil)
		require.NoError(t, err)
		require.False(t, av.LogCoverage)
		require.False(t, av.Complete)
	})

	t.Run("empty addresses means all registered", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		setSyncedCoverageForTest(t, storage, exampleAddr1, 100, 200)

		av, err := storage.TransactionAvailabilityForLogQuery(nil,
			mdrtypes.NewLogQuery(100, 200, nil), nil)
		require.NoError(t, err)
		require.True(t, av.LogCoverage)
		require.True(t, av.Complete)
	})

	t.Run("empty addresses with no registered coverage is not complete", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		av, err := storage.TransactionAvailabilityForLogQuery(nil,
			mdrtypes.NewLogQuery(100, 200, nil), nil)
		require.NoError(t, err)
		require.False(t, av.LogCoverage)
		require.False(t, av.Complete)
	})
}

// TestStorage_TransactionAvailabilityBlockHashCoverage pins that a block-hash
// query requires both the canonical header to match and the address/height to be
// covered.
func TestStorage_TransactionAvailabilityBlockHashCoverage(t *testing.T) {
	storage := newStorageForTest(t, nil)
	blockHash := exampleTestHash[0]
	otherHash := exampleTestHash[1]
	txHash := exampleTestHash[5]
	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, 100, blockHash, txHash, 0, []common.Hash{txTopicA}))
	require.NoError(t, storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
		txTestRPC(100, blockHash, txHash, nil),
	}))
	setSyncedCoverageForTest(t, storage, exampleAddr1, 100, 200)

	// Canonical header matches and the address/height is covered.
	av, err := storage.TransactionAvailabilityForLogQuery(nil,
		mdrtypes.NewLogQueryBlockHash(100, blockHash, []common.Address{exampleAddr1}), nil)
	require.NoError(t, err)
	require.True(t, av.LogCoverage)
	require.True(t, av.Complete)

	// Unknown (non-canonical) hash is not covered.
	av, err = storage.TransactionAvailabilityForLogQuery(nil,
		mdrtypes.NewLogQueryBlockHash(100, otherHash, []common.Address{exampleAddr1}), nil)
	require.NoError(t, err)
	require.False(t, av.LogCoverage)
	require.False(t, av.Complete)

	// Unknown address is not covered even with a matching canonical header.
	av, err = storage.TransactionAvailabilityForLogQuery(nil,
		mdrtypes.NewLogQueryBlockHash(100, blockHash, []common.Address{exampleAddr2}), nil)
	require.NoError(t, err)
	require.False(t, av.LogCoverage)
	require.False(t, av.Complete)

	// A height outside the covered range is not covered.
	av, err = storage.TransactionAvailabilityForLogQuery(nil,
		mdrtypes.NewLogQueryBlockHash(300, blockHash, []common.Address{exampleAddr1}), nil)
	require.NoError(t, err)
	require.False(t, av.LogCoverage)
}

// TestStorage_TransactionAvailabilityReorgCoverage pins that a reorg shrinks
// the persisted coverage so an old query stops being complete once the reorg is
// committed, while a rolled back reorg restores it.
func TestStorage_TransactionAvailabilityReorgCoverage(t *testing.T) {
	storage := newStorageForTest(t, nil)
	setSyncedCoverageForTest(t, storage, exampleAddr1, 100, 200)
	query := mdrtypes.NewLogQuery(100, 200, []common.Address{exampleAddr1})

	// Baseline: fully covered.
	av, err := storage.TransactionAvailabilityForLogQuery(nil, query, nil)
	require.NoError(t, err)
	require.True(t, av.Complete)

	reorgData := mdrtypes.ReorgData{
		BlockRangeAffected:        aggkitcommon.NewBlockRange(150, 150),
		DetectedAtBlock:           300,
		DetectedTimestamp:         1630003000,
		NetworkLatestBlock:        400,
		NetworkFinalizedBlock:     50,
		NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
	}

	// Uncommitted reorg: the coverage shrinks inside the transaction and the
	// old query is no longer complete.
	tx, err := storage.NewTx(t.Context())
	require.NoError(t, err)
	_, err = storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
	require.NoError(t, err)
	av, err = storage.TransactionAvailabilityForLogQuery(tx, query, nil)
	require.NoError(t, err)
	require.False(t, av.LogCoverage)
	require.False(t, av.Complete)
	require.NoError(t, tx.Rollback())

	// Rollback restores the coverage, so the old query is complete again.
	av, err = storage.TransactionAvailabilityForLogQuery(nil, query, nil)
	require.NoError(t, err)
	require.True(t, av.Complete)

	// Committed reorg keeps the shrunken coverage.
	tx, err = storage.NewTx(t.Context())
	require.NoError(t, err)
	_, err = storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	av, err = storage.TransactionAvailabilityForLogQuery(nil, query, nil)
	require.NoError(t, err)
	require.False(t, av.LogCoverage)
	require.False(t, av.Complete)
}

func TestStorage_ReorgRemovesCanonicalTransactions(t *testing.T) {
	storage := newStorageForTest(t, nil)
	blockNumber := uint64(1500)
	blockHash := exampleTestHash[0]
	txHash := exampleTestHash[1]
	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, blockNumber, blockHash, txHash, 0, []common.Hash{txTopicA}))
	require.NoError(t, storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
		txTestRPC(blockNumber, blockHash, txHash, nil),
	}))

	reorgData := mdrtypes.ReorgData{
		BlockRangeAffected:        aggkitcommon.NewBlockRange(blockNumber, blockNumber),
		DetectedAtBlock:           1600,
		DetectedTimestamp:         1630003000,
		NetworkLatestBlock:        1700,
		NetworkFinalizedBlock:     1400,
		NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
	}

	// A rolled back reorg must restore the inclusions.
	tx, err := storage.NewTx(t.Context())
	require.NoError(t, err)
	_, err = storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	got, err := storage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.NotNil(t, got, "rollback must restore the canonical inclusion")

	// A committed reorg purges the canonical inclusion atomically with the
	// block deletion.
	tx, err = storage.NewTx(t.Context())
	require.NoError(t, err)
	_, err = storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	got, err = storage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.Nil(t, got, "committed reorg must purge the canonical inclusion")
}

func TestStorage_Migration0003PreservesExistingData(t *testing.T) {
	logger := log.WithFields("module", "test")
	dbPath := path.Join(t.TempDir(), "migration.sqlite")

	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	// Simulate an old database with only migrations 0001 and 0002 applied. Use a
	// capped slice so RunMigrationsDB cannot append into (and overwrite) the
	// shared global Migrations backing array.
	require.NoError(t, db.RunMigrationsDB(logger, database, migrations.Migrations[0:2:2]))

	var tableName string
	err = database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='transactions'").Scan(&tableName)
	require.Error(t, err, "transactions table must not exist before 0003")

	oldStorage := &MultidownloaderStorage{db: database, logger: logger}
	logs := []ethtypes.Log{
		txTestLog(exampleAddr1, 1500, exampleTestHash[0], exampleTestHash[1], 0, []common.Hash{txTopicA}),
	}
	require.NoError(t, oldStorage.SaveEthLogs(nil, logs, true))
	require.NoError(t, database.Close())

	// Reopening runs the additive migration 0003.
	newStorage, err := NewMultidownloaderStorage(logger, MultidownloaderStorageConfig{DBPath: dbPath})
	require.NoError(t, err)

	read, err := newStorage.GetEthLogs(nil, mdrtypes.NewLogQuery(1400, 1600, []common.Address{exampleAddr1}))
	require.NoError(t, err)
	require.Equal(t, logs, read, "existing logs must be preserved by the additive migration")

	var count int
	require.NoError(t, newStorage.db.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&count))
	require.Zero(t, count, "new transactions table must exist and be empty")
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

// TestStorage_SaveTransactionsNilTxDoesNotBlockIngestion is a deterministic
// regression test for the lock-ordering deadlock. With the SQLite
// `_txlock=immediate` DSN, SaveTransactions(nil) used to hold the storage mutex
// while its BEGIN IMMEDIATE waited for the SQLite writer lock. An in-flight
// ingestion transaction that already held the writer lock could then never
// acquire the storage mutex, stalling every log write until the busy timeout.
// The cache write must instead wait for the writer lock WITHOUT holding the
// storage mutex, so ingestion keeps flowing and commits.
func TestStorage_SaveTransactionsNilTxDoesNotBlockIngestion(t *testing.T) {
	storage := newStorageForTestWithBusyTimeout(t, 2000)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	blockNumber := uint64(1500)
	blockHash := exampleTestHash[0]
	txHash := exampleTestHash[1]
	nextBlockNumber := blockNumber + 1
	nextBlockHash := exampleTestHash[2]

	// A sync_status row so the ingestion transaction can update it later.
	require.NoError(t, storage.UpsertSyncerConfigs(nil, []mdrtypes.ContractConfig{
		{
			Address:   exampleAddr1,
			FromBlock: blockNumber,
			ToBlock:   aggkittypes.LatestBlock,
			Syncers:   []mdrtypes.SyncerID{"test"},
		},
	}))

	// 1) Begin the ingestion transaction first: BEGIN IMMEDIATE takes the SQLite
	// writer lock and holds it for the whole scenario.
	ingestionTx, err := storage.NewTx(ctx)
	require.NoError(t, err)
	defer func() { _ = ingestionTx.Rollback() }()

	// 2) Persist the canonical block + log inside the ingestion transaction so
	// the cache write below has a canonical inclusion to validate against.
	require.NoError(t, storage.SaveEthLogsWithHeaders(ingestionTx,
		[]*aggkittypes.BlockHeader{
			aggkittypes.NewBlockHeader(blockNumber, blockHash, 1630000000, nil),
		},
		[]ethtypes.Log{txTestLog(exampleAddr1, blockNumber, blockHash, txHash, 0, []common.Hash{txTopicA})},
		true))

	// 3) Launch the cache write with a nil tx. It must block on the writer lock
	// (held by the ingestion transaction) without holding the storage mutex.
	cacheStarted := make(chan struct{})
	cacheDone := make(chan error, 1)
	go func() {
		close(cacheStarted)
		cacheDone <- storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
			txTestRPC(blockNumber, blockHash, txHash, nil),
		})
	}()
	<-cacheStarted

	// Prove the cache write is waiting without the storage mutex: for a bounded
	// window the mutex must stay acquirable and the cache write must not have
	// completed (the writer lock is still held by ingestion).
	assertStorageMutexFreeWhileBlocked(t, storage, cacheDone, 250*time.Millisecond)

	// 4) The ingestion transaction must be able to keep writing and commit while
	// the cache write waits. With the old ordering this would deadlock until the
	// busy timeout.
	ingestionDone := make(chan error, 1)
	go func() {
		err := storage.SaveEthLogsWithHeaders(ingestionTx,
			[]*aggkittypes.BlockHeader{
				aggkittypes.NewBlockHeader(nextBlockNumber, nextBlockHash, 1630000060, &blockHash),
			},
			[]ethtypes.Log{
				txTestLog(exampleAddr1, nextBlockNumber, nextBlockHash, exampleTestHash[3], 0,
					[]common.Hash{txTopicA}),
			},
			true)
		if err == nil {
			err = storage.UpdateSyncedStatus(ingestionTx, []mdrtypes.SyncSegment{
				{
					ContractAddr:  exampleAddr1,
					TargetToBlock: aggkittypes.LatestBlock,
					BlockRange:    aggkitcommon.NewBlockRange(blockNumber, nextBlockNumber),
				},
			})
		}
		if err == nil {
			err = ingestionTx.Commit()
		}
		ingestionDone <- err
	}()

	select {
	case err := <-ingestionDone:
		require.NoError(t, err, "ingestion must not be blocked by the cache write")
	case <-ctx.Done():
		t.Fatal("ingestion blocked: SaveTransactions(nil) held the storage mutex while waiting for the writer lock")
	}

	// 5) Releasing the writer lock lets the cache write finish.
	select {
	case err := <-cacheDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("cache write did not complete after the ingestion transaction committed")
	}

	// The cached transaction is now visible and canonical.
	got, err := storage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.NotNil(t, got)
}

// TestStorage_NewTxDoesNotBlockIngestion is the reverse-direction companion to
// TestStorage_SaveTransactionsNilTxDoesNotBlockIngestion. NewTx also issues
// BEGIN IMMEDIATE, so if it held the storage mutex while waiting for the SQLite
// writer lock it would deadlock against an in-flight writer that already owns
// the writer lock: the writer would block on the storage mutex while NewTx
// blocks on the writer lock, stalling until the busy timeout. NewTx must begin
// its transaction WITHOUT the storage mutex, so the existing writer keeps
// flowing and commits, after which NewTx proceeds.
func TestStorage_NewTxDoesNotBlockIngestion(t *testing.T) {
	storage := newStorageForTestWithBusyTimeout(t, 2000)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	blockNumber := uint64(1600)
	blockHash := exampleTestHash[0]
	txHash := exampleTestHash[1]

	// 1) An existing writer takes the SQLite writer lock first, begun directly
	// (BEGIN IMMEDIATE) without going through NewTx.
	writerTx, err := storage.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = writerTx.Rollback() }()

	// 2) NewTx must block in its own BEGIN IMMEDIATE, waiting for the writer
	// lock, WITHOUT holding the storage mutex.
	newTxDone := make(chan error, 1)
	go func() {
		tx, err := storage.NewTx(ctx)
		if err != nil {
			newTxDone <- err
			return
		}
		newTxDone <- nil
		_ = tx.Rollback()
	}()

	// Prove NewTx waits without the storage mutex and does not complete early
	// (the writer lock is still held by writerTx).
	assertStorageMutexFreeWhileBlocked(t, storage, newTxDone, 250*time.Millisecond)

	// 3) The existing writer must be able to keep writing and commit while NewTx
	// waits. With the old ordering this would deadlock until the busy timeout.
	require.NoError(t, storage.SaveEthLogsWithHeaders(writerTx,
		[]*aggkittypes.BlockHeader{
			aggkittypes.NewBlockHeader(blockNumber, blockHash, 1630000000, nil),
		},
		[]ethtypes.Log{txTestLog(exampleAddr1, blockNumber, blockHash, txHash, 0, []common.Hash{txTopicA})},
		true))
	require.NoError(t, storage.SaveTransactions(writerTx, []aggkittypes.RPCTransaction{
		txTestRPC(blockNumber, blockHash, txHash, nil),
	}))

	writerDone := make(chan error, 1)
	go func() { writerDone <- writerTx.Commit() }()
	select {
	case err := <-writerDone:
		require.NoError(t, err, "existing writer must not be blocked by NewTx")
	case <-ctx.Done():
		t.Fatal("writer blocked: NewTx held the storage mutex while waiting for the writer lock")
	}

	// 4) Releasing the writer lock lets the waiting NewTx begin and roll back.
	select {
	case err := <-newTxDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("NewTx did not begin after the existing writer committed")
	}

	// The writer's committed state is intact and canonical.
	got, err := storage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.NotNil(t, got)
}

// TestStorage_SaveTransactionsAndNewTxConcurrent pins that concurrent
// self-owned cache saves (BEGIN IMMEDIATE before the storage mutex) and NewTx
// transactions (BEGIN IMMEDIATE without the storage mutex) interleave without
// deadlocking or stalling on the busy timeout, in either direction.
func TestStorage_SaveTransactionsAndNewTxConcurrent(t *testing.T) {
	storage := newStorageForTestWithBusyTimeout(t, 10000)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	blockNumber := uint64(3100)
	blockHash := exampleTestHash[0]
	txHash := exampleTestHash[1]
	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, blockNumber, blockHash, txHash, 0, []common.Hash{txTopicA}))

	const workers = 8
	errs := make(chan error, workers*2)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs <- storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
				txTestRPC(blockNumber, blockHash, txHash, nil),
			})
		}()
		go func() {
			defer wg.Done()
			tx, err := storage.NewTx(ctx)
			if err != nil {
				errs <- err
				return
			}
			errs <- tx.Rollback()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	byBlock, err := storage.GetTransactionsByBlock(nil, blockNumber, blockHash)
	require.NoError(t, err)
	require.Len(t, byBlock, 1)
}

// TestStorage_SaveTransactionsCallerOwnedTxReorgSafe pins that SaveTransactions
// accepts a caller-owned transaction and that the caller owns atomicity. The
// inclusion is inserted and then reorged inside the same caller-owned
// transaction; rolling back discards both the INSERT and the reorg DELETE, so
// the persisted state returns to its pre-transaction state.
func TestStorage_SaveTransactionsCallerOwnedTxReorgSafe(t *testing.T) {
	storage := newStorageForTest(t, nil)
	blockNumber := uint64(2000)
	blockHash := exampleTestHash[0]
	txHash := exampleTestHash[1]
	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, blockNumber, blockHash, txHash, 0, []common.Hash{txTopicA}))

	reorgData := mdrtypes.ReorgData{
		BlockRangeAffected:        aggkitcommon.NewBlockRange(blockNumber, blockNumber),
		DetectedAtBlock:           blockNumber + 1,
		DetectedTimestamp:         1630003000,
		NetworkLatestBlock:        blockNumber + 2,
		NetworkFinalizedBlock:     blockNumber - 100,
		NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
	}

	// Save through a caller-owned tx. The caller sees its own uncommitted
	// inclusion, but it is never committed.
	tx, err := storage.NewTx(t.Context())
	require.NoError(t, err)
	require.NoError(t, storage.SaveTransactions(tx, []aggkittypes.RPCTransaction{
		txTestRPC(blockNumber, blockHash, txHash, nil),
	}))
	inTx, err := storage.GetTransaction(tx, txHash, blockHash)
	require.NoError(t, err)
	require.NotNil(t, inTx, "caller-owned tx must observe its own uncommitted inclusion")

	_, err = storage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())

	// Rolling back discards the INSERT together with the reorg DELETE, so the
	// original pre-transaction state is restored: no transaction row was ever
	// persisted (transactions=0) and the original block stays canonical
	// (blocks=1). This is ordinary SQL rollback semantics, not a lost write.
	got, err := storage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.Nil(t, got, "rolled-back caller-owned tx must not leak its inclusion")

	var txCount, blockCount int
	require.NoError(t, storage.db.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&txCount))
	require.NoError(t, storage.db.QueryRow("SELECT COUNT(*) FROM blocks").Scan(&blockCount))
	require.Zero(t, txCount, "rollback must discard the uncommitted INSERT")
	require.Equal(t, 1, blockCount, "the original pre-transaction block must remain")
}

// TestStorage_SaveTransactionsConcurrentNilTx pins that many concurrent
// self-owned saves are serialized without deadlocking (each BEGIN IMMEDIATE is
// taken before the storage mutex) and stay idempotent per (tx_hash, block_hash).
func TestStorage_SaveTransactionsConcurrentNilTx(t *testing.T) {
	storage := newStorageForTestWithBusyTimeout(t, 5000)
	blockNumber := uint64(3000)
	blockHash := exampleTestHash[0]
	txHash := exampleTestHash[1]
	saveLogsForTest(t, storage,
		txTestLog(exampleAddr1, blockNumber, blockHash, txHash, 0, []common.Hash{txTopicA}))

	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- storage.SaveTransactions(nil, []aggkittypes.RPCTransaction{
				txTestRPC(blockNumber, blockHash, txHash, nil),
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	byBlock, err := storage.GetTransactionsByBlock(nil, blockNumber, blockHash)
	require.NoError(t, err)
	require.Len(t, byBlock, 1)
}
