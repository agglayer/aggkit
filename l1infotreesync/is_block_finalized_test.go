package l1infotreesync

import (
	"encoding/json"
	"errors"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestIsBlockFinalizedMultidownloader(t *testing.T) {
	ctx := t.Context()
	hash := common.HexToHash("0x10")
	header := &aggkittypes.BlockHeader{Number: 10, Hash: hash}

	newSyncer := func(t *testing.T) (*L1InfoTreeSync, *BlockFinalityQuerierMock) {
		t.Helper()
		querier := NewBlockFinalityQuerierMock(t)
		return &L1InfoTreeSync{blockFinality: querier}, querier
	}

	t.Run("read only syncer: unknown", func(t *testing.T) {
		syncer := &L1InfoTreeSync{}
		finalized, found, err := syncer.IsBlockFinalized(ctx, 10, hash)
		require.NoError(t, err)
		require.False(t, finalized)
		require.False(t, found)
	})

	t.Run("stored as finalized", func(t *testing.T) {
		syncer, querier := newSyncer(t)
		querier.EXPECT().StorageHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).Return(header, true, nil)
		finalized, found, err := syncer.IsBlockFinalized(ctx, 10, hash)
		require.NoError(t, err)
		require.True(t, finalized)
		require.True(t, found)
	})

	t.Run("no hash given: unknown, the stored block may have replaced the one the caller read", func(t *testing.T) {
		// the querier must not even be asked: no expectation set
		syncer, _ := newSyncer(t)
		finalized, found, err := syncer.IsBlockFinalized(ctx, 10, common.Hash{})
		require.NoError(t, err)
		require.False(t, finalized)
		require.False(t, found)
	})

	t.Run("stored but not finalized", func(t *testing.T) {
		syncer, querier := newSyncer(t)
		querier.EXPECT().StorageHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).Return(header, false, nil)
		finalized, found, err := syncer.IsBlockFinalized(ctx, 10, hash)
		require.NoError(t, err)
		require.False(t, finalized)
		require.True(t, found)
	})

	t.Run("stored with another hash: unknown", func(t *testing.T) {
		syncer, querier := newSyncer(t)
		querier.EXPECT().StorageHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).Return(header, true, nil)
		_, found, err := syncer.IsBlockFinalized(ctx, 10, common.HexToHash("0x99"))
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("block not stored: unknown", func(t *testing.T) {
		syncer, querier := newSyncer(t)
		querier.EXPECT().StorageHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).Return(nil, false, nil)
		_, found, err := syncer.IsBlockFinalized(ctx, 10, hash)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("storage error", func(t *testing.T) {
		syncer, querier := newSyncer(t)
		querier.EXPECT().StorageHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).
			Return(nil, false, errors.New("boom"))
		_, found, err := syncer.IsBlockFinalized(ctx, 10, hash)
		require.ErrorContains(t, err, "boom")
		require.False(t, found)
	})
}

// newLegacyTestSyncer returns a legacy syncer with block 10 (hash 0x10) and block 11 (no hash) stored
func newLegacyTestSyncer(t *testing.T) (*L1InfoTreeSync, *sync.ReorgDetectorMock, common.Hash) {
	t.Helper()

	processor, err := newProcessor(path.Join(t.TempDir(), "l1infotreesyncIsBlockFinalized.sqlite"))
	require.NoError(t, err)
	hash := common.HexToHash("0x10")
	_, err = processor.db.Exec(`INSERT INTO block (num, hash) VALUES (10, $1)`, hash.String())
	require.NoError(t, err)
	_, err = processor.db.Exec(`INSERT INTO block (num) VALUES (11)`)
	require.NoError(t, err)

	rd := sync.NewReorgDetectorMock(t)
	return &L1InfoTreeSync{processor: processor, reorgDetector: rd}, rd, hash
}

func TestIsBlockFinalizedLegacy(t *testing.T) {
	ctx := t.Context()

	t.Run("stored and not tracked anymore: finalized", func(t *testing.T) {
		syncer, rd, hash := newLegacyTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(syncerID, uint64(10)).Return(nil, db.ErrNotFound)

		finalized, found, err := syncer.IsBlockFinalized(ctx, 10, hash)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, finalized)
	})

	t.Run("still tracked by the reorg detector: not finalized", func(t *testing.T) {
		syncer, rd, hash := newLegacyTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(syncerID, uint64(10)).
			Return(&reorgdetector.Header{Num: 10, Hash: hash}, nil)

		finalized, found, err := syncer.IsBlockFinalized(ctx, 10, hash)
		require.NoError(t, err)
		require.True(t, found)
		require.False(t, finalized)
	})

	t.Run("hash differs from the stored one: unknown", func(t *testing.T) {
		syncer, _, _ := newLegacyTestSyncer(t)

		_, found, err := syncer.IsBlockFinalized(ctx, 10, common.HexToHash("0x99"))
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("zero hash, block without hash or not stored: unknown", func(t *testing.T) {
		syncer, _, hash := newLegacyTestSyncer(t)

		for _, tc := range []struct {
			blockNum uint64
			hash     common.Hash
		}{{10, common.Hash{}}, {11, hash}, {99, hash}} {
			_, found, err := syncer.IsBlockFinalized(ctx, tc.blockNum, tc.hash)
			require.NoError(t, err)
			require.False(t, found)
		}
	})

	t.Run("reorg detector error", func(t *testing.T) {
		syncer, rd, hash := newLegacyTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(syncerID, uint64(10)).Return(nil, errors.New("boom"))

		_, found, err := syncer.IsBlockFinalized(ctx, 10, hash)
		require.ErrorContains(t, err, "boom")
		require.False(t, found)
	})

	t.Run("halted processor", func(t *testing.T) {
		syncer, _, hash := newLegacyTestSyncer(t)
		syncer.processor.halted = true

		_, _, err := syncer.IsBlockFinalized(ctx, 10, hash)
		require.ErrorIs(t, err, sync.ErrInconsistentState)
	})
}

func TestGetInfoByIndexFillsBlockHash(t *testing.T) {
	processor, err := newProcessor(path.Join(t.TempDir(), "l1infotreesyncGetInfoByIndex.sqlite"))
	require.NoError(t, err)
	hash := common.HexToHash("0xb10c")
	require.NoError(t, processor.ProcessBlock(t.Context(), sync.Block{
		Num:  1,
		Hash: hash,
		Events: []any{Event{UpdateL1InfoTree: &UpdateL1InfoTree{
			MainnetExitRoot: common.HexToHash("beef"),
			RollupExitRoot:  common.HexToHash("5ca1e"),
			ParentHash:      common.HexToHash("1010101"),
			Timestamp:       420,
		}}},
	}))
	syncer := &L1InfoTreeSync{processor: processor}

	leaf, err := syncer.GetInfoByIndex(t.Context(), 0)
	require.NoError(t, err)
	require.Equal(t, uint64(1), leaf.BlockNumber)
	require.Equal(t, hash, leaf.BlockHash)

	// the hash is not part of the leaf's JSON
	raw, err := json.Marshal(leaf)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "BlockHash")
	require.NotContains(t, string(raw), hash.Hex())
}
