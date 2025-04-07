package lastgersync

import (
	"context"
	"path"
	"testing"

	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func Test_getLastIndex(t *testing.T) {
	testDir := path.Join(t.TempDir(), "lastgersync_Test_getLastIndex.sqlite")
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

	index, err := processor.getLastIndex()
	require.NoError(t, err)
	require.Equal(t, uint32(2), index)
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

	index, err := processor.getLastIndex()
	require.NoError(t, err)
	require.Equal(t, uint32(2), index)
}
