package lastgersync

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
	testDir := path.Join(t.TempDir(), "lastgersync_Test_getLatestL1InfoTreeIndex.sqlite")
	processor, err := newProcessor(testDir)
	require.NoError(t, err)

	block := sync.Block{
		Num:  1,
		Hash: common.Hash{},
		Events: []interface{}{
			&Event{
				GERInfo: &GlobalExitRootInfo{
					GlobalExitRoot:  common.HexToHash("0x1"),
					L1InfoTreeIndex: 2,
				}},
		},
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
							GERInfo: &GlobalExitRootInfo{
								GlobalExitRoot:  common.HexToHash("0x1234"),
								L1InfoTreeIndex: l1InfoTreeIndex,
							},
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
							GERInfo: &GlobalExitRootInfo{
								GlobalExitRoot:  common.HexToHash("0xffee"),
								L1InfoTreeIndex: l1InfoTreeIndex,
							},
						},
						&Event{
							GEREvent: &GEREvent{
								GlobalExitRoot: common.HexToHash("0xffee"),
								IsRemove:       true,
							},
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
							GERInfo: &GlobalExitRootInfo{
								GlobalExitRoot:  common.HexToHash("0x1234"),
								L1InfoTreeIndex: l1InfoTreeIndex,
							},
						},
					},
				},
				{
					Num: 4,
					Events: []any{
						&Event{
							GERInfo: &GlobalExitRootInfo{
								GlobalExitRoot:  common.HexToHash("0x5678"),
								L1InfoTreeIndex: l1InfoTreeIndex + 1,
							},
						},
					},
				},
				{
					Num: 5,
					Events: []any{
						&Event{
							GERInfo: &GlobalExitRootInfo{
								GlobalExitRoot:  common.HexToHash("0x9876"),
								L1InfoTreeIndex: l1InfoTreeIndex + 2,
							},
						},
					},
				},
				{
					Num: 6,
					Events: []any{
						&Event{
							GEREvent: &GEREvent{
								GlobalExitRoot: common.HexToHash("0x9876"),
								IsRemove:       true,
							},
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

			testDir := path.Join(t.TempDir(), fmt.Sprintf("lastgersync_Test_ProcessBlock_%s.sqlite", tt.name))
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
	testDir := path.Join(t.TempDir(), "lastgersync_TestReorg.sqlite")
	processor, err := newProcessor(testDir)
	require.NoError(t, err)

	block1 := sync.Block{
		Num:  1,
		Hash: common.Hash{},
		Events: []interface{}{
			&Event{
				GERInfo: &GlobalExitRootInfo{
					GlobalExitRoot:  common.HexToHash("0x1"),
					L1InfoTreeIndex: 2,
				}},
		},
	}
	block2 := sync.Block{
		Num:  2,
		Hash: common.Hash{},
		Events: []interface{}{
			&Event{
				GERInfo: &GlobalExitRootInfo{
					GlobalExitRoot:  common.HexToHash("0x2"),
					L1InfoTreeIndex: 3,
				}},
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

func TestGetInjectedGERsForRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	makeGERs := func() []*GlobalExitRootInfo {
		return []*GlobalExitRootInfo{
			{GlobalExitRoot: common.HexToHash("0x1234")},
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
			{Num: 3, Events: []any{&Event{GERInfo: gerList[0]}}},
			{Num: 4, Events: []any{&Event{GERInfo: gerList[1]}}},
			{Num: 5, Events: []any{&Event{GERInfo: gerList[2]}}},
			{Num: 6, Events: []any{&Event{GEREvent: &GEREvent{
				GlobalExitRoot: gerList[2].GlobalExitRoot,
				IsRemove:       true,
			}}}},
		}

		processor := setupProcessorWithGERs(t, allBlocks)
		injectedGERsMap, err := processor.GetInjectedGERsForRange(ctx, 10, 1)
		require.ErrorContains(t, err, "invalid block range: fromBlock(10) > toBlock(1)")
		require.Empty(t, injectedGERsMap)
	})

	t.Run("returns only non-removed GERs", func(t *testing.T) {
		t.Parallel()

		gerList := makeGERs()
		allBlocks := []sync.Block{
			{Num: 3, Events: []any{&Event{GERInfo: gerList[0]}}},
			{Num: 4, Events: []any{&Event{GERInfo: gerList[1]}}},
			{Num: 5, Events: []any{&Event{GERInfo: gerList[2]}}},
			{Num: 6, Events: []any{&Event{GEREvent: &GEREvent{
				GlobalExitRoot: gerList[2].GlobalExitRoot,
				IsRemove:       true,
			}}}},
		}

		processor := setupProcessorWithGERs(t, allBlocks)
		injectedGERsMap, err := processor.GetInjectedGERsForRange(ctx, 3, 5)
		require.NoError(t, err)

		expectedGERs := gerList[:2] // The 3rd was removed
		require.Len(t, injectedGERsMap, len(expectedGERs))

		for _, expected := range expectedGERs {
			actual, ok := injectedGERsMap[expected.GlobalExitRoot]
			require.True(t, ok, "GER %s not found", expected.GlobalExitRoot.Hex())
			require.Equal(t, expected.GlobalExitRoot, actual.GlobalExitRoot)
		}
	})

	t.Run("includes removed GER if block range excludes removal", func(t *testing.T) {
		t.Parallel()

		gerList := makeGERs()
		blocksExcludingRemoval := []sync.Block{
			{Num: 3, Events: []any{&Event{GERInfo: gerList[0]}}},
			{Num: 4, Events: []any{&Event{GERInfo: gerList[1]}}},
			{Num: 5, Events: []any{&Event{GERInfo: gerList[2]}}},
		}

		processor := setupProcessorWithGERs(t, blocksExcludingRemoval)
		injectedGERsMap, err := processor.GetInjectedGERsForRange(ctx, 3, 5)
		require.NoError(t, err)

		require.Len(t, injectedGERsMap, 3)
		for _, expected := range gerList {
			_, ok := injectedGERsMap[expected.GlobalExitRoot]
			require.True(t, ok, "GER %s not found", expected.GlobalExitRoot.Hex())
		}
	})
}
