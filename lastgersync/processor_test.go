package lastgersync

import (
	"context"
	"fmt"
	"path"
	"testing"

	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGetLastProcessedBlock(t *testing.T) {
	testDir := path.Join(t.TempDir(), "lastgersync_TestGetLastProcessedBlock.sqlite")
	fmt.Println(testDir)
	processor, err := newProcessor(testDir, reorgDetectorID)
	require.NoError(t, err)

	block := sync.Block{
		Num:  1,
		Hash: common.Hash{},
		Events: []interface{}{
			Event{
				GlobalExitRoot:  common.HexToHash("0x1"),
				L1InfoTreeIndex: 2,
			},
		},
	}
	processor.ProcessBlock(context.TODO(), block)

	blockNum, err := processor.GetLastProcessedBlock(context.TODO())
	require.NoError(t, err)
	require.Equal(t, uint64(1), blockNum)
}

func Test_getLastIndex(t *testing.T) {
	testDir := path.Join(t.TempDir(), "lastgersync_Test_getLastIndex.sqlite")
	fmt.Println(testDir)
	processor, err := newProcessor(testDir, reorgDetectorID)
	require.NoError(t, err)

	block := sync.Block{
		Num:  1,
		Hash: common.Hash{},
		Events: []interface{}{
			Event{
				GlobalExitRoot:  common.HexToHash("0x1"),
				L1InfoTreeIndex: 2,
			},
		},
	}
	processor.ProcessBlock(context.TODO(), block)

	index, err := processor.getLastIndex()
	require.NoError(t, err)
	require.Equal(t, uint32(2), index)
}

func TestReorg(t *testing.T) {
	testDir := path.Join(t.TempDir(), "lastgersync_TestReorg.sqlite")
	fmt.Println(testDir)
	processor, err := newProcessor(testDir, reorgDetectorID)
	require.NoError(t, err)

	block1 := sync.Block{
		Num:  1,
		Hash: common.Hash{},
		Events: []interface{}{
			Event{
				GlobalExitRoot:  common.HexToHash("0x1"),
				L1InfoTreeIndex: 2,
			},
		},
	}
	block2 := sync.Block{
		Num:  2,
		Hash: common.Hash{},
		Events: []interface{}{
			Event{
				GlobalExitRoot:  common.HexToHash("0x2"),
				L1InfoTreeIndex: 3,
			},
		},
	}
	processor.ProcessBlock(context.TODO(), block1)
	processor.ProcessBlock(context.TODO(), block2)

	err = processor.Reorg(context.TODO(), 2)
	require.NoError(t, err)

	blockNum, err := processor.GetLastProcessedBlock(context.TODO())
	require.NoError(t, err)
	require.Equal(t, uint64(1), blockNum)

	index, err := processor.getLastIndex()
	require.NoError(t, err)
	require.Equal(t, uint32(2), index)
}

func TestGetFirstGERAfterL1InfoTreeIndex(t *testing.T) {
	testDir := path.Join(t.TempDir(), "lastgersync_TestGetFirstGERAfterL1InfoTreeIndex.sqlite")
	fmt.Println(testDir)
	processor, err := newProcessor(testDir, reorgDetectorID)
	require.NoError(t, err)

	block := sync.Block{
		Num:  1,
		Hash: common.Hash{},
		Events: []interface{}{
			Event{
				GlobalExitRoot:  common.HexToHash("0x1"),
				L1InfoTreeIndex: 2,
			},
		},
	}
	processor.ProcessBlock(context.TODO(), block)

	ger, err := processor.GetFirstGERAfterL1InfoTreeIndex(context.TODO(), 1)
	require.NoError(t, err)
	require.Equal(t, common.HexToHash("0x1"), ger.GlobalExitRoot)
	require.Equal(t, uint32(2), ger.L1InfoTreeIndex)
}
