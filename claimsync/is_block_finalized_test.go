package claimsync

import (
	"errors"
	"testing"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	claimmocks "github.com/agglayer/aggkit/claimsync/types/mocks"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestClaimSyncIsBlockFinalized(t *testing.T) {
	hash := common.HexToHash("0x1")
	otherHash := common.HexToHash("0x2")
	trackerID := claimsynctypes.L2ClaimSyncer.String()

	newSyncer := func(t *testing.T) (*ClaimSync, *claimmocks.ClaimsReader, *sync.ReorgDetectorMock) {
		t.Helper()
		reader := claimmocks.NewClaimsReader(t)
		rd := sync.NewReorgDetectorMock(t)
		return &ClaimSync{
			reader:        reader,
			reorgDetector: rd,
			syncerID:      claimsynctypes.L2ClaimSyncer,
		}, reader, rd
	}

	t.Run("stored and not tracked anymore: finalized", func(t *testing.T) {
		s, reader, rd := newSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(trackerID, uint64(10)).Return(nil, db.ErrNotFound)
		reader.EXPECT().GetBlockHash(mock.Anything, nil, uint64(10)).Return(hash, true, nil)

		finalized, found, err := s.IsBlockFinalized(t.Context(), 10, hash)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, finalized)
	})

	t.Run("still tracked by the reorg detector: not finalized", func(t *testing.T) {
		s, _, rd := newSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(trackerID, uint64(10)).
			Return(&reorgdetector.Header{Num: 10, Hash: hash}, nil)

		finalized, found, err := s.IsBlockFinalized(t.Context(), 10, hash)
		require.NoError(t, err)
		require.True(t, found)
		require.False(t, finalized)
	})

	t.Run("not tracked and hash differs from the stored one: unknown", func(t *testing.T) {
		s, reader, rd := newSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(trackerID, uint64(10)).Return(nil, db.ErrNotFound)
		reader.EXPECT().GetBlockHash(mock.Anything, nil, uint64(10)).Return(otherHash, true, nil)

		finalized, found, err := s.IsBlockFinalized(t.Context(), 10, hash)
		require.NoError(t, err)
		require.False(t, found)
		require.False(t, finalized)
	})

	t.Run("not tracked and block without stored hash: unknown", func(t *testing.T) {
		s, reader, rd := newSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(trackerID, uint64(10)).Return(nil, db.ErrNotFound)
		reader.EXPECT().GetBlockHash(mock.Anything, nil, uint64(10)).Return(common.Hash{}, false, nil)

		_, found, err := s.IsBlockFinalized(t.Context(), 10, hash)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("zero hash: unknown", func(t *testing.T) {
		s, _, _ := newSyncer(t)

		_, found, err := s.IsBlockFinalized(t.Context(), 10, common.Hash{})
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("storage error", func(t *testing.T) {
		s, reader, rd := newSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(trackerID, uint64(10)).Return(nil, db.ErrNotFound)
		reader.EXPECT().GetBlockHash(mock.Anything, nil, uint64(10)).Return(common.Hash{}, false, errors.New("boom"))

		_, found, err := s.IsBlockFinalized(t.Context(), 10, hash)
		require.ErrorContains(t, err, "boom")
		require.False(t, found)
	})

	t.Run("reorg detector error", func(t *testing.T) {
		s, _, rd := newSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(trackerID, uint64(10)).Return(nil, errors.New("boom"))

		_, found, err := s.IsBlockFinalized(t.Context(), 10, hash)
		require.ErrorContains(t, err, "boom")
		require.False(t, found)
	})

	t.Run("unknown subscriber: error, never finalized", func(t *testing.T) {
		s, _, rd := newSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(trackerID, uint64(10)).
			Return(nil, reorgdetector.ErrSubscriberNotFound)

		finalized, found, err := s.IsBlockFinalized(t.Context(), 10, hash)
		require.ErrorIs(t, err, reorgdetector.ErrSubscriberNotFound)
		require.False(t, found)
		require.False(t, finalized)
	})
}
