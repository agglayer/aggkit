package storage

import (
	"testing"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// idempotencyLog builds a minimal real eth log for the given block/address. It
// avoids any production RPC: the fixtures only exercise the storage SQL paths.
func idempotencyLog(addr common.Address, blockNumber uint64, blockHash common.Hash,
	timestamp uint64, logIndex uint) types.Log {
	return types.Log{
		Address:        addr,
		BlockNumber:    blockNumber,
		BlockHash:      blockHash,
		BlockTimestamp: timestamp,
		Topics:         []common.Hash{exampleTestHash[6]},
		Data:           []byte{0x01, byte(logIndex)},
		TxHash:         exampleTestHash[7],
		TxIndex:        logIndex,
		Index:          logIndex,
	}
}

// TestStorage_SaveBlocksSecondAddressLogIdempotent is the real-world case: the
// same block header is saved once per address whose logs land in it. The
// second save collides on the blocks primary key and must be treated as a
// benign idempotent replay (one header, both logs).
func TestStorage_SaveBlocksSecondAddressLogIdempotent(t *testing.T) {
	storage := newStorageForTest(t, nil)
	const blockNumber = uint64(1000)
	blockHash := exampleTestHash[0]
	header := aggkittypes.NewBlockHeader(blockNumber, blockHash, 1630000000, &exampleTestHash[1])

	require.NoError(t, storage.SaveEthLogsWithHeaders(nil, aggkittypes.ListBlockHeaders{header},
		[]types.Log{idempotencyLog(exampleAddr1, blockNumber, blockHash, 1630000000, 0)}, true))

	// Same header, a second address's log: hits the blocks PK constraint.
	require.NoError(t, storage.SaveEthLogsWithHeaders(nil, aggkittypes.ListBlockHeaders{header},
		[]types.Log{idempotencyLog(exampleAddr2, blockNumber, blockHash, 1630000000, 1)}, true))

	readHeader, isFinal, err := storage.GetBlockHeaderByNumber(nil, blockNumber)
	require.NoError(t, err)
	require.NotNil(t, readHeader)
	require.Equal(t, blockHash, readHeader.Hash)
	require.Equal(t, uint64(1630000000), readHeader.Time)
	require.True(t, isFinal)

	logs, err := storage.GetEthLogs(nil,
		mdrtypes.NewLogQuery(blockNumber, blockNumber, []common.Address{exampleAddr1, exampleAddr2}))
	require.NoError(t, err)
	require.Len(t, logs, 2, "both addresses' logs must be stored under the single header")

	var blockCount int
	require.NoError(t, storage.db.QueryRow("SELECT COUNT(*) FROM blocks").Scan(&blockCount))
	require.Equal(t, 1, blockCount, "the header must not be duplicated")
}

// TestStorage_SaveBlocksFinalityMonotonicNoDowngrade pins that replaying a
// header promotes is_final false -> true and never downgrades it back.
func TestStorage_SaveBlocksFinalityMonotonicNoDowngrade(t *testing.T) {
	storage := newStorageForTest(t, nil)
	const blockNumber = uint64(2000)
	header := aggkittypes.NewBlockHeader(blockNumber, exampleTestHash[0], 1630000000, &exampleTestHash[1])

	require.NoError(t, storage.saveAggkitBlock(nil, header, false))
	_, isFinal, err := storage.GetBlockHeaderByNumber(nil, blockNumber)
	require.NoError(t, err)
	require.False(t, isFinal)

	// Replay as final: promote.
	require.NoError(t, storage.saveAggkitBlock(nil, header, true))
	_, isFinal, err = storage.GetBlockHeaderByNumber(nil, blockNumber)
	require.NoError(t, err)
	require.True(t, isFinal, "finality must be promoted on replay")

	// Replay as non-final: must not downgrade.
	require.NoError(t, storage.saveAggkitBlock(nil, header, false))
	_, isFinal, err = storage.GetBlockHeaderByNumber(nil, blockNumber)
	require.NoError(t, err)
	require.True(t, isFinal, "finality must never be downgraded")
}

// TestStorage_SaveBlocksMetadataMergeAndConflict pins that unknown metadata is
// merged in and contradictory known metadata is rejected without overwriting.
func TestStorage_SaveBlocksMetadataMergeAndConflict(t *testing.T) {
	storage := newStorageForTest(t, nil)
	const blockNumber = uint64(3000)
	blockHash := exampleTestHash[0]
	parentHash := exampleTestHash[1]

	// Unknown timestamp (0) and nil parent, as an ethLogs-derived header.
	require.NoError(t, storage.saveAggkitBlock(nil,
		aggkittypes.NewBlockHeader(blockNumber, blockHash, 0, nil), false))

	// Enrich with known timestamp and parent.
	require.NoError(t, storage.saveAggkitBlock(nil,
		aggkittypes.NewBlockHeader(blockNumber, blockHash, 1630009999, &parentHash), false))

	readHeader, _, err := storage.GetBlockHeaderByNumber(nil, blockNumber)
	require.NoError(t, err)
	require.NotNil(t, readHeader)
	require.Equal(t, uint64(1630009999), readHeader.Time, "unknown timestamp must be enriched")
	require.NotNil(t, readHeader.ParentHash)
	require.Equal(t, parentHash, *readHeader.ParentHash, "unknown parent must be enriched")

	// Contradicting known timestamp: explicit integrity conflict, no overwrite.
	err = storage.saveAggkitBlock(nil,
		aggkittypes.NewBlockHeader(blockNumber, blockHash, 12345, &parentHash), false)
	require.ErrorIs(t, err, ErrBlockHeaderIntegrityConflict)

	// Contradicting known parent: explicit integrity conflict, no overwrite.
	otherParent := exampleTestHash[2]
	err = storage.saveAggkitBlock(nil,
		aggkittypes.NewBlockHeader(blockNumber, blockHash, 1630009999, &otherParent), false)
	require.ErrorIs(t, err, ErrBlockHeaderIntegrityConflict)

	readHeader, _, err = storage.GetBlockHeaderByNumber(nil, blockNumber)
	require.NoError(t, err)
	require.NotNil(t, readHeader)
	require.Equal(t, uint64(1630009999), readHeader.Time, "conflicting timestamp must not overwrite")
	require.NotNil(t, readHeader.ParentHash)
	require.Equal(t, parentHash, *readHeader.ParentHash, "conflicting parent must not overwrite")
}

// TestStorage_SaveBlocksHashMismatchTypedReorgError pins that a same-number
// different-hash header yields the typed DetectedReorgError and never overwrites
// the stored header or its logs.
func TestStorage_SaveBlocksHashMismatchTypedReorgError(t *testing.T) {
	storage := newStorageForTest(t, nil)
	const blockNumber = uint64(4000)
	storedHash := exampleTestHash[0]
	incomingHash := exampleTestHash[5]

	require.NoError(t, storage.SaveEthLogsWithHeaders(nil,
		aggkittypes.ListBlockHeaders{aggkittypes.NewBlockHeader(blockNumber, storedHash, 1630000000, &exampleTestHash[1])},
		[]types.Log{idempotencyLog(exampleAddr1, blockNumber, storedHash, 1630000000, 0)}, true))

	err := storage.saveAggkitBlock(nil,
		aggkittypes.NewBlockHeader(blockNumber, incomingHash, 1630000000, &exampleTestHash[1]), true)
	require.Error(t, err)
	var reorgErr *mdrtypes.DetectedReorgError
	require.ErrorAs(t, err, &reorgErr)
	require.Equal(t, blockNumber, reorgErr.OffendingBlockNumber)
	require.Equal(t, mdrtypes.ReorgDetectionReason_BlockHashMismatch, reorgErr.ReorgDetectionReason)
	require.Equal(t, storedHash, reorgErr.OldHash)
	require.Equal(t, incomingHash, reorgErr.NewHash)

	// Stored header and logs preserved, never overwritten.
	stored, _, err := storage.GetBlockHeaderByNumber(nil, blockNumber)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, storedHash, stored.Hash)
	logs, err := storage.GetEthLogs(nil,
		mdrtypes.NewLogQuery(blockNumber, blockNumber, []common.Address{exampleAddr1}))
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, storedHash, logs[0].BlockHash)
}

// TestStorage_SaveBlocksHashMismatchRollsBackCallerTx pins that the conflict
// error propagates out of a caller-owned transaction, so the whole batch
// (including unrelated inserts staged before the conflict) rolls back atomically.
func TestStorage_SaveBlocksHashMismatchRollsBackCallerTx(t *testing.T) {
	storage := newStorageForTest(t, nil)
	const canonicalBlock = uint64(5000)
	canonicalHash := exampleTestHash[0]
	require.NoError(t, storage.SaveEthLogsWithHeaders(nil,
		aggkittypes.ListBlockHeaders{
			aggkittypes.NewBlockHeader(canonicalBlock, canonicalHash, 1630000000, &exampleTestHash[1]),
		},
		[]types.Log{idempotencyLog(exampleAddr1, canonicalBlock, canonicalHash, 1630000000, 0)}, true))

	tx, err := storage.NewTx(t.Context())
	require.NoError(t, err)

	// Stage a different block + log inside the caller-owned tx.
	const stagedBlock = canonicalBlock + 1
	stagedHash := exampleTestHash[2]
	require.NoError(t, storage.SaveEthLogsWithHeaders(tx,
		aggkittypes.ListBlockHeaders{aggkittypes.NewBlockHeader(stagedBlock, stagedHash, 1630000060, &canonicalHash)},
		[]types.Log{idempotencyLog(exampleAddr1, stagedBlock, stagedHash, 1630000060, 0)}, false))

	// Then hit a hash mismatch for the already-stored canonical block.
	err = storage.saveAggkitBlock(tx,
		aggkittypes.NewBlockHeader(canonicalBlock, exampleTestHash[5], 1630000000, &exampleTestHash[1]), true)
	require.Error(t, err)
	var reorgErr *mdrtypes.DetectedReorgError
	require.ErrorAs(t, err, &reorgErr)
	require.Equal(t, canonicalBlock, reorgErr.OffendingBlockNumber)

	require.NoError(t, tx.Rollback())

	// The staged block + log were discarded with the rollback.
	staged, _, err := storage.GetBlockHeaderByNumber(nil, stagedBlock)
	require.NoError(t, err)
	require.Nil(t, staged, "staged block must be rolled back")
	logs, err := storage.GetEthLogs(nil,
		mdrtypes.NewLogQuery(stagedBlock, stagedBlock, []common.Address{exampleAddr1}))
	require.NoError(t, err)
	require.Empty(t, logs, "staged log must be rolled back")

	// The canonical block remains untouched.
	canonical, _, err := storage.GetBlockHeaderByNumber(nil, canonicalBlock)
	require.NoError(t, err)
	require.NotNil(t, canonical)
	require.Equal(t, canonicalHash, canonical.Hash)
}
