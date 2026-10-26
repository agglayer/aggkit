package bridgesync

import (
	"context"
	"errors"
	"math/big"
	"path"
	"reflect"
	"testing"

	"github.com/agglayer/aggkit/bridgesync/migrations"
	mocksbridgesync "github.com/agglayer/aggkit/bridgesync/mocks"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newBlockFinalityTestSyncer returns a syncer with blocks 1 (hash 0x1), 2 (hash 0x2) and 3 (no hash)
// stored, and one bridge in each of them
func newBlockFinalityTestSyncer(t *testing.T) (*BridgeSync, *mocksbridgesync.ReorgDetector) {
	t.Helper()

	dbPath := path.Join(t.TempDir(), "bridgesyncIsBlockFinalized.sqlite")
	require.NoError(t, migrations.RunMigrations(dbPath))
	p, err := newTestProcessor(dbPath, "bridge-syncer", log.WithFields("bridge-syncer", "foo"), dbQueryTimeout)
	require.NoError(t, err)

	tx, err := p.db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES (1, $1), (2, $2)`,
		common.HexToHash("0x1").String(), common.HexToHash("0x2").String())
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO block (num) VALUES (3)`)
	require.NoError(t, err)
	for i := uint32(0); i < 3; i++ {
		require.NoError(t, meddler.Insert(tx, "bridge",
			&Bridge{DepositCount: i, BlockNum: uint64(i) + 1, Amount: big.NewInt(1)}))
	}
	require.NoError(t, tx.Commit())

	rd := mocksbridgesync.NewReorgDetector(t)
	return &BridgeSync{processor: p, reorgDetector: rd, reorgDetectorID: "bridge-syncer-id"}, rd
}

func TestBridgeSyncIsBlockFinalized(t *testing.T) {
	ctx := context.Background()
	hash1, hash2 := common.HexToHash("0x1"), common.HexToHash("0x2")

	t.Run("stored and not tracked anymore: finalized", func(t *testing.T) {
		s, rd := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber("bridge-syncer-id", uint64(1)).Return(nil, db.ErrNotFound)

		finalized, found, err := s.IsBlockFinalized(ctx, 1, hash1)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, finalized)
	})

	t.Run("still tracked by the reorg detector: not finalized", func(t *testing.T) {
		s, rd := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber("bridge-syncer-id", uint64(2)).
			Return(&reorgdetector.Header{Num: 2, Hash: hash2}, nil)

		finalized, found, err := s.IsBlockFinalized(ctx, 2, hash2)
		require.NoError(t, err)
		require.True(t, found)
		require.False(t, finalized)
	})

	t.Run("not tracked and hash differs from the stored one: unknown", func(t *testing.T) {
		s, rd := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber("bridge-syncer-id", uint64(1)).Return(nil, db.ErrNotFound)

		// the caller read the block, then it was replaced: nothing can be said about the old one
		finalized, found, err := s.IsBlockFinalized(ctx, 1, hash2)
		require.NoError(t, err)
		require.False(t, found)
		require.False(t, finalized)
	})

	t.Run("zero hash: unknown without asking the reorg detector", func(t *testing.T) {
		s, _ := newBlockFinalityTestSyncer(t)

		_, found, err := s.IsBlockFinalized(ctx, 1, common.Hash{})
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("not tracked, block without stored hash: unknown", func(t *testing.T) {
		s, rd := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber("bridge-syncer-id", uint64(3)).Return(nil, db.ErrNotFound)

		_, found, err := s.IsBlockFinalized(ctx, 3, hash1)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("not tracked, block not stored: unknown", func(t *testing.T) {
		s, rd := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber("bridge-syncer-id", uint64(99)).Return(nil, db.ErrNotFound)

		_, found, err := s.IsBlockFinalized(ctx, 99, hash1)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("unknown subscriber: error, never finalized", func(t *testing.T) {
		s, rd := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber("bridge-syncer-id", uint64(1)).
			Return(nil, reorgdetector.ErrSubscriberNotFound)

		finalized, found, err := s.IsBlockFinalized(ctx, 1, hash1)
		require.ErrorIs(t, err, reorgdetector.ErrSubscriberNotFound)
		require.False(t, found)
		require.False(t, finalized)
	})

	t.Run("reorg detector error", func(t *testing.T) {
		s, rd := newBlockFinalityTestSyncer(t)
		rd.EXPECT().GetTrackedBlockByBlockNumber(mock.Anything, uint64(1)).Return(nil, errors.New("boom"))

		_, found, err := s.IsBlockFinalized(ctx, 1, hash1)
		require.ErrorContains(t, err, "boom")
		require.False(t, found)
	})

	t.Run("halted processor", func(t *testing.T) {
		s, _ := newBlockFinalityTestSyncer(t)
		s.processor.halted = true

		_, _, err := s.IsBlockFinalized(ctx, 1, hash1)
		require.ErrorIs(t, err, sync.ErrInconsistentState)
	})
}

func TestBridgeQueriesFillBlockHash(t *testing.T) {
	ctx := context.Background()
	hash1, hash2 := common.HexToHash("0x1"), common.HexToHash("0x2")
	s, _ := newBlockFinalityTestSyncer(t)

	// bridges with metadata (block 2) and archived bridges: one in block 2, one in block 3 (no hash) and
	// one in a block that is not stored anymore
	metadata := []byte{0xca, 0xfe}
	tx, err := s.processor.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, meddler.Insert(tx, bridgeTableName,
		&Bridge{DepositCount: 3, BlockNum: 2, BlockPos: 1, Amount: big.NewInt(1), Metadata: metadata}))
	for _, archived := range []*Bridge{
		{DepositCount: 10, BlockNum: 2, BlockPos: 2, Amount: big.NewInt(1), Metadata: metadata},
		{DepositCount: 11, BlockNum: 3, BlockPos: 1, Amount: big.NewInt(1)},
		{DepositCount: 12, BlockNum: 99, Amount: big.NewInt(1)},
	} {
		require.NoError(t, meddler.Insert(tx, bridgeArchiveTableName, archived))
	}
	require.NoError(t, tx.Commit())

	hashByDepositCount := func(bridges []*Bridge) map[uint32]common.Hash {
		hashes := make(map[uint32]common.Hash, len(bridges))
		for _, b := range bridges {
			hashes[b.DepositCount] = b.BlockHash
		}
		return hashes
	}

	t.Run("GetBridgesPaged", func(t *testing.T) {
		bridges, count, err := s.GetBridgesPaged(ctx, 1, 10, nil, nil, "")
		require.NoError(t, err)
		require.Equal(t, 4, count)
		// block 3 has no stored hash
		require.Equal(t, map[uint32]common.Hash{0: hash1, 1: hash2, 2: {}, 3: hash2}, hashByDepositCount(bridges))
		// the join keeps the order and the row data
		require.Equal(t, []uint32{3, 2, 1, 0},
			[]uint32{bridges[0].DepositCount, bridges[1].DepositCount, bridges[2].DepositCount, bridges[3].DepositCount})
		require.Equal(t, metadata, bridges[0].Metadata)
		require.Equal(t, uint64(1), bridges[0].BlockPos)

		// filtered and paged
		depositCount := uint64(1)
		bridges, count, err = s.GetBridgesPaged(ctx, 1, 10, &depositCount, []uint32{0}, "")
		require.NoError(t, err)
		require.Equal(t, 1, count)
		require.Equal(t, map[uint32]common.Hash{1: hash2}, hashByDepositCount(bridges))
		bridges, _, err = s.GetBridgesPaged(ctx, 2, 3, nil, nil, "")
		require.NoError(t, err)
		require.Equal(t, map[uint32]common.Hash{0: hash1}, hashByDepositCount(bridges))
	})

	t.Run("GetBridgeByDepositCount", func(t *testing.T) {
		for depositCount, expected := range map[uint32]common.Hash{
			1:  hash2,
			2:  {}, // block without hash
			10: hash2,
			11: {}, // archived, block without hash
			12: {}, // archived, block not stored
		} {
			bridge, err := s.GetBridgeByDepositCount(ctx, depositCount)
			require.NoError(t, err)
			require.Equal(t, depositCount, bridge.DepositCount)
			require.Equal(t, expected, bridge.BlockHash, "deposit count %d", depositCount)
		}

		archived, err := s.GetBridgeByDepositCount(ctx, 10)
		require.NoError(t, err)
		require.Equal(t, metadata, archived.Metadata)
		require.Equal(t, uint64(2), archived.BlockNum)

		_, err = s.GetBridgeByDepositCount(ctx, 1000)
		require.ErrorIs(t, err, db.ErrNotFound)
	})

	t.Run("GetBridgesByContent without metadata", func(t *testing.T) {
		bridges, err := s.GetBridgesByContent(ctx, 0, common.Address{}, 0, common.Address{}, big.NewInt(1), nil)
		require.NoError(t, err)
		require.Equal(t, map[uint32]common.Hash{0: hash1, 1: hash2, 2: {}, 11: {}, 12: {}},
			hashByDepositCount(bridges))
	})

	t.Run("GetBridgesByContent with metadata", func(t *testing.T) {
		bridges, err := s.GetBridgesByContent(ctx, 0, common.Address{}, 0, common.Address{}, big.NewInt(1), metadata)
		require.NoError(t, err)
		require.Equal(t, map[uint32]common.Hash{3: hash2, 10: hash2}, hashByDepositCount(bridges))
	})

	t.Run("GetRootByLER", func(t *testing.T) {
		roots := []*tree.Root{
			{Hash: common.HexToHash("0xa"), Index: 0, BlockNum: 1},
			{Hash: common.HexToHash("0xb"), Index: 1, BlockNum: 3, BlockPosition: 4},
			{Hash: common.HexToHash("0xc"), Index: 2, BlockNum: 99},
		}
		for _, root := range roots {
			require.NoError(t, meddler.Insert(s.processor.db, exitTreeDBPrefix+"root", root))
		}
		for i, expected := range []common.Hash{hash1, {}, {}} {
			root, err := s.GetRootByLER(ctx, roots[i].Hash)
			require.NoError(t, err)
			want := *roots[i]
			want.BlockHash = expected
			require.Equal(t, want, *root)

			// same root as the exit tree's own query, which doesn't read the block hash
			treeRoot, err := s.processor.exitTree.GetRootByHash(ctx, roots[i].Hash)
			require.NoError(t, err)
			treeRoot.BlockHash = expected
			require.Equal(t, treeRoot, root)
		}

		_, err := s.GetRootByLER(ctx, common.HexToHash("0xdead"))
		require.ErrorIs(t, err, db.ErrNotFound)
	})
}

// TestBlockHashReadModels checks that the read models used to read rows together with their block hash
// map the same columns as the public types they are converted to, besides block_hash. Field names and
// types are already checked by the compiler, through the conversions.
func TestBlockHashReadModels(t *testing.T) {
	requireSameMeddlerColumns(t, Bridge{}, bridgeWithBlockHash{})
	requireSameMeddlerColumns(t, tree.Root{}, rootWithBlockHash{})
}

// requireSameMeddlerColumns checks that public and readModel have the same meddler tags, except that
// public.BlockHash is not a column and readModel.BlockHash is the block_hash column
func requireSameMeddlerColumns(t *testing.T, public, readModel any) {
	t.Helper()

	publicType, readModelType := reflect.TypeOf(public), reflect.TypeOf(readModel)
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
