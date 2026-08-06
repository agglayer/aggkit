package l1infotreesync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agglayer/aggkit/db"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	aggkitsync "github.com/agglayer/aggkit/sync"
	treetypesmocks "github.com/agglayer/aggkit/tree/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetInfo(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestGetInfo.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	// Test ErrNotFound returned correctly on all methods
	_, err = p.GetFirstL1InfoWithRollupExitRoot(common.Hash{})
	require.Equal(t, db.ErrNotFound, err)
	_, err = p.GetLastInfo()
	require.Equal(t, db.ErrNotFound, err)
	_, err = p.GetFirstInfo()
	require.Equal(t, db.ErrNotFound, err)
	_, err = p.GetFirstInfoAfterBlock(0)
	require.Equal(t, db.ErrNotFound, err)
	_, err = p.GetInfoByGlobalExitRoot(common.Hash{})
	require.Equal(t, db.ErrNotFound, err)
	_, err = p.GetInfoByRoot(common.Hash{})
	require.Equal(t, db.ErrNotFound, err)

	// First insert
	info1 := &UpdateL1InfoTree{
		MainnetExitRoot: common.HexToHash("beef"),
		RollupExitRoot:  common.HexToHash("5ca1e"),
		ParentHash:      common.HexToHash("1010101"),
		Timestamp:       420,
	}
	expected1 := L1InfoTreeLeaf{
		BlockNumber:       1,
		L1InfoTreeIndex:   0,
		PreviousBlockHash: info1.ParentHash,
		Timestamp:         info1.Timestamp,
		MainnetExitRoot:   info1.MainnetExitRoot,
		RollupExitRoot:    info1.RollupExitRoot,
	}
	expected1.GlobalExitRoot = expected1.GetGlobalExitRoot()
	expected1.Hash = expected1.GetHash()
	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num: 1,
		Events: []any{
			Event{UpdateL1InfoTree: info1},
		},
	})
	require.NoError(t, err)
	actual, err := p.GetFirstL1InfoWithRollupExitRoot(info1.RollupExitRoot)
	require.NoError(t, err)
	require.Equal(t, expected1, *actual)
	actual, err = p.GetLastInfo()
	require.NoError(t, err)
	require.Equal(t, expected1, *actual)
	actual, err = p.GetFirstInfo()
	require.NoError(t, err)
	require.Equal(t, expected1, *actual)
	actual, err = p.GetFirstInfoAfterBlock(0)
	require.NoError(t, err)
	require.Equal(t, expected1, *actual)
	actual, err = p.GetInfoByGlobalExitRoot(expected1.GlobalExitRoot)
	require.NoError(t, err)
	require.Equal(t, expected1, *actual)

	// Second insert
	info2 := &UpdateL1InfoTree{
		MainnetExitRoot: common.HexToHash("b055"),
		RollupExitRoot:  common.HexToHash("5ca1e"),
		ParentHash:      common.HexToHash("1010101"),
		Timestamp:       420,
	}
	expected2 := L1InfoTreeLeaf{
		BlockNumber:       2,
		L1InfoTreeIndex:   1,
		PreviousBlockHash: info2.ParentHash,
		Timestamp:         info2.Timestamp,
		MainnetExitRoot:   info2.MainnetExitRoot,
		RollupExitRoot:    info2.RollupExitRoot,
	}
	expected2.GlobalExitRoot = expected2.GetGlobalExitRoot()
	expected2.Hash = expected2.GetHash()
	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num: 2,
		Events: []interface{}{
			Event{UpdateL1InfoTree: info2},
		},
	})
	require.NoError(t, err)
	actual, err = p.GetFirstL1InfoWithRollupExitRoot(info2.RollupExitRoot)
	require.NoError(t, err)
	require.Equal(t, expected1, *actual)
	actual, err = p.GetLastInfo()
	require.NoError(t, err)
	require.Equal(t, expected2, *actual)
	actual, err = p.GetFirstInfo()
	require.NoError(t, err)
	require.Equal(t, expected1, *actual)
	actual, err = p.GetFirstInfoAfterBlock(2)
	require.NoError(t, err)
	require.Equal(t, expected2, *actual)
	actual, err = p.GetInfoByGlobalExitRoot(expected2.GlobalExitRoot)
	require.NoError(t, err)
	require.Equal(t, expected2, *actual)
}

func TestGetLatestInfoUntilBlockIfNotFoundReturnsErrNotFound(t *testing.T) {
	ctx := t.Context()
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestGetLatestInfoUntilBlockIfNotFoundReturnsErrNotFound.sqlite")
	sut, err := newProcessor(dbPath)
	require.NoError(t, err)
	// Fake block 1
	_, err = sut.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 1, "0x1")
	require.NoError(t, err)

	blockNum := uint64(1)
	_, err = sut.GetLatestL1InfoLeafUntilBlock(ctx, &blockNum)
	require.Equal(t, db.ErrNotFound, err)
}

func TestGetLatestL1InfoLeafUntilBlock(t *testing.T) {
	ctx := t.Context()
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestGetLatestL1InfoLeafUntilBlock.sqlite")

	sut, err := newProcessor(dbPath)
	require.NoError(t, err)

	// Insert a base block for tests that need one
	_, err = sut.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 1, "0x1")
	require.NoError(t, err)

	tests := []struct {
		name        string
		blockNum    *uint64
		expectedErr error
	}{
		{
			name:        "returns ErrNoBlock0 when block number is zero",
			blockNum:    func() *uint64 { n := uint64(0); return &n }(),
			expectedErr: ErrNoBlock0,
		},
		{
			name:        "returns ErrBlockNotProcessed when requested block not processed yet",
			blockNum:    func() *uint64 { n := uint64(5); return &n }(),
			expectedErr: ErrBlockNotProcessed,
		},
		{
			name:        "returns ErrNotFound when no L1 info leaf before given block",
			blockNum:    func() *uint64 { n := uint64(1); return &n }(),
			expectedErr: db.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean DB state between test cases
			_, err := sut.db.Exec("DELETE FROM l1info_leaf;")
			require.NoError(t, err)

			_, err = sut.GetLatestL1InfoLeafUntilBlock(ctx, tt.blockNum)
			require.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestProcessor_Reorg(t *testing.T) {
	t.Parallel()

	testTable := []struct {
		name         string
		getProcessor func(t *testing.T) *processor
		reorgBlock   uint64
		expectedErr  string
	}{
		{
			name: "empty tree",
			getProcessor: func(t *testing.T) *processor {
				t.Helper()

				p, err := newProcessor(path.Join(t.TempDir(), "l1infotreesyncTest_processor_Reorg_1.sqlite"))
				require.NoError(t, err)
				return p
			},
			reorgBlock:  0,
			expectedErr: "",
		},
		{
			name: "single leaf tree",
			getProcessor: func(t *testing.T) *processor {
				t.Helper()

				p, err := newProcessor(path.Join(t.TempDir(), "l1infotreesyncTest_processor_Reorg_2.sqlite"))
				require.NoError(t, err)

				info := &UpdateL1InfoTree{
					MainnetExitRoot: common.HexToHash("beef"),
					RollupExitRoot:  common.HexToHash("5ca1e"),
					ParentHash:      common.HexToHash("1010101"),
					Timestamp:       420,
				}
				err = p.ProcessBlock(context.Background(), aggkitsync.Block{
					Num: 1,
					Events: []interface{}{
						Event{UpdateL1InfoTree: info},
					},
				})
				require.NoError(t, err)

				return p
			},
			reorgBlock:  1,
			expectedErr: "",
		},
		{
			name: "l1 info tree fails to reorg",
			getProcessor: func(t *testing.T) *processor {
				t.Helper()
				p, err := newProcessor(path.Join(t.TempDir(), "l1infotreesyncTest_processor_Reorg_3.sqlite"))
				require.NoError(t, err)

				l1InfoTreeMock := treetypesmocks.NewFullTreer(t)
				l1InfoTreeMock.EXPECT().
					Reorg(mock.Anything, mock.Anything).
					Return(errors.New("failed to reorg l1 info tree")).
					Once()
				p.l1InfoTree = l1InfoTreeMock
				return p
			},
			expectedErr: "failed to reorg l1 info tree",
		},
		{
			name: "rollup exit tree fails to reorg",
			getProcessor: func(t *testing.T) *processor {
				t.Helper()
				p, err := newProcessor(path.Join(t.TempDir(), "l1infotreesyncTest_processor_Reorg_4.sqlite"))
				require.NoError(t, err)

				l1InfoTreeMock := treetypesmocks.NewFullTreer(t)
				l1InfoTreeMock.EXPECT().
					Reorg(mock.Anything, mock.Anything).
					Return(nil).
					Once()
				p.l1InfoTree = l1InfoTreeMock

				rollupExitTreeMock := treetypesmocks.NewFullTreer(t)
				rollupExitTreeMock.EXPECT().
					Reorg(mock.Anything, mock.Anything).
					Return(errors.New("failed to reorg rollup exit tree")).
					Once()
				p.rollupExitTree = rollupExitTreeMock
				return p
			},
			expectedErr: "failed to reorg rollup exit tree",
		},
	}

	for _, tt := range testTable {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := tt.getProcessor(t)
			err := p.Reorg(context.Background(), tt.reorgBlock)
			if tt.expectedErr != "" {
				require.ErrorContains(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestProcessor_ConcurrentProcessBlockAndReorg(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "processor_concurrent_process_block_reorg.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	const maxBlockNum = 30
	var (
		wg    sync.WaitGroup
		errCh = make(chan error, 2)
	)

	reorgBlock := uint64(rand.Intn(int(maxBlockNum/2)) + int(maxBlockNum/4)) // middle 50%
	t.Logf("📍 Chosen reorg block: %d", reorgBlock)

	var maxAllowedBlock atomic.Uint64
	maxAllowedBlock.Store(math.MaxUint64)

	// Block processing goroutine
	wg.Go(func() {
		for i := uint64(0); i <= maxBlockNum; i++ {
			select {
			case <-ctx.Done():
				t.Logf("🛑 Stopping block processing at block %d", i)
				return
			default:
			}

			if i >= maxAllowedBlock.Load() {
				t.Logf("🚫 Skipping ProcessBlock(%d) due to reorg limit", i)
				return
			}

			block := aggkitsync.Block{Num: i}
			block.Events = []any{
				Event{
					UpdateL1InfoTree: &UpdateL1InfoTree{
						BlockPosition:   i,
						MainnetExitRoot: common.HexToHash(fmt.Sprintf("%x", i)),
						RollupExitRoot:  common.HexToHash(fmt.Sprintf("%x", i)),
					},
				},
			}

			if err := p.ProcessBlock(ctx, block); err != nil && !strings.Contains(err.Error(), "context canceled") {
				errCh <- fmt.Errorf("❌ ProcessBlock(%d) failed: %w", i, err)
				return
			}
			t.Logf("✅ Processed block %d", i)

			time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
		}
	})

	// Reorg goroutine
	wg.Go(func() {
		time.Sleep(time.Duration(rand.Intn(200)+50) * time.Millisecond)

		t.Logf("🔄 Starting Reorg to block %d", reorgBlock)
		if err := p.Reorg(ctx, reorgBlock); err != nil {
			errCh <- fmt.Errorf("❌ Reorg to block %d failed: %w", reorgBlock, err)
			return
		} else {
			t.Logf("✅ Reorg to %d succeeded", reorgBlock)
		}

		maxAllowedBlock.Store(reorgBlock)

		// Cancel context to stop block processing
		cancel()
	})

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	// Assert DB state
	rows, err := p.db.QueryContext(context.Background(), `SELECT num FROM block`)
	require.NoError(t, err)
	defer rows.Close()

	var remaining []uint64
	for rows.Next() {
		var n uint64
		require.NoError(t, rows.Scan(&n))
		remaining = append(remaining, n)
	}
	require.NoError(t, rows.Err())

	t.Logf("🧾 Remaining blocks in DB: %v", remaining)
	for _, n := range remaining {
		require.Truef(t, n < reorgBlock, "Block %d should not be in DB (>= reorgBlock %d)", n, reorgBlock)
	}
}

func TestProcessBlockUpdateL1InfoTreeV2DontMatchTree(t *testing.T) {
	sut, err := newProcessor(
		path.Join(t.TempDir(), "l1infotreesyncTestProcessBlockUpdateL1InfoTreeV2DontMatchTree.sqlite"))
	require.NoError(t, err)
	block := aggkitsync.Block{
		Num: 10,
		Events: []any{
			Event{
				UpdateL1InfoTree: &UpdateL1InfoTree{
					MainnetExitRoot: common.HexToHash("beef"),
					RollupExitRoot:  common.HexToHash("5ca1e"),
					ParentHash:      common.HexToHash("1010101"),
					Timestamp:       420,
				}},
			Event{UpdateL1InfoTreeV2: &UpdateL1InfoTreeV2{
				CurrentL1InfoRoot: common.HexToHash("beef"),
				LeafCount:         1,
			}},
		},
	}
	err = sut.ProcessBlock(context.Background(), block)
	require.ErrorIs(t, err, aggkitsync.ErrInconsistentState)
	require.True(t, sut.halted)
}

func TestGetProcessedBlockUntil(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestGetProcessedBlockUntil.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	// Test when no blocks are present
	_, _, err = p.GetProcessedBlockUntil(ctx, 1)
	require.Error(t, err)

	// Insert some blocks
	_, err = p.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 1, "0x1")
	require.NoError(t, err)
	_, err = p.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 2, "0x2")
	require.NoError(t, err)
	_, err = p.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 3, "0x3")
	require.NoError(t, err)

	// Test when blockNum is less than the first block
	_, _, err = p.GetProcessedBlockUntil(ctx, 0)
	require.ErrorIs(t, err, sql.ErrNoRows)

	// Test when blockNum is exactly the first block
	blockNum, blockHash, err := p.GetProcessedBlockUntil(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), blockNum)
	require.Equal(t, common.HexToHash("0x1"), blockHash)

	// Test when blockNum is between two blocks
	blockNum, blockHash, err = p.GetProcessedBlockUntil(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), blockNum)
	require.Equal(t, common.HexToHash("0x2"), blockHash)

	// Test when blockNum is exactly the last block
	blockNum, blockHash, err = p.GetProcessedBlockUntil(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(3), blockNum)
	require.Equal(t, common.HexToHash("0x3"), blockHash)

	// Test when blockNum is greater than the last block
	blockNum, blockHash, err = p.GetProcessedBlockUntil(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, uint64(3), blockNum)
	require.Equal(t, common.HexToHash("0x3"), blockHash)

	// Test when hash is nil
	_, err = p.db.Exec(`INSERT INTO block (num) VALUES ($1)`, 4)
	require.NoError(t, err)

	blockNum, blockHash, err = p.GetProcessedBlockUntil(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, uint64(4), blockNum)
	require.Equal(t, common.Hash{}, blockHash)
}

func TestProcessorGetLatestL1InfoGER(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	dbPath := path.Join(t.TempDir(), "TestGetLatestL1InfoGER.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)

	// Querying latest GER on empty processor should return error
	latestGER, err := p.GetLatestL1InfoGER(ctx)
	require.ErrorIs(t, err, db.ErrNotFound)
	require.Equal(t, common.Hash{}, latestGER)

	addBlock := func(num, pos uint64, mainnetRoot, rollupRoot, parentHash string, ts uint64) {
		err := p.ProcessBlock(ctx, aggkitsync.Block{
			Num: num,
			Events: []any{
				Event{
					UpdateL1InfoTree: &UpdateL1InfoTree{
						BlockPosition:   pos,
						MainnetExitRoot: common.HexToHash(mainnetRoot),
						RollupExitRoot:  common.HexToHash(rollupRoot),
						ParentHash:      common.HexToHash(parentHash),
						Timestamp:       ts,
					},
				},
			},
		})
		require.NoError(t, err)
	}

	// Insert blocks
	addBlock(1, 1, "beef", "5ca1e", "1010101", 420)
	addBlock(2, 1, "aabb", "ccdd", "10101010", 421)

	// Check latest GER on non empty database
	gotGER, err := p.GetLatestL1InfoGER(ctx)
	require.NoError(t, err)

	wantGER := crypto.Keccak256Hash(
		common.HexToHash("aabb").Bytes(),
		common.HexToHash("ccdd").Bytes(),
	)

	require.Equal(t, wantGER, gotGER, "latest GER should match last processed block")
}

func TestCalculateGER(t *testing.T) {
	cases := []struct {
		testName        string
		mainnetExitRoot common.Hash
		rollupExitRoot  common.Hash
		expectedGER     common.Hash
	}{
		{
			testName:        "both MER and RER non-zero",
			mainnetExitRoot: common.HexToHash("0xdde590a282827306734e608dac3f46fbd0c7d2ad9a2f2fa231619bf717074ebc"),
			rollupExitRoot:  common.HexToHash("0xfa61f6390bc150b7daa1f876b3b750e1a5e3ae1582dbd014887aff8657c1e947"),
			expectedGER:     common.HexToHash("0x7fe6169049e0e70ed4f6f5c15f00ccf1f312da1ebff927e8fa65fff0dbab1e0e"),
		},
		{
			testName:        "MER non-zero, RER zero",
			mainnetExitRoot: common.HexToHash("0x3f586b16c4b88ef284961e9fdd12b414e8e8227311c968f94454dc46680a9701"),
			rollupExitRoot:  common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000"),
			expectedGER:     common.HexToHash("0x8e4eda1ee01e9df0f792b4b84fdbcdbe1393f73dc03d81381557140445b28e76"),
		},
	}

	for _, c := range cases {
		t.Run(c.testName, func(t *testing.T) {
			calculatedGER := CalculateGER(c.mainnetExitRoot, c.rollupExitRoot)
			require.Equal(t, c.expectedGER, calculatedGER)
		})
	}
}

func TestGetLastProcessedBlockHeader(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("returns nil when no blocks are processed", func(t *testing.T) {
		t.Parallel()
		dbPath := path.Join(t.TempDir(), "TestGetLastProcessedBlockHeader_empty.sqlite")
		p, err := newProcessor(dbPath)
		require.NoError(t, err)

		hdr, err := p.GetLastProcessedBlockHeader(ctx)
		require.NoError(t, err)
		require.Nil(t, hdr)
	})

	t.Run("returns last processed block when single block exists", func(t *testing.T) {
		t.Parallel()
		dbPath := path.Join(t.TempDir(), "TestGetLastProcessedBlockHeader_single.sqlite")
		p, err := newProcessor(dbPath)
		require.NoError(t, err)

		expectedHash := common.HexToHash("0xabc123")
		expectedNum := uint64(1)
		_, err = p.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, expectedNum, expectedHash.String())
		require.NoError(t, err)

		hdr, err := p.GetLastProcessedBlockHeader(ctx)
		require.NoError(t, err)
		require.NotNil(t, hdr)
		require.Equal(t, expectedNum, hdr.Number)
		require.Equal(t, expectedHash, hdr.Hash)
	})

	t.Run("returns last processed block when multiple blocks exist", func(t *testing.T) {
		t.Parallel()
		dbPath := path.Join(t.TempDir(), "TestGetLastProcessedBlockHeader_multiple.sqlite")
		p, err := newProcessor(dbPath)
		require.NoError(t, err)

		// Insert multiple blocks
		_, err = p.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 1, common.HexToHash("0x1").String())
		require.NoError(t, err)
		_, err = p.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 2, common.HexToHash("0x2").String())
		require.NoError(t, err)
		expectedHash := common.HexToHash("0x3")
		expectedNum := uint64(3)
		_, err = p.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, expectedNum, expectedHash.String())
		require.NoError(t, err)

		hdr, err := p.GetLastProcessedBlockHeader(ctx)
		require.NoError(t, err)
		require.NotNil(t, hdr)
		require.Equal(t, expectedNum, hdr.Number)
		require.Equal(t, expectedHash, hdr.Hash)
	})
}

// TestReorgUnhaltsWhenNoRowsAffected covers the recovery path of the cardona-67-op incident
// (2026-07-23): a halt caused by a batch whose tx rolled back leaves nothing persisted, so a
// recovery Reorg deletes 0 rows — it must still unhalt the processor.
func TestReorgUnhaltsWhenNoRowsAffected(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestReorgUnhaltsWhenNoRowsAffected.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)

	p.halt("test: poisoned in-memory batch")
	require.True(t, p.isHalted())

	require.NoError(t, p.Reorg(context.Background(), 100))
	require.False(t, p.isHalted(), "Reorg must unhalt even when it deleted 0 rows")
}

// TestReorgUnhaltsWhenRowsAffected guards the previously-working branch: a Reorg that actually
// purges committed rows keeps unhalting the processor.
func TestReorgUnhaltsWhenRowsAffected(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestReorgUnhaltsWhenRowsAffected.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num: 1,
		Events: []any{
			Event{UpdateL1InfoTree: &UpdateL1InfoTree{
				MainnetExitRoot: common.HexToHash("beef"),
				RollupExitRoot:  common.HexToHash("5ca1e"),
				ParentHash:      common.HexToHash("1010101"),
				Timestamp:       420,
			}},
		},
	})
	require.NoError(t, err)

	p.halt("test: halted after committed block")
	require.True(t, p.isHalted())

	require.NoError(t, p.Reorg(ctx, 1))
	require.False(t, p.isHalted())
}

// TestProcessBlocksWorksAfterUnhaltingReorg verifies the full recovery cycle used by the
// multidownloader driver: halt -> ProcessBlocks short-circuits with ErrInconsistentState ->
// Reorg (0 rows) unhalts -> a valid batch is processed and persisted.
func TestProcessBlocksWorksAfterUnhaltingReorg(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestProcessBlocksWorksAfterUnhaltingReorg.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	batch := &mdrsynctypes.DownloadResult{
		Data: aggkitsync.EVMBlocks{
			{
				EVMBlockHeader: aggkitsync.EVMBlockHeader{Num: 1},
				Events: []any{
					Event{UpdateL1InfoTree: &UpdateL1InfoTree{
						MainnetExitRoot: common.HexToHash("beef"),
						RollupExitRoot:  common.HexToHash("5ca1e"),
						ParentHash:      common.HexToHash("1010101"),
						Timestamp:       420,
					}},
				},
			},
		},
		CompletionPercentage: 100,
	}

	p.halt("test: poisoned in-memory batch")
	err = p.ProcessBlocks(ctx, batch)
	require.ErrorIs(t, err, aggkitsync.ErrInconsistentState)

	require.NoError(t, p.Reorg(ctx, 1))
	require.False(t, p.isHalted())

	require.NoError(t, p.ProcessBlocks(ctx, batch))
	lastProcessed, _, err := p.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), lastProcessed)
	info, err := p.GetLastInfo()
	require.NoError(t, err)
	require.Equal(t, uint64(1), info.BlockNumber)
}

// commitPassingCheckpoint processes a leaf-adding block at leafBlock, fills every block in
// between with an empty block (so block numbers stay contiguous for callers that also process
// surrounding blocks), and finally a block at checkpointBlock whose UpdateL1InfoTreeV2 event
// matches the resulting root exactly. The V2 sanity check therefore passes and checkpointBlock is
// persisted as the last verified checkpoint.
func commitPassingCheckpoint(t *testing.T, p *processor, leafBlock, checkpointBlock uint64) {
	t.Helper()
	ctx := context.Background()

	err := p.ProcessBlock(ctx, aggkitsync.Block{
		Num: leafBlock,
		Events: []any{
			Event{UpdateL1InfoTree: &UpdateL1InfoTree{
				MainnetExitRoot: common.HexToHash(fmt.Sprintf("%x", leafBlock)),
				RollupExitRoot:  common.HexToHash("5ca1e"),
				ParentHash:      common.HexToHash("1010101"),
				Timestamp:       420,
			}},
		},
	})
	require.NoError(t, err)

	for n := leafBlock + 1; n < checkpointBlock; n++ {
		require.NoError(t, p.ProcessBlock(ctx, aggkitsync.Block{Num: n}))
	}

	root, err := p.l1InfoTree.GetLastRoot(p.db)
	require.NoError(t, err)

	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num: checkpointBlock,
		Events: []any{
			Event{UpdateL1InfoTreeV2: &UpdateL1InfoTreeV2{
				CurrentL1InfoRoot: root.Hash,
				LeafCount:         root.Index + 1,
			}},
		},
	})
	require.NoError(t, err)
	require.False(t, p.isHalted())
}

// TestProcessorPersistsCheckpointOnPassingV2Check covers the write side of the self-healing fix:
// when the UpdateL1InfoTreeV2 sanity check passes, the block that carried it must be recorded as
// the last verified checkpoint.
func TestProcessorPersistsCheckpointOnPassingV2Check(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestProcessorPersistsCheckpointOnPassingV2Check.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)

	checkpointBlock, found, err := getCheckpointBlockWithTx(p.db)
	require.NoError(t, err)
	require.False(t, found, "no checkpoint should exist before any block is processed")
	require.Zero(t, checkpointBlock)

	commitPassingCheckpoint(t, p, 1, 2)

	checkpointBlock, found, err = getCheckpointBlockWithTx(p.db)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(2), checkpointBlock)
}

// TestProcessorCheckpointAtomicWithBatch verifies that persisting the checkpoint is atomic with
// the rest of the batch: if the same block's tx later fails and rolls back (here, via a
// verify_batches primary-key collision), the checkpoint set earlier in that same tx must not be
// persisted either.
func TestProcessorCheckpointAtomicWithBatch(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestProcessorCheckpointAtomicWithBatch.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num: 1,
		Events: []any{
			Event{UpdateL1InfoTree: &UpdateL1InfoTree{
				MainnetExitRoot: common.HexToHash("beef"),
				RollupExitRoot:  common.HexToHash("5ca1e"),
				ParentHash:      common.HexToHash("1010101"),
				Timestamp:       420,
			}},
		},
	})
	require.NoError(t, err)

	root, err := p.l1InfoTree.GetLastRoot(p.db)
	require.NoError(t, err)

	// Block 2: the V2 checkpoint passes first, then two VerifyBatches events collide on the same
	// (block_num, block_pos) primary key, forcing the whole block's tx to roll back.
	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num: 2,
		Events: []any{
			Event{UpdateL1InfoTreeV2: &UpdateL1InfoTreeV2{
				CurrentL1InfoRoot: root.Hash,
				LeafCount:         root.Index + 1,
			}},
			Event{VerifyBatches: &VerifyBatches{
				BlockPosition: 0,
				RollupID:      1,
				NumBatch:      1,
				StateRoot:     common.HexToHash("aaaa"),
				ExitRoot:      common.HexToHash("bbbb"),
			}},
			Event{VerifyBatches: &VerifyBatches{
				BlockPosition: 0, // duplicate block_pos -> PRIMARY KEY collision on insert
				RollupID:      2,
				NumBatch:      1,
				StateRoot:     common.HexToHash("cccc"),
				ExitRoot:      common.HexToHash("dddd"),
			}},
		},
	})
	require.Error(t, err)

	_, found, err := getCheckpointBlockWithTx(p.db)
	require.NoError(t, err)
	require.False(t, found, "checkpoint must not survive a rolled-back batch")
}

// TestReorgClearsCheckpointWhenPurgingAtOrPastIt verifies that a Reorg purging blocks at or after
// the checkpoint's own block clears the stored checkpoint, since it no longer vouches for data
// that no longer exists.
func TestReorgClearsCheckpointWhenPurgingAtOrPastIt(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestReorgClearsCheckpointWhenPurgingAtOrPastIt.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	commitPassingCheckpoint(t, p, 1, 5)

	require.NoError(t, p.Reorg(ctx, 3))

	_, found, err := getCheckpointBlockWithTx(p.db)
	require.NoError(t, err)
	require.False(t, found, "checkpoint at block 5 must be cleared by a reorg purging from block 3")
}

// TestReorgKeepsCheckpointWhenPurgingAfterIt verifies that a Reorg purging blocks strictly after
// the checkpoint's own block leaves the checkpoint untouched.
func TestReorgKeepsCheckpointWhenPurgingAfterIt(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestReorgKeepsCheckpointWhenPurgingAfterIt.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	commitPassingCheckpoint(t, p, 1, 5)

	require.NoError(t, p.Reorg(ctx, 10))

	checkpointBlock, found, err := getCheckpointBlockWithTx(p.db)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(5), checkpointBlock)
}

// TestReorgFirstAttemptDoesNotEscalate verifies that the first Reorg recovering from a halt at
// block B purges exactly at the requested block, with no escalation.
func TestReorgFirstAttemptDoesNotEscalate(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestReorgFirstAttemptDoesNotEscalate.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	for n := uint64(1); n <= 10; n++ {
		require.NoError(t, p.ProcessBlock(ctx, aggkitsync.Block{Num: n}))
	}

	haltedBlock := uint64(50)
	p.haltAtBlock("test: first halt at block 50", &haltedBlock)
	require.True(t, p.isHalted())

	require.NoError(t, p.Reorg(ctx, 8))
	require.False(t, p.isHalted())

	lastProcessed, _, err := p.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(7), lastProcessed, "first recovery attempt must purge exactly from the requested block")

	require.NotNil(t, p.lastReorgRecoveryBlock)
	require.Equal(t, haltedBlock, *p.lastReorgRecoveryBlock)
}

// TestReorgEscalatesOnSecondConsecutiveHaltAtSameBlock is the core regression test for the
// bokuto incident (2026-08-05): a v0.11.0-rc2 processor kept halting at the exact same block
// because the true divergence was in already-committed data. The second consecutive Reorg
// recovery for that same halted block must escalate the purge back to the last verified
// checkpoint, deep enough to actually reach the divergence.
func TestReorgEscalatesOnSecondConsecutiveHaltAtSameBlock(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestReorgEscalatesOnSecondConsecutiveHaltAtSameBlock.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()

	// A verified checkpoint at block 5 (the divergence, by construction, is at or before it).
	commitPassingCheckpoint(t, p, 1, 5)
	for n := uint64(6); n <= 10; n++ {
		require.NoError(t, p.ProcessBlock(ctx, aggkitsync.Block{Num: n}))
	}

	haltedBlock := uint64(50)

	// First halt+recovery attempt at block 50: shallow purge, no progress made (the driver
	// re-downloads and hits the exact same failure at block 50 again).
	p.haltAtBlock("test: halt #1 at block 50", &haltedBlock)
	require.NoError(t, p.Reorg(ctx, 50))
	require.False(t, p.isHalted())

	// Second consecutive halt at the very same block: escalate.
	p.haltAtBlock("test: halt #2 at block 50 (no progress)", &haltedBlock)
	require.NoError(t, p.Reorg(ctx, 50))
	require.False(t, p.isHalted())

	lastProcessed, _, err := p.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(4), lastProcessed,
		"escalated recovery must purge back to (and including) the checkpoint block, so it is re-verified")

	_, found, err := getCheckpointBlockWithTx(p.db)
	require.NoError(t, err)
	require.False(t, found, "the checkpoint's own block was purged, so the checkpoint must be cleared")
}

// TestReorgEscalationFallsBackToInitialBlockWhenNoCheckpoint verifies that, absent any verified
// checkpoint (fresh DB, or one created before this upgrade), escalation falls back to the
// syncer's configured initial block, i.e. a full resync.
func TestReorgEscalationFallsBackToInitialBlockWhenNoCheckpoint(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestReorgEscalationFallsBackToInitialBlockWhenNoCheckpoint.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)
	p.initialBlock = 3
	ctx := context.Background()

	for n := uint64(1); n <= 10; n++ {
		require.NoError(t, p.ProcessBlock(ctx, aggkitsync.Block{Num: n}))
	}

	_, found, err := getCheckpointBlockWithTx(p.db)
	require.NoError(t, err)
	require.False(t, found, "fixture broken: no checkpoint should have been recorded")

	haltedBlock := uint64(100)
	p.haltAtBlock("test: halt #1 at block 100", &haltedBlock)
	require.NoError(t, p.Reorg(ctx, 100))
	require.False(t, p.isHalted())

	p.haltAtBlock("test: halt #2 at block 100 (no progress)", &haltedBlock)
	require.NoError(t, p.Reorg(ctx, 100))
	require.False(t, p.isHalted())

	lastProcessed, _, err := p.GetLastProcessedBlock(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), lastProcessed,
		"escalation without a checkpoint must fall back to the syncer's initial block")
}
