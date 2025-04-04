package lastgersync

import (
	"context"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGetLastProcessedBlock(t *testing.T) {
	testDir := path.Join(t.TempDir(), "lastgersync_TestGetLastProcessedBlock.sqlite")
	processor, err := newProcessor(testDir)
	require.NoError(t, err)

	block := sync.Block{
		Num:  1,
		Hash: common.Hash{},
		Events: []interface{}{
			GlobalExitRootInfo{
				GlobalExitRoot:  common.HexToHash("0x1"),
				L1InfoTreeIndex: 2,
			},
		},
	}
	err = processor.ProcessBlock(context.TODO(), block)
	require.NoError(t, err)

	lastgersync := &LastGERSync{
		processor: processor,
	}
	blockNum, err := lastgersync.GetLastProcessedBlock(context.TODO())
	require.NoError(t, err)
	require.Equal(t, uint64(1), blockNum)
}

func TestGetFirstGERAfterL1InfoTreeIndex(t *testing.T) {
	testDir := path.Join(t.TempDir(), "lastgersync_TestGetFirstGERAfterL1InfoTreeIndex.sqlite")
	processor, err := newProcessor(testDir)
	require.NoError(t, err)

	ctx := context.TODO()
	block := sync.Block{
		Num:  1,
		Hash: common.Hash{},
		Events: []interface{}{
			GlobalExitRootInfo{
				GlobalExitRoot:  common.HexToHash("0x1"),
				L1InfoTreeIndex: 2,
			},
		},
	}
	err = processor.ProcessBlock(context.TODO(), block)
	require.NoError(t, err)
	lastgersync := &LastGERSync{
		processor: processor,
	}

	t.Run("GER found", func(t *testing.T) {
		ger, err := lastgersync.GetFirstGERAfterL1InfoTreeIndex(ctx, 1)
		require.NoError(t, err, "expected GER to be found")
		require.Equal(t, common.HexToHash("0x1"), ger.GlobalExitRoot, "unexpected GlobalExitRoot")
		require.Equal(t, uint32(2), ger.L1InfoTreeIndex, "unexpected L1InfoTreeIndex")
	})

	t.Run("GER not found", func(t *testing.T) {
		ger, err := lastgersync.GetFirstGERAfterL1InfoTreeIndex(ctx, 3)
		require.ErrorIs(t, err, db.ErrNotFound, "expected ErrNotFound")
		require.Equal(t, common.HexToHash("0x0"), ger.GlobalExitRoot, "unexpected GlobalExitRoot when not found")
		require.Equal(t, uint32(0), ger.L1InfoTreeIndex, "unexpected L1InfoTreeIndex when not found")
	})
}
