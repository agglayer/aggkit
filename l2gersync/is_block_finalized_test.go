package l2gersync

import (
	"context"
	"errors"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
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

	t.Run("hash differs from the stored one: unknown", func(t *testing.T) {
		s, _, _ := newBlockFinalityTestSyncer(t)

		_, found, err := s.IsBlockFinalized(ctx, 10, common.HexToHash("0x99"))
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("zero hash, block without hash or not stored: unknown", func(t *testing.T) {
		s, _, hash := newBlockFinalityTestSyncer(t)

		for _, tc := range []struct {
			blockNum uint64
			hash     common.Hash
		}{{10, common.Hash{}}, {11, hash}, {99, hash}} {
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

	info, err := s.processor.GetFirstGERAfterL1InfoTreeIndex(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, uint64(10), info.BlockNum)
	require.Equal(t, hash, info.BlockHash)
}
