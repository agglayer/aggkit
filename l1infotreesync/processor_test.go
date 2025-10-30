package l1infotreesync

import (
	"database/sql"
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
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
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
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTestGetLatestInfoUntilBlockIfNotFoundReturnsErrNotFound.sqlite")
	sut, err := newProcessor(dbPath)
	require.NoError(t, err)
	ctx := context.Background()
	// Fake block 1
	_, err = sut.db.Exec(`INSERT INTO block (num, hash) VALUES ($1, $2)`, 1, "0x1")
	require.NoError(t, err)

	blockNum := uint64(1)
	_, err = sut.GetLatestL1InfoLeafUntilBlock(ctx, &blockNum)
	require.Equal(t, db.ErrNotFound, err)
}

func TestProcessor_GetL1InfoTreeMerkleProof(t *testing.T) {
	testTable := []struct {
		name         string
		getProcessor func(t *testing.T) *processor
		idx          uint32
		expectedRoot types.Root
		expectedErr  error
	}{
		{
			name: "empty tree",
			getProcessor: func(t *testing.T) *processor {
				t.Helper()

				p, err := newProcessor(path.Join(t.TempDir(), "l1infotreesyncTestProcessor_GetL1InfoTreeMerkleProof_1.sqlite"))
				require.NoError(t, err)

				return p
			},
			idx:         0,
			expectedErr: db.ErrNotFound,
		},
		{
			name: "single leaf tree",
			getProcessor: func(t *testing.T) *processor {
				t.Helper()

				p, err := newProcessor(path.Join(t.TempDir(), "l1infotreesyncTestProcessor_GetL1InfoTreeMerkleProof_2.sqlite"))
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
			idx: 0,
			expectedRoot: types.Root{
				Hash:          common.HexToHash("beef"),
				Index:         0,
				BlockNum:      1,
				BlockPosition: 0,
			},
		},
	}

	for _, tt := range testTable {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.getProcessor(t)
			proof, root, err := p.GetL1InfoTreeMerkleProof(context.Background(), tt.idx)
			if tt.expectedErr != nil {
				require.Equal(t, tt.expectedErr, err)
			} else {
				require.NoError(t, err)
				require.NotEmpty(t, proof)
				require.NotEmpty(t, root.Hash)
				require.Equal(t, tt.expectedRoot.Index, root.Index)
				require.Equal(t, tt.expectedRoot.BlockNum, root.BlockNum)
				require.Equal(t, tt.expectedRoot.BlockPosition, root.BlockPosition)
			}
		})
	}
}

func TestProcessor_Reorg(t *testing.T) {
	t.Parallel()

	testTable := []struct {
		name         string
		getProcessor func(t *testing.T) *processor
		reorgBlock   uint64
		expectedErr  error
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
			expectedErr: nil,
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
			reorgBlock: 1,
		},
	}

	for _, tt := range testTable {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := tt.getProcessor(t)
			err := p.Reorg(context.Background(), tt.reorgBlock)
			if tt.expectedErr != nil {
				require.Equal(t, tt.expectedErr, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestProcessBlockUpdateL1InfoTreeV2DontMatchTree(t *testing.T) {
	sut, err := newProcessor(path.Join(t.TempDir(), "l1infotreesyncTestProcessBlockUpdateL1InfoTreeV2DontMatchTree.sqlite"))
	require.NoError(t, err)
	block := aggkitsync.Block{
		Num: 10,
		Events: []interface{}{
			Event{UpdateL1InfoTree: &UpdateL1InfoTree{
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

	// Check latest GER
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
