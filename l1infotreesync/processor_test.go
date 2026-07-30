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

func TestProcessor_Reorg_PublishesGERReorgEvent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := path.Join(t.TempDir(), "processor_reorg_publishes_event.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)

	// Subscribe to reorg events
	reorgCh := p.gerReorgNotifier.Subscribe("test-subscriber")

	// Create initial state with multiple L1InfoTree leaves
	info1 := &UpdateL1InfoTree{
		MainnetExitRoot: common.HexToHash("beef"),
		RollupExitRoot:  common.HexToHash("5ca1e"),
		ParentHash:      common.HexToHash("1010101"),
		Timestamp:       420,
		BlockPosition:   0,
	}
	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num:    1,
		Hash:   common.HexToHash("block1"),
		Events: []interface{}{Event{UpdateL1InfoTree: info1}},
	})
	require.NoError(t, err)

	info2 := &UpdateL1InfoTree{
		MainnetExitRoot: common.HexToHash("dead"),
		RollupExitRoot:  common.HexToHash("c0de"),
		ParentHash:      common.HexToHash("2020202"),
		Timestamp:       421,
		BlockPosition:   0,
	}
	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num:    2,
		Hash:   common.HexToHash("block2"),
		Events: []interface{}{Event{UpdateL1InfoTree: info2}},
	})
	require.NoError(t, err)

	info3 := &UpdateL1InfoTree{
		MainnetExitRoot: common.HexToHash("fade"),
		RollupExitRoot:  common.HexToHash("babe"),
		ParentHash:      common.HexToHash("3030303"),
		Timestamp:       422,
		BlockPosition:   0,
	}
	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num:    3,
		Hash:   common.HexToHash("block3"),
		Events: []interface{}{Event{UpdateL1InfoTree: info3}},
	})
	require.NoError(t, err)

	// Verify initial state
	lastInfo, err := p.GetLastInfo()
	require.NoError(t, err)
	require.Equal(t, uint64(3), lastInfo.BlockNumber)
	require.Equal(t, uint32(2), lastInfo.L1InfoTreeIndex)

	// Perform reorg from block 2
	err = p.Reorg(ctx, 2)
	require.NoError(t, err)

	// Verify reorg event was published
	select {
	case event := <-reorgCh:
		require.Equal(t, uint64(2), event.FirstReorgedBlock)
		require.Len(t, event.ReorgedLeaves, 2, "should have 2 reorged leaves (blocks 2 and 3)")

		// Verify the reorged leaves contain correct data
		require.Equal(t, uint64(2), event.ReorgedLeaves[0].BlockNumber)
		require.Equal(t, uint32(1), event.ReorgedLeaves[0].L1InfoTreeIndex)
		require.Equal(t, CalculateGER(info2.MainnetExitRoot, info2.RollupExitRoot),
			event.ReorgedLeaves[0].GlobalExitRoot)

		require.Equal(t, uint64(3), event.ReorgedLeaves[1].BlockNumber)
		require.Equal(t, uint32(2), event.ReorgedLeaves[1].L1InfoTreeIndex)
		require.Equal(t, CalculateGER(info3.MainnetExitRoot, info3.RollupExitRoot),
			event.ReorgedLeaves[1].GlobalExitRoot)

		require.Greater(t, event.Timestamp, uint64(0), "timestamp should be set")
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for reorg event")
	}

	// Verify database state after reorg
	lastInfo, err = p.GetLastInfo()
	require.NoError(t, err)
	require.Equal(t, uint64(1), lastInfo.BlockNumber, "only block 1 should remain")
	require.Equal(t, uint32(0), lastInfo.L1InfoTreeIndex)
}

func TestProcessor_Reorg_NoEventWhenNoLeaves(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := path.Join(t.TempDir(), "processor_reorg_no_event.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)

	// Subscribe to reorg events
	reorgCh := p.gerReorgNotifier.Subscribe("test-subscriber")

	// Create a block without any L1InfoTree events
	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num:    1,
		Hash:   common.HexToHash("block1"),
		Events: []interface{}{},
	})
	require.NoError(t, err)

	// Perform reorg
	err = p.Reorg(ctx, 1)
	require.NoError(t, err)

	// Verify NO event was published (since no l1info_leaf entries were affected)
	select {
	case event := <-reorgCh:
		t.Fatalf("unexpected reorg event received: %+v", event)
	case <-time.After(100 * time.Millisecond):
		// Expected: no event
	}
}

func TestProcessor_Reorg_NoEventWhenRowsNotAffected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := path.Join(t.TempDir(), "processor_reorg_no_rows.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)

	// Subscribe to reorg events
	reorgCh := p.gerReorgNotifier.Subscribe("test-subscriber")

	// Create initial state
	info1 := &UpdateL1InfoTree{
		MainnetExitRoot: common.HexToHash("beef"),
		RollupExitRoot:  common.HexToHash("5ca1e"),
		ParentHash:      common.HexToHash("1010101"),
		Timestamp:       420,
		BlockPosition:   0,
	}
	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num:    1,
		Hash:   common.HexToHash("block1"),
		Events: []interface{}{Event{UpdateL1InfoTree: info1}},
	})
	require.NoError(t, err)

	// Reorg from block 10 (which doesn't exist)
	err = p.Reorg(ctx, 10)
	require.NoError(t, err)

	// Verify NO event was published (since rowsAffected == 0)
	select {
	case event := <-reorgCh:
		t.Fatalf("unexpected reorg event received: %+v", event)
	case <-time.After(100 * time.Millisecond):
		// Expected: no event
	}
}

func TestProcessor_Reorg_MultipleSubscribers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := path.Join(t.TempDir(), "processor_reorg_multiple_subs.sqlite")
	p, err := newProcessor(dbPath)
	require.NoError(t, err)

	// Create multiple subscribers
	sub1Ch := p.gerReorgNotifier.Subscribe("subscriber-1")
	sub2Ch := p.gerReorgNotifier.Subscribe("subscriber-2")
	sub3Ch := p.gerReorgNotifier.Subscribe("subscriber-3")

	// Create initial state
	info1 := &UpdateL1InfoTree{
		MainnetExitRoot: common.HexToHash("beef"),
		RollupExitRoot:  common.HexToHash("5ca1e"),
		ParentHash:      common.HexToHash("1010101"),
		Timestamp:       420,
		BlockPosition:   0,
	}
	err = p.ProcessBlock(ctx, aggkitsync.Block{
		Num:    1,
		Hash:   common.HexToHash("block1"),
		Events: []interface{}{Event{UpdateL1InfoTree: info1}},
	})
	require.NoError(t, err)

	// Perform reorg
	err = p.Reorg(ctx, 1)
	require.NoError(t, err)

	// Verify all subscribers receive the event
	var wg sync.WaitGroup
	wg.Add(3)

	checkSubscriber := func(ch <-chan GERReorgEvent, name string) {
		defer wg.Done()
		select {
		case event := <-ch:
			require.Equal(t, uint64(1), event.FirstReorgedBlock)
			require.Len(t, event.ReorgedLeaves, 1)
			t.Logf("%s received event successfully", name)
		case <-time.After(1 * time.Second):
			t.Errorf("%s: timeout waiting for reorg event", name)
		}
	}

	go checkSubscriber(sub1Ch, "subscriber-1")
	go checkSubscriber(sub2Ch, "subscriber-2")
	go checkSubscriber(sub3Ch, "subscriber-3")

	wg.Wait()
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
