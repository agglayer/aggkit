package storage

import (
	"testing"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// countStorageRowsForTest returns the number of rows in one of the storage
// tables. It is used to prove that a rejected batch performs no write at all.
func countStorageRowsForTest(t *testing.T, storage *MultidownloaderStorage, table string) int {
	t.Helper()
	var n int
	require.NoError(t, storage.db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

// TestStorage_SaveEthLogsWithHeadersRejectsHashMismatch pins the guard that was
// missing (the old TODO in saveLogsAndBlocks): a log whose BlockHash contradicts
// the header hash for its block must be rejected with the typed sentinel, and
// nothing must be written.
func TestStorage_SaveEthLogsWithHeadersRejectsHashMismatch(t *testing.T) {
	const blockNumber = uint64(9000)
	headerHash := exampleTestHash[0]
	logHash := exampleTestHash[5]
	require.NotEqual(t, headerHash, logHash, "fixture requires two distinct hashes")

	header := aggkittypes.NewBlockHeader(blockNumber, headerHash, 1630000000, &exampleTestHash[1])
	lg := idempotencyLog(exampleAddr1, blockNumber, logHash, 1630000000, 0)

	t.Run("nil tx", func(t *testing.T) {
		storage := newStorageForTest(t, nil)

		err := storage.SaveEthLogsWithHeaders(nil,
			aggkittypes.ListBlockHeaders{header}, []types.Log{lg}, true)
		require.ErrorIs(t, err, mdrtypes.ErrLogBlockHeaderMismatch)

		require.Zero(t, countStorageRowsForTest(t, storage, "blocks"), "no block row must be written")
		require.Zero(t, countStorageRowsForTest(t, storage, "logs"), "no log row must be written")
		readLogs, err := storage.GetEthLogs(nil,
			mdrtypes.NewLogQuery(blockNumber, blockNumber, []common.Address{exampleAddr1}))
		require.NoError(t, err)
		require.Empty(t, readLogs)
	})

	t.Run("caller-owned tx", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		tx, err := storage.NewTx(t.Context())
		require.NoError(t, err)

		err = storage.SaveEthLogsWithHeaders(tx,
			aggkittypes.ListBlockHeaders{header}, []types.Log{lg}, true)
		require.ErrorIs(t, err, mdrtypes.ErrLogBlockHeaderMismatch)
		require.NoError(t, tx.Rollback())

		require.Zero(t, countStorageRowsForTest(t, storage, "blocks"))
		require.Zero(t, countStorageRowsForTest(t, storage, "logs"))
	})
}

// TestStorage_SaveEthLogsWithHeadersRejectsMissingOrNilHeader pins that a log
// whose block number has no header, or only a nil header entry, is rejected as
// a missing header without dereferencing nil and without writing anything.
func TestStorage_SaveEthLogsWithHeadersRejectsMissingOrNilHeader(t *testing.T) {
	t.Run("missing header", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		headers := aggkittypes.ListBlockHeaders{
			aggkittypes.NewBlockHeader(9001, exampleTestHash[0], 1630000000, nil),
		}
		lg := idempotencyLog(exampleAddr1, 9002, exampleTestHash[1], 1630000000, 0)

		err := storage.SaveEthLogsWithHeaders(nil, headers, []types.Log{lg}, true)
		require.ErrorIs(t, err, mdrtypes.ErrLogBlockHeaderMismatch)
		require.Zero(t, countStorageRowsForTest(t, storage, "blocks"))
		require.Zero(t, countStorageRowsForTest(t, storage, "logs"))
	})

	t.Run("nil header entry", func(t *testing.T) {
		storage := newStorageForTest(t, nil)
		headers := aggkittypes.ListBlockHeaders{nil}
		lg := idempotencyLog(exampleAddr1, 9003, exampleTestHash[1], 1630000000, 0)

		err := storage.SaveEthLogsWithHeaders(nil, headers, []types.Log{lg}, true)
		require.ErrorIs(t, err, mdrtypes.ErrLogBlockHeaderMismatch)
		require.Zero(t, countStorageRowsForTest(t, storage, "blocks"))
		require.Zero(t, countStorageRowsForTest(t, storage, "logs"))
	})
}

// TestStorage_SaveEthLogsWithHeadersRejectsInconsistentDuplicateHeaders pins
// that two headers for the same block number with different hashes are rejected
// rather than silently letting the last one win.
func TestStorage_SaveEthLogsWithHeadersRejectsInconsistentDuplicateHeaders(t *testing.T) {
	storage := newStorageForTest(t, nil)
	const blockNumber = uint64(9004)
	headers := aggkittypes.ListBlockHeaders{
		aggkittypes.NewBlockHeader(blockNumber, exampleTestHash[0], 1630000000, nil),
		aggkittypes.NewBlockHeader(blockNumber, exampleTestHash[5], 1630000000, nil),
	}
	lg := idempotencyLog(exampleAddr1, blockNumber, exampleTestHash[0], 1630000000, 0)

	err := storage.SaveEthLogsWithHeaders(nil, headers, []types.Log{lg}, true)
	require.ErrorIs(t, err, mdrtypes.ErrLogBlockHeaderMismatch)
	require.Zero(t, countStorageRowsForTest(t, storage, "blocks"))
	require.Zero(t, countStorageRowsForTest(t, storage, "logs"))
}

// TestStorage_SaveEthLogsWithHeadersRejectsWholeBatch pins atomicity: a batch
// with one valid pair and one contradictory pair writes nothing at all.
func TestStorage_SaveEthLogsWithHeadersRejectsWholeBatch(t *testing.T) {
	storage := newStorageForTest(t, nil)
	headers := aggkittypes.ListBlockHeaders{
		aggkittypes.NewBlockHeader(9005, exampleTestHash[0], 1630000000, nil),
		aggkittypes.NewBlockHeader(9006, exampleTestHash[1], 1630000060, nil),
	}
	logs := []types.Log{
		idempotencyLog(exampleAddr1, 9005, exampleTestHash[0], 1630000000, 0), // valid
		idempotencyLog(exampleAddr1, 9006, exampleTestHash[5], 1630000060, 0), // contradicts header
	}

	err := storage.SaveEthLogsWithHeaders(nil, headers, logs, true)
	require.ErrorIs(t, err, mdrtypes.ErrLogBlockHeaderMismatch)
	require.Zero(t, countStorageRowsForTest(t, storage, "blocks"), "the valid block must not be written either")
	require.Zero(t, countStorageRowsForTest(t, storage, "logs"))
}

// TestStorage_SaveEthLogsRejectsLaterMismatchInSameBlock pins that SaveEthLogs,
// which derives the headers from the logs themselves, still catches a later log
// in the same block that carries a different hash (the first log defines the
// derived header).
func TestStorage_SaveEthLogsRejectsLaterMismatchInSameBlock(t *testing.T) {
	storage := newStorageForTest(t, nil)
	const blockNumber = uint64(9007)
	logs := []types.Log{
		idempotencyLog(exampleAddr1, blockNumber, exampleTestHash[0], 1630000000, 0),
		idempotencyLog(exampleAddr1, blockNumber, exampleTestHash[5], 1630000000, 1),
	}

	err := storage.SaveEthLogs(nil, logs, true)
	require.ErrorIs(t, err, mdrtypes.ErrLogBlockHeaderMismatch)
	require.Zero(t, countStorageRowsForTest(t, storage, "blocks"))
	require.Zero(t, countStorageRowsForTest(t, storage, "logs"))
}

// TestStorage_SaveEthLogsValidBatchStillPersists is the green companion: a
// consistent batch is still persisted, so the new guard does not reject valid
// data.
func TestStorage_SaveEthLogsValidBatchStillPersists(t *testing.T) {
	storage := newStorageForTest(t, nil)
	logs := []types.Log{
		idempotencyLog(exampleAddr1, 9008, exampleTestHash[0], 1630000000, 0),
		idempotencyLog(exampleAddr1, 9008, exampleTestHash[0], 1630000000, 1),
		idempotencyLog(exampleAddr2, 9009, exampleTestHash[1], 1630000060, 0),
	}

	require.NoError(t, storage.SaveEthLogs(nil, logs, true))

	readLogs, err := storage.GetEthLogs(nil,
		mdrtypes.NewLogQuery(9008, 9009, []common.Address{exampleAddr1, exampleAddr2}))
	require.NoError(t, err)
	require.Len(t, readLogs, 3)
	require.Equal(t, 2, countStorageRowsForTest(t, storage, "blocks"))
	require.Equal(t, 3, countStorageRowsForTest(t, storage, "logs"))
}
