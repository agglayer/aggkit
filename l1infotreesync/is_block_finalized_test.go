package l1infotreesync

import (
	"errors"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestIsBlockFinalized(t *testing.T) {
	ctx := t.Context()
	header := &aggkittypes.BlockHeader{Number: 10}

	t.Run("not multidownloader based: unknown", func(t *testing.T) {
		syncer := &L1InfoTreeSync{}
		finalized, found, err := syncer.IsBlockFinalized(ctx, 10)
		require.NoError(t, err)
		require.False(t, finalized)
		require.False(t, found)
	})

	t.Run("stored as finalized", func(t *testing.T) {
		querier := NewBlockFinalityQuerierMock(t)
		querier.EXPECT().StorageHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).Return(header, true, nil)
		syncer := &L1InfoTreeSync{blockFinality: querier}
		finalized, found, err := syncer.IsBlockFinalized(ctx, 10)
		require.NoError(t, err)
		require.True(t, finalized)
		require.True(t, found)
	})

	t.Run("stored but not finalized", func(t *testing.T) {
		querier := NewBlockFinalityQuerierMock(t)
		querier.EXPECT().StorageHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).Return(header, false, nil)
		syncer := &L1InfoTreeSync{blockFinality: querier}
		finalized, found, err := syncer.IsBlockFinalized(ctx, 10)
		require.NoError(t, err)
		require.False(t, finalized)
		require.True(t, found)
	})

	t.Run("block not stored: unknown", func(t *testing.T) {
		querier := NewBlockFinalityQuerierMock(t)
		querier.EXPECT().StorageHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).Return(nil, false, nil)
		syncer := &L1InfoTreeSync{blockFinality: querier}
		_, found, err := syncer.IsBlockFinalized(ctx, 10)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("storage error", func(t *testing.T) {
		querier := NewBlockFinalityQuerierMock(t)
		querier.EXPECT().StorageHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).
			Return(nil, false, errors.New("boom"))
		syncer := &L1InfoTreeSync{blockFinality: querier}
		_, found, err := syncer.IsBlockFinalized(ctx, 10)
		require.ErrorContains(t, err, "boom")
		require.False(t, found)
	})
}
