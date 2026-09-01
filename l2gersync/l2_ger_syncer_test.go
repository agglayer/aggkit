package l2gersync

import (
	"context"
	"errors"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetLastProcessedBlock(t *testing.T) {
	testDir := path.Join(t.TempDir(), "l2gersync_TestGetLastProcessedBlock.sqlite")
	processor, err := newProcessor(testDir)
	require.NoError(t, err)

	block := sync.Block{
		Num:    1,
		Hash:   common.Hash{},
		Events: []any{newEvent(newGlobalExitRootInfo(common.HexToHash("0x1"), 2, 1, 0), GEREventTypeInsert)},
	}
	err = processor.ProcessBlock(context.TODO(), block)
	require.NoError(t, err)

	l2GERSync := &L2GERSync{
		processor: processor,
	}
	blockNum, err := l2GERSync.GetLastProcessedBlock(context.TODO())
	require.NoError(t, err)
	require.Equal(t, uint64(1), blockNum)
}

func TestGetFirstGERAfterL1InfoTreeIndex(t *testing.T) {
	testDir := path.Join(t.TempDir(), "l2gersync_TestGetFirstGERAfterL1InfoTreeIndex.sqlite")
	processor, err := newProcessor(testDir)
	require.NoError(t, err)

	ctx := context.TODO()
	block := sync.Block{
		Num:    1,
		Hash:   common.Hash{},
		Events: []any{newEvent(newGlobalExitRootInfo(common.HexToHash("0x1"), 2, 1, 0), GEREventTypeInsert)},
	}
	err = processor.ProcessBlock(context.TODO(), block)
	require.NoError(t, err)
	l2GERSync := &L2GERSync{
		processor: processor,
	}

	t.Run("GER found", func(t *testing.T) {
		ger, err := l2GERSync.GetFirstGERAfterL1InfoTreeIndex(ctx, 1)
		require.NoError(t, err, "expected GER to be found")
		require.Equal(t, common.HexToHash("0x1"), ger.GlobalExitRoot, "unexpected GlobalExitRoot")
		require.Equal(t, uint32(2), ger.L1InfoTreeIndex, "unexpected L1InfoTreeIndex")
	})

	t.Run("GER not found", func(t *testing.T) {
		ger, err := l2GERSync.GetFirstGERAfterL1InfoTreeIndex(ctx, 3)
		require.ErrorIs(t, err, db.ErrNotFound, "expected ErrNotFound")
		require.Equal(t, common.HexToHash("0x0"), ger.GlobalExitRoot, "unexpected GlobalExitRoot when not found")
		require.Equal(t, uint32(0), ger.L1InfoTreeIndex, "unexpected L1InfoTreeIndex when not found")
	})
}

func TestGetFirstGERAfterL1InfoTreeIndex_BackfillsTimestamp(t *testing.T) {
	ctx := context.Background()

	newSyncerWithGER := func(t *testing.T) *L2GERSync {
		t.Helper()
		testDir := path.Join(t.TempDir(), "l2gersync_TestGetFirstGERAfterL1InfoTreeIndex_BackfillsTimestamp.sqlite")
		processor, err := newProcessor(testDir)
		require.NoError(t, err)

		// no timestamp persisted at insert time, matching a row written before the column existed
		err = processor.ProcessBlock(ctx, sync.Block{
			Num:    7,
			Events: []any{newEvent(newGlobalExitRootInfo(common.HexToHash("0x1"), 1, 7, 0), GEREventTypeInsert)},
		})
		require.NoError(t, err)

		return &L2GERSync{processor: processor}
	}

	t.Run("resolves timestamp from the RPC and persists it", func(t *testing.T) {
		l2GERSync := newSyncerWithGER(t)
		const backfilledTimestamp = uint64(1700000000)
		mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
		mockL2Client.EXPECT().
			CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(uint64(7))).
			Return(&aggkittypes.BlockHeader{Number: 7, Time: backfilledTimestamp}, nil).Once()
		l2GERSync.l2Client = mockL2Client

		ger, err := l2GERSync.GetFirstGERAfterL1InfoTreeIndex(ctx, 1)
		require.NoError(t, err)
		require.NotNil(t, ger.Timestamp)
		require.Equal(t, backfilledTimestamp, *ger.Timestamp)

		// persisted, so a later read does not need the RPC again
		mockL2Client.AssertExpectations(t)
		ger, err = l2GERSync.GetFirstGERAfterL1InfoTreeIndex(ctx, 1)
		require.NoError(t, err)
		require.NotNil(t, ger.Timestamp)
		require.Equal(t, backfilledTimestamp, *ger.Timestamp)
	})

	t.Run("RPC failure is best-effort: other fields still returned, timestamp stays nil", func(t *testing.T) {
		l2GERSync := newSyncerWithGER(t)
		mockL2Client := aggkittypesmocks.NewBaseEthereumClienter(t)
		mockL2Client.EXPECT().
			CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(uint64(7))).
			Return(nil, errors.New("rpc unavailable")).Once()
		l2GERSync.l2Client = mockL2Client

		ger, err := l2GERSync.GetFirstGERAfterL1InfoTreeIndex(ctx, 1)
		require.NoError(t, err)
		require.Nil(t, ger.Timestamp)
		require.Equal(t, common.HexToHash("0x1"), ger.GlobalExitRoot)
	})

	t.Run("no l2Client configured (e.g. L2GERSync built directly, not via New): skips backfill", func(t *testing.T) {
		l2GERSync := newSyncerWithGER(t)

		ger, err := l2GERSync.GetFirstGERAfterL1InfoTreeIndex(ctx, 1)
		require.NoError(t, err)
		require.Nil(t, ger.Timestamp)
	})
}
