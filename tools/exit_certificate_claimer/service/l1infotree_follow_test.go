package claimer

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// indexedL1 is an L1 Info Tree reader whose set of indexed GERs the test controls.
type indexedL1 struct {
	fakeL1
	mu      sync.Mutex
	indexed map[common.Hash]bool
}

func (l *indexedL1) GetInfoByGlobalExitRoot(ger common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.indexed[ger] {
		return nil, db.ErrNotFound
	}
	return &l1infotreesync.L1InfoTreeLeaf{}, nil
}

func (l *indexedL1) index(ger common.Hash) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.indexed[ger] = true
}

// testL1InfoTree is an l1InfoTree whose reader and syncs are fakes; it counts what gets opened.
type testL1InfoTree struct {
	*l1InfoTree
	reader      *indexedL1
	readerOpens int
	sessions    []*l1SyncSession
	cancelled   []*bool
}

func newTestL1InfoTree(t *testing.T, enabled bool) *testL1InfoTree {
	t.Helper()
	tt := &testL1InfoTree{reader: &indexedL1{indexed: map[common.Hash]bool{}}}
	tree := newL1InfoTree(context.Background(), L1SyncConfig{Enabled: enabled}, "l1.sqlite", log.GetDefaultLogger())
	tree.pollInterval = 5 * time.Millisecond
	tree.openReader = func(context.Context) (L1InfoTreeQuerier, error) {
		tt.readerOpens++
		return tt.reader, nil
	}
	tree.startSync = func(context.Context) (*l1SyncSession, error) {
		cancelled := false
		done := make(chan struct{})
		var once sync.Once
		session := &l1SyncSession{done: done, cancel: func() {
			once.Do(func() {
				cancelled = true
				close(done)
			})
		}}
		tt.sessions = append(tt.sessions, session)
		tt.cancelled = append(tt.cancelled, &cancelled)
		return session, nil
	}
	tt.l1InfoTree = tree
	return tt
}

// attempt is one bounded call to Open, like one follow mode attempt.
func (tt *testL1InfoTree) attempt(ger common.Hash) (L1InfoTreeQuerier, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	return tt.Open(ctx, ger)
}

// With L1 sync disabled and the GER missing, every retry fails without opening anything new.
func TestL1InfoTreeRetriesOpenNothingWhenSyncDisabled(t *testing.T) {
	t.Parallel()
	tt := newTestL1InfoTree(t, false)
	ger := common.HexToHash("0x1")
	for range 5 {
		_, err := tt.attempt(ger)
		require.ErrorContains(t, err, "L1 sync is disabled")
	}
	require.Equal(t, 1, tt.readerOpens)
	require.Empty(t, tt.sessions)

	// The same against the real database: one read-only handle, whatever the number of retries.
	onDisk := newL1InfoTree(context.Background(), L1SyncConfig{}, filepath.Join(t.TempDir(), "l1.sqlite"),
		log.GetDefaultLogger())
	_, err := onDisk.Open(context.Background(), ger)
	require.ErrorContains(t, err, "L1 sync is disabled")
	first := onDisk.reader
	require.NotNil(t, first)
	_, err = onDisk.Open(context.Background(), ger)
	require.ErrorContains(t, err, "L1 sync is disabled")
	require.Same(t, first, onDisk.reader)
}

// An attempt that gives up leaves the L1 sync running; the next ones reuse it, and it is stopped once
// the GER is indexed. One reader serves every run.
func TestL1InfoTreeReusesSyncAcrossAttempts(t *testing.T) {
	t.Parallel()
	tt := newTestL1InfoTree(t, true)
	ger := common.HexToHash("0x1")

	for range 3 {
		_, err := tt.attempt(ger)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	}
	require.Len(t, tt.sessions, 1, "one sync for the three attempts")
	require.False(t, *tt.cancelled[0], "kept running between attempts")

	tt.reader.index(ger)
	l1, err := tt.attempt(ger)
	require.NoError(t, err)
	require.Same(t, tt.reader, l1)
	require.True(t, *tt.cancelled[0], "stopped once the GER is indexed")
	require.Nil(t, tt.sync)

	// The next run: indexed while waiting, with a sync of its own; same reader.
	next := common.HexToHash("0x2")
	go func() {
		time.Sleep(10 * time.Millisecond)
		tt.reader.index(next)
	}()
	l1, err = tt.attempt(next)
	require.NoError(t, err)
	require.Same(t, tt.reader, l1)
	require.Len(t, tt.sessions, 2)
	require.True(t, *tt.cancelled[1])

	// A run whose GER is already there needs no sync at all.
	_, err = tt.attempt(next)
	require.NoError(t, err)
	require.Len(t, tt.sessions, 2)
	require.Equal(t, 1, tt.readerOpens)
}

// A sync that stopped on its own is replaced, not waited on forever.
func TestL1InfoTreeRestartsDeadSync(t *testing.T) {
	t.Parallel()
	tt := newTestL1InfoTree(t, true)
	ger := common.HexToHash("0x1")
	_, err := tt.attempt(ger)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(tt.sessions[0].done) // the multidownloader returned

	_, err = tt.attempt(ger)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Len(t, tt.sessions, 2)
}

func TestL1InfoTreeStartSyncFails(t *testing.T) {
	t.Parallel()
	tt := newTestL1InfoTree(t, true)
	tt.startSync = func(context.Context) (*l1SyncSession, error) { return nil, errors.New("dial failed") }
	_, err := tt.attempt(common.HexToHash("0x1"))
	require.ErrorContains(t, err, "dial failed")
	require.Nil(t, tt.sync)

	tt.openReader = func(context.Context) (L1InfoTreeQuerier, error) { return nil, errors.New("no db") }
	tt.reader = nil
	tt.l1InfoTree.reader = nil
	_, err = tt.attempt(common.HexToHash("0x1"))
	require.ErrorContains(t, err, "opening read-only L1 info tree")
}
