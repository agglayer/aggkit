package l2gersync

import (
	"context"
	"fmt"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func Test_getLatestL1InfoTreeIndex(t *testing.T) {
	t.Parallel()
	testDir := path.Join(t.TempDir(), "l2gersync_Test_getLatestL1InfoTreeIndex.sqlite")
	processor, err := newProcessor(testDir)
	require.NoError(t, err)

	block := sync.Block{
		Num:    1,
		Hash:   common.Hash{},
		Events: []any{newEvent(newGlobalExitRootInfo(common.HexToHash("0x1"), 2, 1, 0), GEREventTypeInsert)},
	}
	err = processor.ProcessBlock(context.TODO(), block)
	require.NoError(t, err)

	index, err := processor.getLatestL1InfoTreeIndex()
	require.NoError(t, err)
	require.Equal(t, uint32(2), index)
}

func TestProcessBlock(t *testing.T) {
	t.Parallel()
	l1InfoTreeIndex := uint32(42)

	tests := []struct {
		name          string
		blocks        []sync.Block
		expectedIndex uint32
		expectedErr   string
	}{
		{
			name: "Add GERInfo",
			blocks: []sync.Block{
				{
					Num: 1,
					Events: []any{
						&Event{
							GERInfo: newGlobalExitRootInfo(common.HexToHash("0x1234"), l1InfoTreeIndex, 1, 0),
						},
					},
				},
			},
			expectedIndex: l1InfoTreeIndex,
		},
		{
			name: "Remove GER event",
			blocks: []sync.Block{
				{
					Num: 2,
					Events: []any{
						&Event{
							GERInfo:   newGlobalExitRootInfo(common.HexToHash("0xffee"), l1InfoTreeIndex, 2, 0),
							EventType: GEREventTypeInsert,
						},
						&Event{
							GERInfo:   newGlobalExitRootInfo(common.HexToHash("0xffee"), 0, 2, 0),
							EventType: GEREventTypeRemove,
						},
					},
				},
			},
			expectedIndex: 0,
			expectedErr:   db.ErrNotFound.Error(),
		},
		{
			name: "Insert multiple GER events and remove",
			blocks: []sync.Block{
				{
					Num: 3,
					Events: []any{
						&Event{
							GERInfo: newGlobalExitRootInfo(common.HexToHash("0x1234"), l1InfoTreeIndex, 3, 0),
						},
					},
				},
				{
					Num: 4,
					Events: []any{
						&Event{
							GERInfo: newGlobalExitRootInfo(common.HexToHash("0x5678"), l1InfoTreeIndex+1, 4, 0),
						},
					},
				},
				{
					Num: 5,
					Events: []any{
						&Event{
							GERInfo: newGlobalExitRootInfo(common.HexToHash("0x9876"), l1InfoTreeIndex+2, 5, 0),
						},
					},
				},
				{
					Num: 6,
					Events: []any{
						&Event{
							GERInfo:   newGlobalExitRootInfo(common.HexToHash("0x9876"), 0, 6, 0),
							EventType: GEREventTypeRemove,
						},
					},
				},
			},
			expectedIndex: l1InfoTreeIndex + 1,
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			testDir := path.Join(t.TempDir(), fmt.Sprintf("l2gersync_Test_ProcessBlock_%s.sqlite", tt.name))
			p, err := newProcessor(testDir)
			require.NoError(t, err)

			for _, b := range tt.blocks {
				err := p.ProcessBlock(ctx, b)
				require.NoError(t, err)
			}

			index, err := p.getLatestL1InfoTreeIndex()
			if tt.expectedErr == "" {
				require.NoError(t, err)
				require.Equal(t, tt.expectedIndex, index)
			} else {
				require.ErrorContains(t, err, tt.expectedErr)
			}
		})
	}
}

func TestReorg(t *testing.T) {
	testDir := path.Join(t.TempDir(), "l2gersync_TestReorg.sqlite")
	processor, err := newProcessor(testDir)
	require.NoError(t, err)

	block1 := sync.Block{
		Num:  1,
		Hash: common.Hash{},
		Events: []any{
			&Event{
				GERInfo: newGlobalExitRootInfo(common.HexToHash("0x1"), 2, 1, 0),
			},
		},
	}
	block2 := sync.Block{
		Num:  2,
		Hash: common.Hash{},
		Events: []any{
			&Event{
				GERInfo: newGlobalExitRootInfo(common.HexToHash("0x2"), 3, 2, 0),
			},
		},
	}
	err = processor.ProcessBlock(context.TODO(), block1)
	require.NoError(t, err)
	err = processor.ProcessBlock(context.TODO(), block2)
	require.NoError(t, err)

	err = processor.Reorg(context.TODO(), 2)
	require.NoError(t, err)

	blockNum, err := processor.GetLastProcessedBlock(context.TODO())
	require.NoError(t, err)
	require.Equal(t, uint64(1), blockNum)

	index, err := processor.getLatestL1InfoTreeIndex()
	require.NoError(t, err)
	require.Equal(t, uint32(2), index)
}

func TestProcessor_GetInjectedGERsForRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	blockPosition := uint64(0)
	makeGERs := func() []*GlobalExitRootInfo {
		return []*GlobalExitRootInfo{
			{GlobalExitRoot: common.HexToHash("0x1234"), BlockPosition: &blockPosition},
			{GlobalExitRoot: common.HexToHash("0x5678")},
			{GlobalExitRoot: common.HexToHash("0x9876")},
		}
	}

	setupProcessorWithGERs := func(t *testing.T, blocks []sync.Block) *processor {
		t.Helper()

		testDir := path.Join(t.TempDir(), "test.sqlite")
		processor, err := newProcessor(testDir)
		require.NoError(t, err)

		var l1InfoTreeIndex uint32
		for _, b := range blocks {
			for _, evt := range b.Events {
				if gerEvent, ok := evt.(*Event); ok && gerEvent.GERInfo != nil {
					gerEvent.GERInfo.L1InfoTreeIndex = l1InfoTreeIndex
					gerEvent.GERInfo.BlockNum = b.Num
					l1InfoTreeIndex++
				}
			}
			require.NoError(t, processor.ProcessBlock(t.Context(), b))
		}
		return processor
	}

	t.Run("invalid block range", func(t *testing.T) {
		t.Parallel()

		gerList := makeGERs()
		allBlocks := []sync.Block{
			{Num: 93, Events: []any{&Event{GERInfo: gerList[0]}}},
			{Num: 94, Events: []any{&Event{GERInfo: gerList[1]}}},
			{Num: 95, Events: []any{&Event{GERInfo: gerList[2]}}},
			{Num: 96, Events: []any{&Event{
				GERInfo: newGlobalExitRootInfo(
					gerList[2].GlobalExitRoot, 0, 0, 0),
				EventType: GEREventTypeRemove,
			}}},
		}

		processor := setupProcessorWithGERs(t, allBlocks)
		injectedGERsMap, err := processor.GetInjectedGERsForRange(ctx, 100, 10)
		require.ErrorContains(t, err, "invalid block range: fromBlock(100) > toBlock(10)")
		require.Empty(t, injectedGERsMap)
	})

	t.Run("returns only non-removed GERs", func(t *testing.T) {
		t.Parallel()

		gerList := makeGERs()
		allBlocks := []sync.Block{
			{Num: 93, Events: []any{&Event{GERInfo: gerList[0]}}},
			{Num: 94, Events: []any{&Event{GERInfo: gerList[1]}}},
			{Num: 95, Events: []any{&Event{GERInfo: gerList[2]}}},
			{Num: 96, Events: []any{&Event{
				GERInfo:   &GlobalExitRootInfo{GlobalExitRoot: gerList[2].GlobalExitRoot},
				EventType: GEREventTypeRemove,
			}}},
		}

		processor := setupProcessorWithGERs(t, allBlocks)
		injectedGERsMap, err := processor.GetInjectedGERsForRange(ctx,
			allBlocks[0].Num, allBlocks[2].Num)
		require.NoError(t, err)

		expectedGERs := gerList[:2] // The 3rd was removed
		require.Len(t, injectedGERsMap, len(expectedGERs))

		for _, expected := range expectedGERs {
			actual, ok := injectedGERsMap[expected.GlobalExitRoot]
			require.True(t, ok, "GER %s not found", expected.GlobalExitRoot.Hex())
			require.Equal(t, expected, &actual)
		}
	})

	t.Run("includes removed GER if block range excludes removal", func(t *testing.T) {
		t.Parallel()

		gerList := makeGERs()
		blocksExcludingRemoval := []sync.Block{
			{Num: 93, Events: []any{&Event{GERInfo: gerList[0]}}},
			{Num: 94, Events: []any{&Event{GERInfo: gerList[1]}}},
			{Num: 95, Events: []any{&Event{GERInfo: gerList[2]}}},
		}

		processor := setupProcessorWithGERs(t, blocksExcludingRemoval)
		injectedGERsMap, err := processor.GetInjectedGERsForRange(ctx,
			blocksExcludingRemoval[0].Num,
			blocksExcludingRemoval[len(blocksExcludingRemoval)-1].Num)
		require.NoError(t, err)

		require.Len(t, injectedGERsMap, 3)
		for _, expected := range gerList {
			actual, ok := injectedGERsMap[expected.GlobalExitRoot]
			require.True(t, ok, "GER %s not found", expected.GlobalExitRoot.Hex())
			require.Equal(t, expected, &actual)
		}
	})
}

func TestRemoveGEREvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := path.Join(t.TempDir(), "test_remove_ger_events.sqlite")
	processor, err := newProcessor(dbPath)
	require.NoError(t, err)

	ger1 := common.HexToHash("0x1234567890abcdef")
	ger2 := common.HexToHash("0xfedcba0987654321")

	t.Run("Insert and Remove GER Events", func(t *testing.T) {
		t.Parallel()
		insertEvent1 := &Event{
			GERInfo:   newGlobalExitRootInfo(ger1, 1, 100, 0),
			EventType: GEREventTypeInsert,
		}
		removeEvent1 := &Event{
			GERInfo:   newGlobalExitRootInfo(ger1, 0, 101, 1),
			EventType: GEREventTypeRemove,
		}
		removeEvent2 := &Event{
			GERInfo:   newGlobalExitRootInfo(ger2, 0, 102, 0),
			EventType: GEREventTypeRemove,
		}

		// Process blocks with events
		err = processor.ProcessBlock(ctx, sync.Block{
			Num:    100,
			Events: []any{insertEvent1},
			Hash:   common.HexToHash("0xblock100"),
		})
		require.NoError(t, err)

		err = processor.ProcessBlock(ctx, sync.Block{
			Num:    101,
			Events: []any{removeEvent1},
			Hash:   common.HexToHash("0xblock101"),
		})
		require.NoError(t, err)

		err = processor.ProcessBlock(ctx, sync.Block{
			Num:    102,
			Events: []any{removeEvent2},
			Hash:   common.HexToHash("0xblock102"),
		})
		require.NoError(t, err)

		// Test GetRemoveGEREvents - all events (no filters)
		allRemoveEvents, err := processor.GetRemoveGEREvents(ctx, nil, nil, nil)
		require.NoError(t, err)
		require.Len(t, allRemoveEvents, 2)

		// Verify first remove event
		require.Equal(t, ger1, allRemoveEvents[0].GlobalExitRoot)
		require.Equal(t, uint64(101), allRemoveEvents[0].BlockNum)
		require.Equal(t, uint64(1), allRemoveEvents[0].BlockPos) // Block position from removeEvent1
		require.Greater(t, allRemoveEvents[0].CreatedAt, uint64(0)) // CreatedAt should be set

		// Verify second remove event
		require.Equal(t, ger2, allRemoveEvents[1].GlobalExitRoot)
		require.Equal(t, uint64(102), allRemoveEvents[1].BlockNum)
		require.Equal(t, uint64(0), allRemoveEvents[1].BlockPos) // Block position from removeEvent2
		require.Greater(t, allRemoveEvents[1].CreatedAt, uint64(0)) // CreatedAt should be set

		// Test GetRemoveGEREvents by block range
		fromBlock := uint64(101)
		toBlock := uint64(101)
		rangeEvents, err := processor.GetRemoveGEREvents(ctx, nil, &fromBlock, &toBlock)
		require.NoError(t, err)
		require.Len(t, rangeEvents, 1)
		require.Equal(t, ger1, rangeEvents[0].GlobalExitRoot)

		// Test GetRemoveGEREvents by specific GER
		gerEvents, err := processor.GetRemoveGEREvents(ctx, &ger1, nil, nil)
		require.NoError(t, err)
		require.Len(t, gerEvents, 1)
		require.Equal(t, ger1, gerEvents[0].GlobalExitRoot)
		require.Equal(t, uint64(101), gerEvents[0].BlockNum)

		// Test no results for non-existent GER
		nonExistentGER := common.HexToHash("0xnonexistent")
		noEvents, err := processor.GetRemoveGEREvents(ctx, &nonExistentGER, nil, nil)
		require.NoError(t, err)
		require.Len(t, noEvents, 0)
	})
}
