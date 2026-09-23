package l1infotreesync

import (
	"context"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestProcessVerifyBatchesNil(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestProcessVerifyBatchesNil.sqlite")
	sut, err := newProcessor(dbPath)
	require.NoError(t, err)
	err = sut.processVerifyBatches(nil, 1, nil)
	require.Error(t, err)
}

func TestProcessVerifyBatchesOK(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestProcessVerifyBatchesOK.sqlite")
	sut, err := newProcessor(dbPath)
	require.NoError(t, err)
	event := VerifyBatches{
		BlockPosition:  1,
		RollupID:       1,
		NumBatch:       1,
		StateRoot:      common.HexToHash("5ca1e"),
		ExitRoot:       common.HexToHash("b455"),
		Aggregator:     common.HexToAddress("beef"),
		RollupExitRoot: common.HexToHash("b455"),
	}
	ctx := context.TODO()
	tx, err := db.NewTx(ctx, sut.db)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO block (num) VALUES ($1)`, 1)
	require.NoError(t, err)
	err = sut.processVerifyBatches(tx, 1, &event)
	require.NoError(t, err)
}

func TestProcessVerifyBatchesSkip0000(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestProcessVerifyBatchesSkip0000.sqlite")
	sut, err := newProcessor(dbPath)
	require.NoError(t, err)
	event := VerifyBatches{
		BlockPosition:  1,
		RollupID:       1,
		NumBatch:       1,
		StateRoot:      common.HexToHash("5ca1e"),
		ExitRoot:       common.Hash{},
		Aggregator:     common.HexToAddress("beef"),
		RollupExitRoot: common.HexToHash("b455"),
	}
	ctx := context.TODO()
	tx, err := db.NewTx(ctx, sut.db)
	require.NoError(t, err)
	err = sut.processVerifyBatches(tx, 1, &event)
	require.NoError(t, err)
}

func TestGetVerifiedBatches(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestGetVerifiedBatches.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	// Test ErrNotFound returned correctly on all methods
	_, err = p.GetLastVerifiedBatches(0)
	require.Equal(t, db.ErrNotFound, err)
	_, err = p.GetFirstVerifiedBatches(0)
	require.Equal(t, db.ErrNotFound, err)
	_, err = p.GetFirstVerifiedBatchesAfterBlock(0, 0)
	require.Equal(t, db.ErrNotFound, err)

	// First insert
	expected1 := &VerifyBatches{
		RollupID:   420,
		NumBatch:   69,
		StateRoot:  common.HexToHash("5ca1e"),
		ExitRoot:   common.HexToHash("b455"),
		Aggregator: common.HexToAddress("beef"),
	}
	err = p.ProcessBlock(ctx, sync.Block{
		Num: 1,
		Events: []interface{}{
			Event{VerifyBatches: expected1},
		},
	})
	require.NoError(t, err)
	_, err = p.GetLastVerifiedBatches(0)
	require.Equal(t, db.ErrNotFound, err)
	actual, err := p.GetLastVerifiedBatches(420)
	require.NoError(t, err)
	require.Equal(t, expected1, actual)
	actual, err = p.GetFirstVerifiedBatches(420)
	require.NoError(t, err)
	require.Equal(t, expected1, actual)

	// Second insert
	expected2 := &VerifyBatches{
		RollupID:   420,
		NumBatch:   690,
		StateRoot:  common.HexToHash("5ca1e3"),
		ExitRoot:   common.HexToHash("ba55"),
		Aggregator: common.HexToAddress("beef3"),
	}
	err = p.ProcessBlock(ctx, sync.Block{
		Num: 2,
		Events: []interface{}{
			Event{VerifyBatches: expected2},
		},
	})
	require.NoError(t, err)
	_, err = p.GetLastVerifiedBatches(0)
	require.Equal(t, db.ErrNotFound, err)
	actual, err = p.GetLastVerifiedBatches(420)
	require.NoError(t, err)
	require.Equal(t, expected2, actual)
	actual, err = p.GetFirstVerifiedBatches(420)
	require.NoError(t, err)
	require.Equal(t, expected1, actual)
	actual, err = p.GetFirstVerifiedBatchesAfterBlock(420, 2)
	require.NoError(t, err)
	require.Equal(t, expected2, actual)
}

// TestProcessPessimisticVerifyBatches proves that a pessimistic LER update (the
// VerifyBatchesTrustedAggregator event the rollup manager emits on pessimistic verifications,
// with zero NumBatch/StateRoot) updates the rollupExitTree leaf rollupID-1 to newLocalExitRoot
// and produces a verify_batches-queryable row.
func TestProcessPessimisticVerifyBatches(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestProcessPessimisticVerifyBatches.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	const rollupID = uint32(2)
	newLocalExitRoot := common.HexToHash("0xabc123")
	pessimistic := &VerifyBatches{
		BlockPosition: 4,
		RollupID:      rollupID,
		ExitRoot:      newLocalExitRoot, // carries newLocalExitRoot; NumBatch/StateRoot stay zero
		Aggregator:    common.HexToAddress("0xdead"),
	}
	err = p.ProcessBlock(ctx, sync.Block{
		Num:    100,
		Events: []interface{}{Event{VerifyBatches: pessimistic}},
	})
	require.NoError(t, err)

	// rollupExitTree leaf rollupID-1 must equal newLocalExitRoot
	root, err := p.rollupExitTree.GetLastRoot(p.db)
	require.NoError(t, err)
	leaf, err := p.rollupExitTree.GetLeaf(p.db, rollupID-1, root.Hash)
	require.NoError(t, err)
	require.Equal(t, newLocalExitRoot, leaf)

	// The row must be queryable via verify_batches queries
	last, err := p.GetLastVerifiedBatches(rollupID)
	require.NoError(t, err)
	require.Equal(t, newLocalExitRoot, last.ExitRoot)
	require.Zero(t, last.NumBatch)
	require.Equal(t, common.Hash{}, last.StateRoot)

	rows, err := p.GetVerifiedBatchesInBlockRange(100, 100)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, rollupID, rows[0].RollupID)
	require.Equal(t, newLocalExitRoot, rows[0].ExitRoot)
}

func TestGetVerifiedBatchesInBlockRange(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestGetVerifiedBatchesInBlockRange.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	// Empty DB: empty range returns an empty slice and no error
	rows, err := p.GetVerifiedBatchesInBlockRange(0, 1000)
	require.NoError(t, err)
	require.Empty(t, rows)

	// Block 10: two rollups (zkEVM-style row for rollup 1, another for rollup 2), distinct block_pos
	zkevmR1 := &VerifyBatches{
		BlockPosition: 0,
		RollupID:      1,
		NumBatch:      42,
		StateRoot:     common.HexToHash("0x5ca1e"),
		ExitRoot:      common.HexToHash("0x111"),
		Aggregator:    common.HexToAddress("0xa1"),
	}
	zkevmR2 := &VerifyBatches{
		BlockPosition: 1,
		RollupID:      2,
		NumBatch:      7,
		StateRoot:     common.HexToHash("0x5ca1f"),
		ExitRoot:      common.HexToHash("0x222"),
		Aggregator:    common.HexToAddress("0xa2"),
	}
	require.NoError(t, p.ProcessBlock(ctx, sync.Block{
		Num:    10,
		Events: []interface{}{Event{VerifyBatches: zkevmR1}, Event{VerifyBatches: zkevmR2}},
	}))

	// Block 12: pessimistic-style row for rollup 1 (zero NumBatch/StateRoot)
	pessimisticR1 := &VerifyBatches{
		BlockPosition: 0,
		RollupID:      1,
		ExitRoot:      common.HexToHash("0x333"),
		Aggregator:    common.HexToAddress("0xa3"),
	}
	require.NoError(t, p.ProcessBlock(ctx, sync.Block{
		Num:    12,
		Events: []interface{}{Event{VerifyBatches: pessimisticR1}},
	}))

	// Block 15: pessimistic-style row for rollup 3
	pessimisticR3 := &VerifyBatches{
		BlockPosition: 0,
		RollupID:      3,
		ExitRoot:      common.HexToHash("0x444"),
		Aggregator:    common.HexToAddress("0xa4"),
	}
	require.NoError(t, p.ProcessBlock(ctx, sync.Block{
		Num:    15,
		Events: []interface{}{Event{VerifyBatches: pessimisticR3}},
	}))

	// Empty range (no rows in window)
	rows, err = p.GetVerifiedBatchesInBlockRange(100, 200)
	require.NoError(t, err)
	require.Empty(t, rows)

	// Lower bound inclusive, upper bound inclusive: [11, 12] excludes block 10, includes block 12
	rows, err = p.GetVerifiedBatchesInBlockRange(11, 12)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 12, rows[0].BlockNumber)
	require.EqualValues(t, 1, rows[0].RollupID)

	// [10, 12] inclusive on both ends: block 10's two rows (ordered by block_pos) then block 12
	rows, err = p.GetVerifiedBatchesInBlockRange(10, 12)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.EqualValues(t, 10, rows[0].BlockNumber)
	require.EqualValues(t, 0, rows[0].BlockPosition)
	require.EqualValues(t, 1, rows[0].RollupID)
	require.EqualValues(t, 10, rows[1].BlockNumber)
	require.EqualValues(t, 1, rows[1].BlockPosition)
	require.EqualValues(t, 2, rows[1].RollupID)
	require.EqualValues(t, 12, rows[2].BlockNumber)
	require.EqualValues(t, 1, rows[2].RollupID)

	// Full range mixes zkEVM (blocks 10) and pessimistic (blocks 12, 15) rows across rollups,
	// ordered by block_num then block_pos
	rows, err = p.GetVerifiedBatchesInBlockRange(0, 1000)
	require.NoError(t, err)
	require.Len(t, rows, 4)
	require.EqualValues(t, 10, rows[0].BlockNumber)
	require.EqualValues(t, 10, rows[1].BlockNumber)
	require.EqualValues(t, 12, rows[2].BlockNumber)
	require.EqualValues(t, 15, rows[3].BlockNumber)
	// pessimistic rows carry zero NumBatch/StateRoot; zkEVM rows carry their values
	require.EqualValues(t, 42, rows[0].NumBatch)
	require.Zero(t, rows[2].NumBatch)
	require.Zero(t, rows[3].NumBatch)
}

// TestGetVerifiedBatchesPaged proves GetVerifiedBatchesPaged (issue #1817) returns rollupID's
// settlements most recent first, paginates correctly, filters out other rollups, and enriches
// each row with the settlement block's hash.
func TestGetVerifiedBatchesPaged(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestGetVerifiedBatchesPaged.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	// No settlements yet for this rollup
	rows, count, err := p.GetVerifiedBatchesPaged(99, 1, 10)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, count)

	const rollupID = uint32(7)
	block10Hash := common.HexToHash("0xb10c10")
	block20Hash := common.HexToHash("0xb10c20")
	block30Hash := common.HexToHash("0xb10c30")
	txHash1 := common.HexToHash("0xf00d1")
	txHash2 := common.HexToHash("0xf00d2")
	txHash3 := common.HexToHash("0xf00d3")
	timestamp := uint64(1684500000)

	require.NoError(t, p.ProcessBlock(ctx, sync.Block{
		Num: 10, Hash: block10Hash,
		Events: []interface{}{Event{VerifyBatches: &VerifyBatches{
			RollupID: rollupID, ExitRoot: common.HexToHash("0x111"), Aggregator: common.HexToAddress("0xa1"),
			TxHash: &txHash1, BlockTimestamp: &timestamp,
		}}},
	}))
	require.NoError(t, p.ProcessBlock(ctx, sync.Block{
		Num: 20, Hash: block20Hash,
		Events: []interface{}{Event{VerifyBatches: &VerifyBatches{
			RollupID: rollupID, ExitRoot: common.HexToHash("0x222"), Aggregator: common.HexToAddress("0xa2"),
			TxHash: &txHash2, BlockTimestamp: &timestamp,
		}}},
	}))
	require.NoError(t, p.ProcessBlock(ctx, sync.Block{
		Num: 30, Hash: block30Hash,
		Events: []interface{}{Event{VerifyBatches: &VerifyBatches{
			RollupID: rollupID, ExitRoot: common.HexToHash("0x333"), Aggregator: common.HexToAddress("0xa3"),
			TxHash: &txHash3, BlockTimestamp: &timestamp,
		}}},
	}))
	// A settlement for a different rollup must never leak into rollupID's page
	require.NoError(t, p.ProcessBlock(ctx, sync.Block{
		Num: 40, Hash: common.HexToHash("0xb10c40"),
		Events: []interface{}{Event{VerifyBatches: &VerifyBatches{
			RollupID: rollupID + 1, ExitRoot: common.HexToHash("0x444"), Aggregator: common.HexToAddress("0xa4"),
		}}},
	}))

	// Page 1, size 2: most recent settlements first (block 30, then 20), TxHash/BlockTimestamp
	// carried through as set by the downloader, block hash joined in
	rows, count, err = p.GetVerifiedBatchesPaged(rollupID, 1, 2)
	require.NoError(t, err)
	require.Equal(t, 3, count)
	require.Len(t, rows, 2)
	require.EqualValues(t, 30, rows[0].BlockNumber)
	require.NotNil(t, rows[0].TxHash)
	require.NotNil(t, rows[0].BlockTimestamp)
	require.NotNil(t, rows[0].BlockHash)
	require.Equal(t, block30Hash, *rows[0].BlockHash)
	require.EqualValues(t, 20, rows[1].BlockNumber)
	require.Equal(t, block20Hash, *rows[1].BlockHash)

	// Page 2, size 2: the remaining, oldest row (block 10)
	rows, count, err = p.GetVerifiedBatchesPaged(rollupID, 2, 2)
	require.NoError(t, err)
	require.Equal(t, 3, count)
	require.Len(t, rows, 1)
	require.EqualValues(t, 10, rows[0].BlockNumber)
	require.Equal(t, block10Hash, *rows[0].BlockHash)
}

// TestGetVerifiedBatchesPagedLegacyRow proves that a row synced before tx_hash/block_timestamp
// existed (simulated here by inserting directly, bypassing the downloader/meddler.Insert path
// that always sets them today) comes back with those fields nil, per issue #1817's "never
// backfilled" design, while block_hash -- unrelated to that migration -- still resolves.
func TestGetVerifiedBatchesPagedLegacyRow(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestGetVerifiedBatchesPagedLegacyRow.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)

	blockHash := common.HexToHash("0xb1050")
	_, err = p.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 5, blockHash.String())
	require.NoError(t, err)
	_, err = p.db.Exec(`
		INSERT INTO verify_batches (block_num, block_pos, rollup_id, batch_num, state_root,
			exit_root, aggregator, rollup_exit_root)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		5, 0, 3, 1,
		common.HexToHash("0xaaa").String(), common.HexToHash("0x555").String(),
		common.HexToAddress("0xa5").String(), common.HexToHash("0x555").String())
	require.NoError(t, err)

	rows, count, err := p.GetVerifiedBatchesPaged(3, 1, 10)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Len(t, rows, 1)
	require.Nil(t, rows[0].TxHash)
	require.Nil(t, rows[0].BlockTimestamp)
	require.NotNil(t, rows[0].BlockHash)
	require.Equal(t, blockHash, *rows[0].BlockHash)
}
