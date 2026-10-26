package l2gersync

import (
	"context"
	"errors"
	"path"
	"reflect"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newBlockFinalityTestSyncer returns a SovereignChain syncer with a GER injected in block 10
// (hash 0x10) and another in block 11 stored without hash
func newBlockFinalityTestSyncer(t *testing.T) (*L2GERSync, *sync.ReorgDetectorMock, common.Hash) {
	t.Helper()

	processor, err := newProcessor(path.Join(t.TempDir(), "l2gersync_IsBlockFinalized.sqlite"))
	require.NoError(t, err)

	hash := common.HexToHash("0x10")
	require.NoError(t, processor.ProcessBlock(context.Background(), sync.Block{
		Num:    10,
		Hash:   hash,
		Events: []any{newEvent(newGlobalExitRootInfo(common.HexToHash("0x1"), 2, 10, 0), GEREventTypeInsert)},
	}))
	_, err = processor.database.Exec(`INSERT INTO block (num) VALUES (11)`)
	require.NoError(t, err)

	rd := sync.NewReorgDetectorMock(t)
	return &L2GERSync{processor: processor, reorgDetector: rd, syncMode: SovereignChain}, rd, hash
}

func TestL2GERSyncIsBlockFinalized(t *testing.T) {
	ctx := context.Background()

	t.Run("stored and not tracked anymore: finalized", func(t *testing.T) {
		s, rd, hash := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(reorgDetectorID, uint64(10)).Return(nil, db.ErrNotFound)

		finalized, found, err := s.IsBlockFinalized(ctx, 10, hash)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, finalized)
	})

	t.Run("still tracked by the reorg detector: not finalized", func(t *testing.T) {
		s, rd, hash := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(reorgDetectorID, uint64(10)).
			Return(&reorgdetector.Header{Num: 10, Hash: hash}, nil)

		finalized, found, err := s.IsBlockFinalized(ctx, 10, hash)
		require.NoError(t, err)
		require.True(t, found)
		require.False(t, finalized)
	})

	t.Run("not tracked and hash differs from the stored one: unknown", func(t *testing.T) {
		s, rd, _ := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(reorgDetectorID, uint64(10)).Return(nil, db.ErrNotFound)

		_, found, err := s.IsBlockFinalized(ctx, 10, common.HexToHash("0x99"))
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("zero hash: unknown without asking the reorg detector", func(t *testing.T) {
		s, _, _ := newBlockFinalityTestSyncer(t)

		_, found, err := s.IsBlockFinalized(ctx, 10, common.Hash{})
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("not tracked, block without hash or not stored: unknown", func(t *testing.T) {
		s, rd, hash := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(reorgDetectorID, mock.Anything).Return(nil, db.ErrNotFound).Twice()

		for _, tc := range []struct {
			blockNum uint64
			hash     common.Hash
		}{{11, hash}, {99, hash}} {
			_, found, err := s.IsBlockFinalized(ctx, tc.blockNum, tc.hash)
			require.NoError(t, err)
			require.False(t, found)
		}
	})

	t.Run("Legacy mode: unknown, the stored block is the polling head", func(t *testing.T) {
		s, _, hash := newBlockFinalityTestSyncer(t)
		s.syncMode = Legacy

		_, found, err := s.IsBlockFinalized(ctx, 10, hash)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("unknown subscriber: error, never finalized", func(t *testing.T) {
		s, rd, hash := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(reorgDetectorID, uint64(10)).
			Return(nil, reorgdetector.ErrSubscriberNotFound)

		finalized, found, err := s.IsBlockFinalized(ctx, 10, hash)
		require.ErrorIs(t, err, reorgdetector.ErrSubscriberNotFound)
		require.False(t, found)
		require.False(t, finalized)
	})

	t.Run("reorg detector error", func(t *testing.T) {
		s, rd, hash := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(reorgDetectorID, uint64(10)).Return(nil, errors.New("boom"))

		_, found, err := s.IsBlockFinalized(ctx, 10, hash)
		require.ErrorContains(t, err, "boom")
		require.False(t, found)
	})
}

func TestGetFirstGERAfterL1InfoTreeIndexFillsBlockHash(t *testing.T) {
	s, _, hash := newBlockFinalityTestSyncer(t)
	ctx := context.Background()

	info, err := s.processor.GetFirstGERAfterL1InfoTreeIndex(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(10), info.BlockNum)
	require.Equal(t, uint32(2), info.L1InfoTreeIndex)
	require.Equal(t, common.HexToHash("0x1"), info.GlobalExitRoot)
	require.Equal(t, hash, info.BlockHash)

	// a GER in a block stored without hash
	_, err = s.processor.database.Exec(`INSERT INTO imported_global_exit_root_v2
		(block_num, block_pos, global_exit_root, l1_info_tree_index) VALUES (11, 0, $1, 3)`,
		common.HexToHash("0x2").Hex())
	require.NoError(t, err)
	info, err = s.processor.GetFirstGERAfterL1InfoTreeIndex(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(11), info.BlockNum)
	require.Equal(t, common.HexToHash("0x2"), info.GlobalExitRoot)
	require.Equal(t, common.Hash{}, info.BlockHash)

	_, err = s.processor.GetFirstGERAfterL1InfoTreeIndex(ctx, 4)
	require.ErrorIs(t, err, db.ErrNotFound)
}

// TestGlobalExitRootInfoWithBlockHashColumns checks that globalExitRootInfoWithBlockHash maps the same
// columns as GlobalExitRootInfo, besides block_hash. Field names and types are already checked by the
// compiler, through the conversion in GetFirstGERAfterL1InfoTreeIndex.
func TestGlobalExitRootInfoWithBlockHashColumns(t *testing.T) {
	publicType := reflect.TypeOf(GlobalExitRootInfo{})
	readModelType := reflect.TypeOf(globalExitRootInfoWithBlockHash{})
	require.Equal(t, publicType.NumField(), readModelType.NumField())
	for i := range publicType.NumField() {
		publicField, readModelField := publicType.Field(i), readModelType.Field(i)
		require.Equal(t, publicField.Name, readModelField.Name)
		if publicField.Name == "BlockHash" {
			require.Equal(t, "-", publicField.Tag.Get("meddler"))
			require.Equal(t, "block_hash,hash", readModelField.Tag.Get("meddler"))
			continue
		}
		require.Equal(t, publicField.Tag.Get("meddler"), readModelField.Tag.Get("meddler"), publicField.Name)
	}
}
