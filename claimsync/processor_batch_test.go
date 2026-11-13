package claimsync

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"path"
	"sync/atomic"
	"testing"

	claimsyncStorage "github.com/agglayer/aggkit/claimsync/storage"
	"github.com/agglayer/aggkit/claimsync/storage/migrations"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// --- Standalone storage fixture (real SQLite, shared *sql.DB for raw row reads) ---

func newBatchTestStorage(t *testing.T) (claimsynctypes.ClaimStorager, *sql.DB) {
	t.Helper()
	lg := logger.WithFields("module", "test-batch-processor")

	database, err := db.NewSQLiteDB(path.Join(t.TempDir(), "processor-batch.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	require.NoError(t, migrations.RunMigrations(lg, database))

	store, err := claimsyncStorage.New(lg, database, "test-syncer", processorTestDBQueryTimeout)
	require.NoError(t, err)
	return store, database
}

func newBatchTestProcessor(t *testing.T) (*processor, *sql.DB) {
	t.Helper()
	store, database := newBatchTestStorage(t)
	lg := logger.WithFields("module", "test-batch-processor")
	return newProcessor(lg, store, processorTestDBQueryTimeout), database
}

// --- Full claim fixtures (valid proof roots / metadata / type / isMessage) ---

func batchTestProof(seed byte) treetypes.Proof {
	var raw [treetypes.DefaultHeight][common.HashLength]byte
	for i := range raw {
		raw[i][0] = seed
		raw[i][common.HashLength-1] = byte(i)
	}
	return treetypes.NewProof(raw)
}

func batchTestClaim(blockNum, blockPos uint64, globalIndex int64, isMessage bool) Claim {
	return Claim{
		BlockNum:            blockNum,
		BlockPos:            blockPos,
		TxHash:              common.BigToHash(big.NewInt(int64(blockNum)*100 + int64(blockPos))),
		GlobalIndex:         big.NewInt(globalIndex),
		OriginNetwork:       uint32(globalIndex%7) + 1,
		OriginAddress:       common.BigToAddress(big.NewInt(globalIndex + 1000)),
		DestinationAddress:  common.BigToAddress(big.NewInt(globalIndex + 2000)),
		Amount:              big.NewInt(globalIndex * 10),
		ProofLocalExitRoot:  batchTestProof(0xA0),
		ProofRollupExitRoot: batchTestProof(0xB0),
		MainnetExitRoot:     common.BigToHash(big.NewInt(globalIndex + 3000)),
		RollupExitRoot:      common.BigToHash(big.NewInt(globalIndex + 4000)),
		GlobalExitRoot:      common.BigToHash(big.NewInt(globalIndex + 5000)),
		DestinationNetwork:  uint32(globalIndex%5) + 1,
		Metadata:            []byte(fmt.Sprintf("metadata-%d-%t", globalIndex, isMessage)),
		IsMessage:           isMessage,
		BlockTimestamp:      blockNum * 12,
		Type:                DetailedClaimEvent,
	}
}

func claimPtr(c Claim) *Claim { return &c }

// batchTestBlocks returns an ordered batch covering claim + unset_claim + set_claim
// and an empty checkpoint block (no events), so the batch path is compared against
// the legacy path on every table and on an event-less block.
func batchTestBlocks() []sync.Block {
	return []sync.Block{
		{
			Num:  10,
			Hash: common.HexToHash("0x10"),
			Events: []any{
				Event{Claim: claimPtr(batchTestClaim(10, 0, 1000, false))},
				Event{UnsetClaim: &UnsetClaim{
					BlockNum:                  10,
					BlockPos:                  1,
					TxHash:                    common.HexToHash("0x101"),
					GlobalIndex:               big.NewInt(1001),
					UnsetGlobalIndexHashChain: common.HexToHash("0xff10"),
					CreatedAt:                 123456,
				}},
			},
		},
		{
			Num:  11,
			Hash: common.HexToHash("0x11"),
			Events: []any{
				Event{SetClaim: &SetClaim{
					BlockNum:    11,
					BlockPos:    0,
					TxHash:      common.HexToHash("0x110"),
					GlobalIndex: big.NewInt(2000),
					CreatedAt:   654321,
				}},
				Event{Claim: claimPtr(batchTestClaim(11, 1, 2001, true))},
			},
		},
		{
			Num:    12,
			Hash:   common.HexToHash("0x12"),
			Events: []any{},
		},
	}
}

// --- Raw row snapshot helpers (all columns of every claimsync table) ---

type batchDBSnapshot struct {
	Blocks      [][]string
	Claims      [][]string
	UnsetClaims [][]string
	SetClaims   [][]string
}

func queryAllRows(t *testing.T, database *sql.DB, query string) [][]string {
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

func snapshotBatchDB(t *testing.T, database *sql.DB) batchDBSnapshot {
	t.Helper()
	return batchDBSnapshot{
		Blocks:      queryAllRows(t, database, `SELECT num, hash FROM block ORDER BY num`),
		Claims:      queryAllRows(t, database, `SELECT * FROM claim ORDER BY block_num, block_pos`),
		UnsetClaims: queryAllRows(t, database, `SELECT * FROM unset_claim ORDER BY block_num, block_pos`),
		SetClaims:   queryAllRows(t, database, `SELECT * FROM set_claim ORDER BY block_num, block_pos`),
	}
}

func sequentialSnapshot(t *testing.T, blocks []sync.Block) batchDBSnapshot {
	t.Helper()
	p, database := newBatchTestProcessor(t)
	for _, b := range blocks {
		require.NoError(t, p.ProcessBlock(context.Background(), b))
	}
	return snapshotBatchDB(t, database)
}

// --- Tests ---

// TestProcessorProcessBlocks_MatchesSequential proves the batched single-transaction
// path persists exactly the same rows as the legacy one-block path: every claim,
// unset_claim, set_claim and block row, including both proof arrays, exit roots,
// metadata, type/isMessage and the empty checkpoint block.
func TestProcessorProcessBlocks_MatchesSequential(t *testing.T) {
	t.Parallel()
	blocks := batchTestBlocks()

	want := sequentialSnapshot(t, blocks)

	batProc, batDB := newBatchTestProcessor(t)
	require.NoError(t, batProc.processBlocks(context.Background(), blocks))
	got := snapshotBatchDB(t, batDB)

	require.Equal(t, want, got)
	// The empty checkpoint block must be persisted too.
	require.Contains(t, got.Blocks, []string{"12", common.HexToHash("0x12").Hex()})
	require.Len(t, got.Blocks, len(blocks))
}

// TestProcessorProcessBlocks_RollbackOnBadEventType proves a bad event type in the
// second block aborts the whole batch: no row from any block survives and the
// processor reports no processed block. A corrected retry then matches the legacy
// sequential result.
func TestProcessorProcessBlocks_RollbackOnBadEventType(t *testing.T) {
	t.Parallel()
	bad := []sync.Block{
		{
			Num:    20,
			Hash:   common.HexToHash("0x20"),
			Events: []any{Event{Claim: claimPtr(batchTestClaim(20, 0, 3000, false))}},
		},
		{
			Num:    21,
			Hash:   common.HexToHash("0x21"),
			Events: []any{"not-a-claim-event"},
		},
	}

	proc, database := newBatchTestProcessor(t)
	err := proc.processBlocks(context.Background(), bad)
	require.ErrorContains(t, err, "unexpected event type")
	require.Equal(t, batchDBSnapshot{}, snapshotBatchDB(t, database))

	_, found, err := proc.GetLastProcessedBlock(context.Background())
	require.NoError(t, err)
	require.False(t, found)

	corrected := []sync.Block{
		bad[0],
		{
			Num:    21,
			Hash:   common.HexToHash("0x21"),
			Events: []any{Event{Claim: claimPtr(batchTestClaim(21, 0, 3001, false))}},
		},
	}
	require.NoError(t, proc.processBlocks(context.Background(), corrected))
	require.Equal(t, sequentialSnapshot(t, corrected), snapshotBatchDB(t, database))
}

// TestProcessorProcessBlocks_RollbackOnStorageFailure proves a real SQL constraint
// failure on the second block (duplicate primary key) rolls the whole batch back,
// leaving no rows and no processed block; a corrected retry then matches sequential.
func TestProcessorProcessBlocks_RollbackOnStorageFailure(t *testing.T) {
	t.Parallel()
	bad := []sync.Block{
		{
			Num:    30,
			Hash:   common.HexToHash("0x30"),
			Events: []any{Event{Claim: claimPtr(batchTestClaim(30, 0, 4000, false))}},
		},
		{
			Num:  31,
			Hash: common.HexToHash("0x31"),
			Events: []any{
				Event{Claim: claimPtr(batchTestClaim(31, 0, 4001, false))},
				// Same (block_num, block_pos) primary key -> second insert must fail.
				Event{Claim: claimPtr(batchTestClaim(31, 0, 4002, true))},
			},
		},
	}

	proc, database := newBatchTestProcessor(t)
	err := proc.processBlocks(context.Background(), bad)
	require.Error(t, err)
	require.Equal(t, batchDBSnapshot{}, snapshotBatchDB(t, database))

	_, found, err := proc.GetLastProcessedBlock(context.Background())
	require.NoError(t, err)
	require.False(t, found)

	corrected := []sync.Block{
		bad[0],
		{
			Num:  31,
			Hash: common.HexToHash("0x31"),
			Events: []any{
				Event{Claim: claimPtr(batchTestClaim(31, 0, 4001, false))},
				Event{Claim: claimPtr(batchTestClaim(31, 1, 4002, true))},
			},
		},
	}
	require.NoError(t, proc.processBlocks(context.Background(), corrected))
	require.Equal(t, sequentialSnapshot(t, corrected), snapshotBatchDB(t, database))
}

// cancelAfterFirstEventEmbedded cancels the batch context right after the first
// event is processed, emulating cancellation arriving mid-batch but before commit.
type cancelAfterFirstEventEmbedded struct {
	claimsynctypes.EmbeddedProcessor
	canceled atomic.Bool
	cancel   context.CancelFunc
}

func (c *cancelAfterFirstEventEmbedded) ProcessBlockWithTx(
	ctx context.Context, tx dbtypes.Querier, block sync.Block, eventRaw any,
) error {
	err := c.EmbeddedProcessor.ProcessBlockWithTx(ctx, tx, block, eventRaw)
	if c.canceled.CompareAndSwap(false, true) {
		c.cancel()
	}
	return err
}

// TestProcessorProcessBlocks_CancellationBeforeCommit proves that cancelling the
// batch after some inserts but before commit rolls the whole transaction back, so
// no partial rows are visible.
func TestProcessorProcessBlocks_CancellationBeforeCommit(t *testing.T) {
	t.Parallel()
	proc, database := newBatchTestProcessor(t)

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	proc.embeddedProcessor = &cancelAfterFirstEventEmbedded{
		EmbeddedProcessor: proc.embeddedProcessor,
		cancel:            cancel,
	}

	err := proc.processBlocks(parent, batchTestBlocks())
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, batchDBSnapshot{}, snapshotBatchDB(t, database))
}

// txCountingStorager counts transaction begins and commits so a test can prove the
// batch path uses one transaction instead of one per block.
type txCountingStorager struct {
	claimsynctypes.ClaimStorager
	begins  atomic.Int32
	commits atomic.Int32
}

func (c *txCountingStorager) NewTx(ctx context.Context) (dbtypes.Txer, error) {
	c.begins.Add(1)
	tx, err := c.ClaimStorager.NewTx(ctx)
	if err != nil {
		return nil, err
	}
	return &commitCountingTx{Txer: tx, commits: &c.commits}, nil
}

type commitCountingTx struct {
	dbtypes.Txer
	commits *atomic.Int32
}

func (c *commitCountingTx) Commit() error {
	c.commits.Add(1)
	return c.Txer.Commit()
}

// TestProcessorProcessBlocks_SingleTransaction proves N sequential ProcessBlock
// calls open N transactions, while one processBlocks call opens exactly one. This
// also guards against a fake batch helper that merely loops ProcessBlock.
func TestProcessorProcessBlocks_SingleTransaction(t *testing.T) {
	t.Parallel()
	lg := logger.WithFields("module", "test-batch-count")
	blocks := batchTestBlocks()

	seqStore, _ := newBatchTestStorage(t)
	seqCounting := &txCountingStorager{ClaimStorager: seqStore}
	seqProc := newProcessor(lg, seqCounting, processorTestDBQueryTimeout)
	for _, b := range blocks {
		require.NoError(t, seqProc.ProcessBlock(context.Background(), b))
	}
	require.Equal(t, int32(len(blocks)), seqCounting.begins.Load())
	require.Equal(t, int32(len(blocks)), seqCounting.commits.Load())

	batStore, _ := newBatchTestStorage(t)
	batCounting := &txCountingStorager{ClaimStorager: batStore}
	batProc := newProcessor(lg, batCounting, processorTestDBQueryTimeout)
	require.NoError(t, batProc.processBlocks(context.Background(), blocks))
	require.Equal(t, int32(1), batCounting.begins.Load())
	require.Equal(t, int32(1), batCounting.commits.Load())
}

// TestProcessorProcessBlocks_ReorgCascadeAfterBatch proves the block foreign-key
// cascade still works after a batch: Reorg(firstReorgedBlock) deletes every block
// >= that number together with its claim/unset_claim/set_claim rows.
func TestProcessorProcessBlocks_ReorgCascadeAfterBatch(t *testing.T) {
	t.Parallel()
	proc, database := newBatchTestProcessor(t)
	blocks := batchTestBlocks()
	require.NoError(t, proc.processBlocks(context.Background(), blocks))

	before := snapshotBatchDB(t, database)
	require.Len(t, before.Blocks, len(blocks))
	require.NotEmpty(t, before.Claims)
	require.NotEmpty(t, before.UnsetClaims)
	require.NotEmpty(t, before.SetClaims)

	require.NoError(t, proc.Reorg(context.Background(), 11))

	// Only block 10 and its rows must survive; blocks 11 (claim + set_claim) and the
	// empty block 12 must be cascade-deleted.
	refProc, refDB := newBatchTestProcessor(t)
	require.NoError(t, refProc.ProcessBlock(context.Background(), blocks[0]))
	require.Equal(t, snapshotBatchDB(t, refDB), snapshotBatchDB(t, database))
}

// TestProcessorProcessBlocks_EmptyBatch is a no-op: no transaction, no rows.
func TestProcessorProcessBlocks_EmptyBatch(t *testing.T) {
	t.Parallel()
	proc, database := newBatchTestProcessor(t)
	require.NoError(t, proc.processBlocks(context.Background(), nil))
	require.NoError(t, proc.processBlocks(context.Background(), []sync.Block{}))
	require.Equal(t, batchDBSnapshot{}, snapshotBatchDB(t, database))
}

// TestProcessorProcessBlocks_RejectsOversizedBatch enforces the <=100 block bound.
func TestProcessorProcessBlocks_RejectsOversizedBatch(t *testing.T) {
	t.Parallel()
	proc, database := newBatchTestProcessor(t)

	blocks := make([]sync.Block, maxBlocksPerBatch+1)
	for i := range blocks {
		blocks[i] = sync.Block{Num: uint64(i + 1), Hash: common.BigToHash(big.NewInt(int64(i + 1)))}
	}

	err := proc.processBlocks(context.Background(), blocks)
	require.ErrorContains(t, err, "exceeds")
	require.Equal(t, batchDBSnapshot{}, snapshotBatchDB(t, database))
}
