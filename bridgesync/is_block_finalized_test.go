package bridgesync

import (
	"context"
	"errors"
	"math/big"
	"path"
	"testing"

	"github.com/agglayer/aggkit/bridgesync/migrations"
	mocksbridgesync "github.com/agglayer/aggkit/bridgesync/mocks"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
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

	t.Run("hash differs from the stored one: unknown", func(t *testing.T) {
		s, _ := newBlockFinalityTestSyncer(t)

		// the caller read the block, then it was replaced: nothing can be said about the old one
		finalized, found, err := s.IsBlockFinalized(ctx, 1, hash2)
		require.NoError(t, err)
		require.False(t, found)
		require.False(t, finalized)
	})

	t.Run("zero hash: unknown", func(t *testing.T) {
		s, _ := newBlockFinalityTestSyncer(t)

		_, found, err := s.IsBlockFinalized(ctx, 1, common.Hash{})
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("block without stored hash: unknown", func(t *testing.T) {
		s, _ := newBlockFinalityTestSyncer(t)

		_, found, err := s.IsBlockFinalized(ctx, 3, hash1)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("block not stored: unknown", func(t *testing.T) {
		s, _ := newBlockFinalityTestSyncer(t)

		_, found, err := s.IsBlockFinalized(ctx, 99, hash1)
		require.NoError(t, err)
		require.False(t, found)
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
	s, _ := newBlockFinalityTestSyncer(t)

	bridges, _, err := s.GetBridgesPaged(ctx, 1, 10, nil, nil, "")
	require.NoError(t, err)
	require.Len(t, bridges, 3)
	hashByBlock := map[uint64]common.Hash{}
	for _, b := range bridges {
		hashByBlock[b.BlockNum] = b.BlockHash
	}
	// block 3 has no stored hash
	require.Equal(t, map[uint64]common.Hash{
		1: common.HexToHash("0x1"),
		2: common.HexToHash("0x2"),
		3: {},
	}, hashByBlock)

	bridge, err := s.GetBridgeByDepositCount(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, common.HexToHash("0x2"), bridge.BlockHash)
}
