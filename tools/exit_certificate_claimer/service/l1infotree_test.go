package claimer

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// stubGERProber is a configurable gerProber. Each call to GetInfoByGlobalExitRoot pops the next
// canned result from results (the last entry is reused once the slice is exhausted), so tests can
// drive multi-poll behaviour deterministically.
type stubGERProber struct {
	calls   int
	results []proberResult
}

type proberResult struct {
	leaf *l1infotreesync.L1InfoTreeLeaf
	err  error
}

func (s *stubGERProber) GetInfoByGlobalExitRoot(common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error) {
	r := s.results[min(s.calls, len(s.results)-1)]
	s.calls++
	return r.leaf, r.err
}

func TestResolveBlockFinality(t *testing.T) {
	t.Parallel()

	t.Run("empty defaults to latest", func(t *testing.T) {
		t.Parallel()
		got, err := resolveBlockFinality("")
		require.NoError(t, err)
		require.Equal(t, aggkittypes.LatestBlock, got)
	})

	t.Run("valid value is parsed", func(t *testing.T) {
		t.Parallel()
		f, err := aggkittypes.NewBlockNumberFinality("FinalizedBlock")
		require.NoError(t, err)

		got, err := resolveBlockFinality("FinalizedBlock")
		require.NoError(t, err)
		require.Equal(t, *f, got)
	})

	t.Run("invalid value is a hard error", func(t *testing.T) {
		t.Parallel()
		_, err := resolveBlockFinality("not-a-finality")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid l1Sync.blockFinality")
	})
}

func TestGERIndexed(t *testing.T) {
	t.Parallel()

	t.Run("present GER is indexed", func(t *testing.T) {
		t.Parallel()
		p := &stubGERProber{results: []proberResult{{leaf: &l1infotreesync.L1InfoTreeLeaf{}}}}
		indexed, err := gerIndexed(p, common.HexToHash("0x1"))
		require.NoError(t, err)
		require.True(t, indexed)
	})

	t.Run("ErrNotFound is reported as not indexed", func(t *testing.T) {
		t.Parallel()
		p := &stubGERProber{results: []proberResult{{err: db.ErrNotFound}}}
		indexed, err := gerIndexed(p, common.HexToHash("0x1"))
		require.NoError(t, err)
		require.False(t, indexed)
	})

	t.Run("other errors are propagated", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		p := &stubGERProber{results: []proberResult{{err: boom}}}
		_, err := gerIndexed(p, common.HexToHash("0x1"))
		require.ErrorIs(t, err, boom)
	})
}

func TestWaitForGER(t *testing.T) {
	t.Parallel()

	t.Run("returns nil once GER is indexed", func(t *testing.T) {
		t.Parallel()
		p := &stubGERProber{results: []proberResult{{leaf: &l1infotreesync.L1InfoTreeLeaf{}}}}
		err := waitForGER(context.Background(), p, common.HexToHash("0x1"), log.GetDefaultLogger())
		require.NoError(t, err)
		require.Equal(t, 1, p.calls)
	})

	t.Run("propagates the probe error", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		p := &stubGERProber{results: []proberResult{{err: boom}}}
		err := waitForGER(context.Background(), p, common.HexToHash("0x1"), log.GetDefaultLogger())
		require.ErrorIs(t, err, boom)
	})

	t.Run("returns ctx error when cancelled before the GER appears", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // already cancelled: the first poll misses, then the select sees ctx.Done

		p := &stubGERProber{results: []proberResult{{err: db.ErrNotFound}}}
		err := waitForGER(ctx, p, common.HexToHash("0x1"), log.GetDefaultLogger())
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestOpenL1InfoTreeSyncDisabled(t *testing.T) {
	t.Parallel()

	// A fresh read-only DB has no GERs indexed, so with sync disabled OpenL1InfoTree must fail with
	// the "enable l1Sync" guidance rather than attempting to dial L1.
	dbPath := filepath.Join(t.TempDir(), "l1infotree.sqlite")
	_, err := OpenL1InfoTree(
		context.Background(),
		L1SyncConfig{Enabled: false},
		dbPath,
		common.HexToHash("0xdead"),
		log.GetDefaultLogger(),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "L1 sync is disabled")
}
