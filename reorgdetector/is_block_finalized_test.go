package reorgdetector

import (
	"context"
	"errors"
	"math/big"
	"path"
	"testing"
	"time"

	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/etherman"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/require"
)

func TestIsBlockFinalized(t *testing.T) {
	ctx := context.Background()
	const syncerID = "test-syncer"
	hash := common.BigToHash(big.NewInt(5))
	otherHash := common.BigToHash(big.NewInt(6))

	newDetector := func(t *testing.T) *ReorgDetector {
		t.Helper()
		clientL1 := simulated.NewBackend(nil, simulated.WithBlockGasLimit(10000000))
		rd, err := New(etherman.NewDefaultEthClient(clientL1.Client(), nil, nil), Config{
			DBPath:              path.Join(t.TempDir(), "reorgdetectorIsBlockFinalized.sqlite"),
			CheckReorgsInterval: cfgtypes.NewDuration(time.Hour),
		}, L1)
		require.NoError(t, err)
		_, err = rd.Subscribe(syncerID)
		require.NoError(t, err)
		require.NoError(t, rd.AddBlockToTrack(ctx, syncerID, 5, hash))
		return rd
	}
	// stored returns a StoredBlockHashFunc that counts its calls
	stored := func(calls *int, h common.Hash, ok bool, err error) StoredBlockHashFunc {
		return func(context.Context, uint64) (common.Hash, bool, error) {
			*calls++
			return h, ok, err
		}
	}

	t.Run("tracked with the same hash: not finalized", func(t *testing.T) {
		calls := 0
		finalized, found, err := IsBlockFinalized(ctx, newDetector(t), syncerID, 5, hash,
			stored(&calls, hash, true, nil))
		require.NoError(t, err)
		require.True(t, found)
		require.False(t, finalized)
		require.Zero(t, calls)
	})

	t.Run("tracked with another hash: unknown", func(t *testing.T) {
		calls := 0
		finalized, found, err := IsBlockFinalized(ctx, newDetector(t), syncerID, 5, otherHash,
			stored(&calls, otherHash, true, nil))
		require.NoError(t, err)
		require.False(t, found)
		require.False(t, finalized)
		require.Zero(t, calls)
	})

	t.Run("not tracked and stored with the same hash: finalized", func(t *testing.T) {
		calls := 0
		finalized, found, err := IsBlockFinalized(ctx, newDetector(t), syncerID, 10, hash,
			stored(&calls, hash, true, nil))
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, finalized)
		require.Equal(t, 1, calls)
	})

	t.Run("not tracked and stored with another hash or not stored: unknown", func(t *testing.T) {
		for name, cb := range map[string]func(*int) StoredBlockHashFunc{
			"another hash": func(c *int) StoredBlockHashFunc { return stored(c, otherHash, true, nil) },
			"not found":    func(c *int) StoredBlockHashFunc { return stored(c, common.Hash{}, false, nil) },
		} {
			t.Run(name, func(t *testing.T) {
				calls := 0
				finalized, found, err := IsBlockFinalized(ctx, newDetector(t), syncerID, 10, hash, cb(&calls))
				require.NoError(t, err)
				require.False(t, found)
				require.False(t, finalized)
				require.Equal(t, 1, calls)
			})
		}
	})

	t.Run("unknown subscriber: error, never finalized, DB not read", func(t *testing.T) {
		calls := 0
		finalized, found, err := IsBlockFinalized(ctx, newDetector(t), "non-existent-syncer", 10, hash,
			stored(&calls, hash, true, nil))
		require.ErrorIs(t, err, ErrSubscriberNotFound)
		require.False(t, found)
		require.False(t, finalized)
		require.Zero(t, calls)
	})

	t.Run("zero hash: unknown, nothing is called", func(t *testing.T) {
		calls := 0
		tracker := &fakeTracker{err: errors.New("must not be called")}
		finalized, found, err := IsBlockFinalized(ctx, tracker, syncerID, 5, common.Hash{},
			stored(&calls, hash, true, nil))
		require.NoError(t, err)
		require.False(t, found)
		require.False(t, finalized)
		require.Zero(t, calls)
		require.Zero(t, tracker.calls)
	})

	t.Run("stored hash callback error propagates", func(t *testing.T) {
		calls := 0
		boom := errors.New("boom")
		finalized, found, err := IsBlockFinalized(ctx, newDetector(t), syncerID, 10, hash,
			stored(&calls, common.Hash{}, false, boom))
		require.ErrorIs(t, err, boom)
		require.False(t, found)
		require.False(t, finalized)
	})

	t.Run("tracker generic error propagates", func(t *testing.T) {
		calls := 0
		boom := errors.New("boom")
		finalized, found, err := IsBlockFinalized(ctx, &fakeTracker{err: boom}, syncerID, 10, hash,
			stored(&calls, hash, true, nil))
		require.ErrorIs(t, err, boom)
		require.False(t, found)
		require.False(t, finalized)
		require.Zero(t, calls)
	})
}

// TestIsBlockFinalizedAsksTrackerBeforeStoredHash checks the order of the checks with a reorg that completes
// between them. The reorg detector deletes the orphaned block from the syncer (through the notification) and
// only afterwards stops tracking it, so by the time the tracker says "not tracked" the DB doesn't have the
// block anymore. A DB-first implementation would have read the block (matching hash) before the reorg and
// then, seeing it untracked, would return finalized=true for an orphaned block. Tracker-first returns unknown.
func TestIsBlockFinalizedAsksTrackerBeforeStoredHash(t *testing.T) {
	hash := common.BigToHash(big.NewInt(5))
	var order []string
	dbHasBlock := true

	tracker := &fakeTracker{
		err: db.ErrNotFound,
		hook: func() {
			order = append(order, "tracker")
			// the reorg just completed: the syncer deleted the block and the detector stopped tracking it
			dbHasBlock = false
		},
	}
	storedHash := func(context.Context, uint64) (common.Hash, bool, error) {
		order = append(order, "stored")
		return hash, dbHasBlock, nil
	}

	finalized, found, err := IsBlockFinalized(context.Background(), tracker, "id", 5, hash, storedHash)
	require.NoError(t, err)
	require.False(t, found)
	require.False(t, finalized)
	require.Equal(t, []string{"tracker", "stored"}, order)
}

type fakeTracker struct {
	err   error
	hook  func()
	calls int
}

func (f *fakeTracker) GetTrackedBlockByBlockNumber(string, uint64) (*Header, error) {
	f.calls++
	if f.hook != nil {
		f.hook()
	}
	return nil, f.err
}
