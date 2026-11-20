package bridgesync

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/big"
	"path"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgesync/migrations"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// This file exercises the atomic batch path (processor.processBlocks) against the
// legacy one-block path (processor.ProcessBlock) using the real SQLite processor
// and raw SQL snapshots. It is intentionally standalone: every test builds its own
// fresh processor/database so the global block1/block3/block4 fixtures are never
// mutated or shared between subtests.

// newBatchTestProcessor builds a processor backed by a brand new migrated SQLite
// database in a per-test temporary directory. The *sql.DB is reachable as p.db.
func newBatchTestProcessor(t *testing.T) *processor {
	t.Helper()
	dbPath := path.Join(t.TempDir(), "bridgesync-processor-batch.sqlite")
	logger := log.WithFields("module", "test-batch-processor")
	p, err := newTestProcessor(dbPath, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)
	return p
}

// batchTestBridge builds a minimal, fully-populated bridge event. Only the fields
// needed by the exit tree/insert path are set; no proof/claim fields are involved.
func batchTestBridge(blockNum, blockPos uint64, depositCount uint32, seed byte) *Bridge {
	return &Bridge{
		BlockNum:           blockNum,
		BlockPos:           blockPos,
		LeafType:           bridgesynctypes.LeafTypeAsset.Uint8(),
		OriginNetwork:      uint32(seed) + 1,
		OriginAddress:      common.BigToAddress(big.NewInt(int64(seed) + 0x11)),
		DestinationNetwork: uint32(seed) + 1,
		DestinationAddress: common.BigToAddress(big.NewInt(int64(seed) + 0x22)),
		Amount:             big.NewInt(int64(seed) + 1),
		Metadata:           []byte{seed},
		DepositCount:       depositCount,
	}
}

// batchTestTokenMapping builds a minimal token mapping event.
func batchTestTokenMapping(blockNum, blockPos uint64, seed byte) *TokenMapping {
	return &TokenMapping{
		BlockNum:            blockNum,
		BlockPos:            blockPos,
		OriginNetwork:       uint32(seed) + 1,
		OriginTokenAddress:  common.BigToAddress(big.NewInt(int64(seed) + 0x100)),
		WrappedTokenAddress: common.BigToAddress(big.NewInt(int64(seed) + 0x200)),
		Metadata:            []byte{seed},
	}
}

// batchFixtureBlocks returns a fresh, ordered batch of three blocks:
//   - block 1: one bridge leaf + one token mapping
//   - block 3: two bridge leaves + one token mapping
//   - block 4: an empty checkpoint (no events), which must still be persisted
//
// It therefore covers three leaves (three roots) and an event-less block.
func batchFixtureBlocks() []sync.Block {
	return []sync.Block{
		{
			Num:  1,
			Hash: common.HexToHash("0x1"),
			Events: []any{
				Event{Bridge: batchTestBridge(1, 0, 0, 1)},
				Event{TokenMapping: batchTestTokenMapping(1, 2, 1)},
			},
		},
		{
			Num:  3,
			Hash: common.HexToHash("0x3"),
			Events: []any{
				Event{Bridge: batchTestBridge(3, 0, 1, 2)},
				Event{Bridge: batchTestBridge(3, 1, 2, 3)},
				Event{TokenMapping: batchTestTokenMapping(3, 2, 4)},
			},
		},
		{
			Num:    4,
			Hash:   common.HexToHash("0x4"),
			Events: []any{},
		},
	}
}

// batchBadEventTypeBlocks returns a batch whose FIRST block contains a valid
// bridge leaf (deposit count 0) and whose SECOND block contains an invalid event
// type, so the failure happens after the first leaf was applied inside the batch
// transaction.
func batchBadEventTypeBlocks() []sync.Block {
	return []sync.Block{
		{
			Num:  20,
			Hash: common.HexToHash("0x20"),
			Events: []any{
				Event{Bridge: batchTestBridge(20, 0, 0, 5)},
			},
		},
		{
			Num:    21,
			Hash:   common.HexToHash("0x21"),
			Events: []any{"not-a-bridge-event"},
		},
	}
}

// batchBridgeDBSnapshot holds the raw, stringified rows of every table the batch
// path is required to reproduce: block, bridge, root and token_mapping.
type batchBridgeDBSnapshot struct {
	Blocks        [][]string
	Bridges       [][]string
	Roots         [][]string
	TokenMappings [][]string
}

// batchQueryAllRows reads every row of a query as strings. []byte values are
// copied into fresh strings and NULLs are rendered as "<null>", so two databases
// can be compared with require.Equal regardless of the driver's scan types.
func batchQueryAllRows(t *testing.T, database *sql.DB, query string) [][]string {
	t.Helper()
	rows, err := database.Query(query)
	require.NoError(t, err)
	defer rows.Close()

	cols, err := rows.Columns()
	require.NoError(t, err)

	var out [][]string
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		require.NoError(t, rows.Scan(ptrs...))

		rec := make([]string, len(cols))
		for i, v := range raw {
			switch x := v.(type) {
			case nil:
				rec[i] = "<null>"
			case []byte:
				rec[i] = string(x)
			default:
				rec[i] = fmt.Sprintf("%v", x)
			}
		}
		out = append(out, rec)
	}
	require.NoError(t, rows.Err())
	return out
}

// snapshotBatchBridgeDB snapshots every table the batch path must persist, in a
// deterministic order.
func snapshotBatchBridgeDB(t *testing.T, database *sql.DB) batchBridgeDBSnapshot {
	t.Helper()
	return batchBridgeDBSnapshot{
		Blocks:        batchQueryAllRows(t, database, `SELECT num, hash FROM block ORDER BY num`),
		Bridges:       batchQueryAllRows(t, database, `SELECT * FROM bridge ORDER BY block_num, block_pos`),
		Roots:         batchQueryAllRows(t, database, `SELECT hash, position, block_num, block_position FROM root ORDER BY position`),
		TokenMappings: batchQueryAllRows(t, database, `SELECT * FROM token_mapping ORDER BY block_num, block_pos`),
	}
}

// sequentialBatchSnapshot processes the given blocks one by one through the legacy
// ProcessBlock path on a fresh processor and returns the resulting snapshot.
func sequentialBatchSnapshot(t *testing.T, blocks []sync.Block) batchBridgeDBSnapshot {
	t.Helper()
	p := newBatchTestProcessor(t)
	for _, b := range blocks {
		require.NoError(t, p.ProcessBlock(context.Background(), b))
	}
	return snapshotBatchBridgeDB(t, p.db)
}

// TestProcessorProcessBlocks_MatchesSequential proves the single-transaction batch
// path persists exactly the same rows as the legacy one-block path: every block,
// bridge, exit-tree root and token_mapping row, including every intermediate root
// (not only the last one) and the empty checkpoint block.
func TestProcessorProcessBlocks_MatchesSequential(t *testing.T) {
	t.Parallel()
	blocks := batchFixtureBlocks()

	want := sequentialBatchSnapshot(t, blocks)

	batchProc := newBatchTestProcessor(t)
	require.NoError(t, batchProc.processBlocks(context.Background(), blocks))
	got := snapshotBatchBridgeDB(t, batchProc.db)

	require.Equal(t, want, got)

	// The event-less checkpoint block must be persisted too.
	require.Len(t, got.Blocks, len(blocks))
	require.Contains(t, got.Blocks, []string{"4", common.HexToHash("0x4").Hex()})

	// Every leaf's root is preserved (three leaves -> three root rows), not just the last root.
	require.Len(t, got.Roots, 3)
	require.NotEmpty(t, got.Bridges)
	require.NotEmpty(t, got.TokenMappings)
}

// TestProcessorProcessBlocks_RollbackOnBadEventType proves a bad event type in the
// second block aborts the whole batch: no row from any block survives, the
// processor is NOT halted for a type error, and a corrected retry whose first leaf
// reuses deposit count 0 succeeds (proving the in-memory exit-tree cache was
// invalidated by the rollback) and matches the legacy sequential result.
func TestProcessorProcessBlocks_RollbackOnBadEventType(t *testing.T) {
	t.Parallel()
	bad := batchBadEventTypeBlocks()

	proc := newBatchTestProcessor(t)
	err := proc.processBlocks(context.Background(), bad)
	require.ErrorContains(t, err, "failed to convert event")
	require.Equal(t, batchBridgeDBSnapshot{}, snapshotBatchBridgeDB(t, proc.db))
	require.False(t, proc.isHalted(), "a bad event type must not halt the processor")

	corrected := []sync.Block{
		bad[0],
		{
			Num:    21,
			Hash:   common.HexToHash("0x21"),
			Events: []any{Event{Bridge: batchTestBridge(21, 0, 1, 6)}},
		},
	}
	// The retry re-adds leaf index 0; it can only succeed if the failed batch
	// invalidated the tree cache (otherwise PutLeaf would expect index 1).
	require.NoError(t, proc.processBlocks(context.Background(), corrected))
	require.Equal(t, sequentialBatchSnapshot(t, corrected), snapshotBatchBridgeDB(t, proc.db))
}

// TestProcessorProcessBlocks_InvalidIndexHalts proves an invalid leaf index in the
// second block of a batch halts the processor (as the legacy path does) and that
// the whole batch is rolled back, leaving no rows behind.
func TestProcessorProcessBlocks_InvalidIndexHalts(t *testing.T) {
	t.Parallel()
	blocks := []sync.Block{
		{
			Num:  30,
			Hash: common.HexToHash("0x30"),
			Events: []any{
				Event{Bridge: batchTestBridge(30, 0, 0, 7)},
			},
		},
		{
			Num:  31,
			Hash: common.HexToHash("0x31"),
			Events: []any{
				// Leaf index 5 after leaf 0 -> ErrInvalidIndex -> halt.
				Event{Bridge: batchTestBridge(31, 0, 5, 8)},
			},
		},
	}

	proc := newBatchTestProcessor(t)
	err := proc.processBlocks(context.Background(), blocks)
	require.ErrorIs(t, err, sync.ErrInconsistentState)
	require.True(t, proc.isHalted())
	require.Equal(t, batchBridgeDBSnapshot{}, snapshotBatchBridgeDB(t, proc.db))
}

// drainBatchNotifications reads exactly n notifications from ch, failing the test
// if fewer arrive within the timeout.
func drainBatchNotifications(t *testing.T, ch <-chan uint64, n int) []uint64 {
	t.Helper()
	got := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		select {
		case v := <-ch:
			got = append(got, v)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for notification %d/%d (got %v)", i+1, n, got)
		}
	}
	return got
}

// recordingBridgeSubscriber is a deterministic, synchronous PubSub used only to
// observe the exact order in which processBlocks calls Publish. The production
// GenericSubscriber.Publish fans out from one goroutine per call, so delivery
// order over its channel is not guaranteed; this double pins the publish *call*
// order instead.
type recordingBridgeSubscriber struct {
	blocks []uint64
}

func (r *recordingBridgeSubscriber) Subscribe(string) <-chan uint64 { return nil }

func (r *recordingBridgeSubscriber) Publish(blockNum uint64) {
	r.blocks = append(r.blocks, blockNum)
}

// TestProcessorProcessBlocks_NotificationsOnlyAfterCommit proves bridge
// notifications are emitted exclusively after the batch commits: a successful
// batch notifies exactly the blocks that carried a bridge (block1 and block3, not
// the empty block4), while a rolled-back batch notifies nothing even though its
// first block contained a bridge. Publish call order is checked separately with a
// synchronous subscriber because the production publisher is asynchronous.
func TestProcessorProcessBlocks_NotificationsOnlyAfterCommit(t *testing.T) {
	t.Parallel()

	t.Run("commit notifies bridging blocks only", func(t *testing.T) {
		t.Parallel()
		proc := newBatchTestProcessor(t)
		notifications := proc.bridgeSubscriber.Subscribe("batch-test")

		require.NoError(t, proc.processBlocks(context.Background(), batchFixtureBlocks()))

		expected := []uint64{1, 3}
		got := drainBatchNotifications(t, notifications, len(expected))
		require.ElementsMatch(t, expected, got)
	})

	t.Run("rollback notifies nothing", func(t *testing.T) {
		t.Parallel()
		proc := newBatchTestProcessor(t)
		notifications := proc.bridgeSubscriber.Subscribe("batch-test")

		// The first block has a bridge, so a pre-commit publish would be observable here.
		require.Error(t, proc.processBlocks(context.Background(), batchBadEventTypeBlocks()))

		select {
		case v := <-notifications:
			t.Fatalf("unexpected notification %d after rolled-back batch", v)
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("publish order follows batch order", func(t *testing.T) {
		t.Parallel()
		proc := newBatchTestProcessor(t)
		rec := &recordingBridgeSubscriber{}
		proc.bridgeSubscriber = rec

		require.NoError(t, proc.processBlocks(context.Background(), batchFixtureBlocks()))
		require.Equal(t, []uint64{1, 3}, rec.blocks)
	})
}

// --- Legacy single-block path is a batch of one: regression coverage ---

// batchStandaloneBlocks returns single blocks that are each valid on their own
// (leaf indices start at 0), so they can be fed either to ProcessBlock or to a
// one-element processBlocks batch.
func batchStandaloneBlocks() []sync.Block {
	return []sync.Block{
		{
			Num:  1,
			Hash: common.HexToHash("0x1"),
			Events: []any{
				Event{Bridge: batchTestBridge(1, 0, 0, 1)},
				Event{TokenMapping: batchTestTokenMapping(1, 2, 1)},
			},
		},
		{
			Num:  3,
			Hash: common.HexToHash("0x3"),
			Events: []any{
				Event{Bridge: batchTestBridge(3, 0, 0, 2)},
				Event{Bridge: batchTestBridge(3, 1, 1, 3)},
				Event{TokenMapping: batchTestTokenMapping(3, 2, 4)},
			},
		},
		{
			Num:    4,
			Hash:   common.HexToHash("0x4"),
			Events: []any{},
		},
	}
}

// TestProcessorProcessBlock_MatchesBatchOfOne proves the legacy single-block
// ProcessBlock and a one-element processBlocks batch are byte-for-byte identical:
// every block, bridge, exit-tree root (all intermediate roots) and token_mapping
// row is the same on both paths, including the empty checkpoint block.
func TestProcessorProcessBlock_MatchesBatchOfOne(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		block sync.Block
	}{
		{"bridge+token", batchStandaloneBlocks()[0]},
		{"two bridges+token", batchStandaloneBlocks()[1]},
		{"empty checkpoint", batchStandaloneBlocks()[2]},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			single := newBatchTestProcessor(t)
			require.NoError(t, single.ProcessBlock(context.Background(), tc.block))
			want := snapshotBatchBridgeDB(t, single.db)

			batched := newBatchTestProcessor(t)
			require.NoError(t, batched.processBlocks(context.Background(), []sync.Block{tc.block}))
			got := snapshotBatchBridgeDB(t, batched.db)

			require.Equal(t, want, got)
			require.Len(t, got.Blocks, 1)
		})
	}
}

// txCountingDriverNameSeq gives every counting driver a unique registered name so
// parallel tests never collide in the global sql driver registry.
var txCountingDriverNameSeq atomic.Int32

var errInjectedCommitFailure = errors.New("injected commit failure")

// txCountingDriver wraps the real sqlite3 driver to observe the transaction
// lifecycle without touching the processor: it counts Begin calls and can inject a
// commit failure on the resulting transactions.
type txCountingDriver struct {
	inner      driver.Driver
	begins     *atomic.Int32
	failCommit *atomic.Bool
}

func (d *txCountingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &txCountingConn{Conn: conn, begins: d.begins, failCommit: d.failCommit}, nil
}

type txCountingConn struct {
	driver.Conn
	begins     *atomic.Int32
	failCommit *atomic.Bool
}

func (c *txCountingConn) Begin() (driver.Tx, error) {
	c.begins.Add(1)
	//nolint:staticcheck // the wrapped sqlite3 driver only implements the deprecated Begin.
	tx, err := c.Conn.Begin()
	if err != nil {
		return nil, err
	}
	return &txCountingTx{Tx: tx, failCommit: c.failCommit}, nil
}

func (c *txCountingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.begins.Add(1)
	if beginTx, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err := beginTx.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &txCountingTx{Tx: tx, failCommit: c.failCommit}, nil
	}
	//nolint:staticcheck // the wrapped sqlite3 driver only implements the deprecated Begin.
	tx, err := c.Conn.Begin()
	if err != nil {
		return nil, err
	}
	return &txCountingTx{Tx: tx, failCommit: c.failCommit}, nil
}

type txCountingTx struct {
	driver.Tx
	failCommit *atomic.Bool
}

func (t *txCountingTx) Commit() error {
	if t.failCommit != nil && t.failCommit.Load() {
		// End the transaction underneath before surfacing the injected failure, so
		// database/sql does not return the connection to the pool with an open
		// transaction (which would make the uncommitted rows visible to readers).
		_ = t.Rollback()
		return errInjectedCommitFailure
	}
	return t.Tx.Commit()
}

// newBatchTestProcessorWithTxHook builds a real migrated SQLite processor whose
// transactions are observed (and optionally failed) by a wrapping sqlite driver.
func newBatchTestProcessorWithTxHook(
	t *testing.T, begins *atomic.Int32, failCommit *atomic.Bool,
) *processor {
	t.Helper()
	dbPath := path.Join(t.TempDir(), "bridgesync-processor-txhook.sqlite")
	require.NoError(t, migrations.RunMigrations(dbPath))

	driverName := fmt.Sprintf("sqlite3-txcount-%d", txCountingDriverNameSeq.Add(1))
	sql.Register(driverName, &txCountingDriver{
		inner:      &sqlite3.SQLiteDriver{},
		begins:     begins,
		failCommit: failCommit,
	})

	database, err := sql.Open(driverName, fmt.Sprintf(
		"file:%s?_txlock=immediate&_foreign_keys=on&_journal_mode=WAL&_busy_timeout=30000", dbPath))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	logger := log.WithFields("module", "test-batch-txhook")
	p, err := newProcessor(database, "bridge-syncer", logger, dbQueryTimeout)
	require.NoError(t, err)
	return p
}

// TestProcessorProcessBlock_SingleTransaction proves the delegation keeps the
// legacy one-transaction-per-call guarantee: a single ProcessBlock, a one-element
// batch and a three-block batch each open exactly one transaction.
func TestProcessorProcessBlock_SingleTransaction(t *testing.T) {
	t.Parallel()

	t.Run("legacy single block opens one transaction", func(t *testing.T) {
		t.Parallel()
		var begins atomic.Int32
		proc := newBatchTestProcessorWithTxHook(t, &begins, nil)

		require.NoError(t, proc.ProcessBlock(context.Background(), batchFixtureBlocks()[0]))
		require.Equal(t, int32(1), begins.Load())
	})

	t.Run("batch of one opens one transaction", func(t *testing.T) {
		t.Parallel()
		var begins atomic.Int32
		proc := newBatchTestProcessorWithTxHook(t, &begins, nil)

		require.NoError(t, proc.processBlocks(context.Background(), []sync.Block{batchFixtureBlocks()[0]}))
		require.Equal(t, int32(1), begins.Load())
	})

	t.Run("batch of three opens one transaction", func(t *testing.T) {
		t.Parallel()
		var begins atomic.Int32
		proc := newBatchTestProcessorWithTxHook(t, &begins, nil)

		require.NoError(t, proc.processBlocks(context.Background(), batchFixtureBlocks()))
		require.Equal(t, int32(1), begins.Load())
	})
}

// TestProcessorProcessBlock_CancellationMatchesBatchOfOne proves a cancelled
// context behaves identically on both paths: the error keeps its identity
// (context.Canceled), no row is inserted and no subscriber notification is
// emitted.
func TestProcessorProcessBlock_CancellationMatchesBatchOfOne(t *testing.T) {
	t.Parallel()

	t.Run("legacy single block", func(t *testing.T) {
		t.Parallel()
		proc := newBatchTestProcessor(t)
		rec := &recordingBridgeSubscriber{}
		proc.bridgeSubscriber = rec

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := proc.ProcessBlock(ctx, batchFixtureBlocks()[0])
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, batchBridgeDBSnapshot{}, snapshotBatchBridgeDB(t, proc.db))
		require.Empty(t, rec.blocks)
	})

	t.Run("batch of one", func(t *testing.T) {
		t.Parallel()
		proc := newBatchTestProcessor(t)
		rec := &recordingBridgeSubscriber{}
		proc.bridgeSubscriber = rec

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := proc.processBlocks(ctx, []sync.Block{batchFixtureBlocks()[0]})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, batchBridgeDBSnapshot{}, snapshotBatchBridgeDB(t, proc.db))
		require.Empty(t, rec.blocks)
	})
}

// TestProcessorProcessBlock_CommitFailureRollsBackAndDoesNotPublish proves a
// commit failure on both the legacy single-block path and a one-element batch
// rolls the transaction back (no rows) and never notifies subscribers, even though
// the block carried a bridge.
func TestProcessorProcessBlock_CommitFailureRollsBackAndDoesNotPublish(t *testing.T) {
	t.Parallel()

	t.Run("legacy single block", func(t *testing.T) {
		t.Parallel()
		var begins atomic.Int32
		var failCommit atomic.Bool
		failCommit.Store(true)
		proc := newBatchTestProcessorWithTxHook(t, &begins, &failCommit)
		rec := &recordingBridgeSubscriber{}
		proc.bridgeSubscriber = rec

		err := proc.ProcessBlock(context.Background(), batchFixtureBlocks()[0])
		require.ErrorIs(t, err, errInjectedCommitFailure)
		require.Equal(t, batchBridgeDBSnapshot{}, snapshotBatchBridgeDB(t, proc.db))
		require.Empty(t, rec.blocks)
	})

	t.Run("batch of one", func(t *testing.T) {
		t.Parallel()
		var begins atomic.Int32
		var failCommit atomic.Bool
		failCommit.Store(true)
		proc := newBatchTestProcessorWithTxHook(t, &begins, &failCommit)
		rec := &recordingBridgeSubscriber{}
		proc.bridgeSubscriber = rec

		err := proc.processBlocks(context.Background(), []sync.Block{batchFixtureBlocks()[0]})
		require.ErrorIs(t, err, errInjectedCommitFailure)
		require.Equal(t, batchBridgeDBSnapshot{}, snapshotBatchBridgeDB(t, proc.db))
		require.Empty(t, rec.blocks)
	})
}
