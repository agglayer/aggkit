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
