package bridgetracker

import (
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var testHash = common.HexToHash(testTxHash)

func TestRegistryGetSnapshot(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	// first read: entry created, no info -> Registered
	tracking, err := r.Get(id, true)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	require.Nil(t, tracking.Info())
	require.Nil(t, tracking.StepIndex())
	require.Nil(t, tracking.AllSteps())
	require.False(t, tracking.Failed())

	// publish -> Get returns the stored status/steps, with TrackingStatus/StepIndex derived
	published := testBridgeInfo()
	publishedSteps := testAllSteps(false)
	require.NoError(t, publishStatus(r, id, published, publishedSteps))
	tracking, err = r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRunning, tracking.TrackingStatus())
	require.Same(t, published, tracking.Info())
	require.Equal(t, 0, *tracking.StepIndex())
	require.Equal(t, publishedSteps, tracking.AllSteps())
	require.False(t, tracking.Failed())

	// terminal error on a bridge never resolved -> Get returns it as Failed. This mirrors the
	// only way the engine reaches a tx-level terminal error (handleUnresolved/handlePermanentFailure,
	// both before AllSteps is ever populated): UpdateTrackingBridgeTx does not clear
	// Status/AllSteps itself, so a terminal error published after AllSteps was already set
	// would not read back as Failed (see TestRegistryUpdateTrackingBridgeTxAfterErrorRevives)
	unresolved := TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x05")}
	_, err = r.Get(unresolved, true)
	require.NoError(t, err)
	terminal := testErrorStep()
	require.NoError(t, publishError(r, unresolved, terminal))
	tracking, err = r.Get(unresolved, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusError, tracking.TrackingStatus())
	require.Nil(t, tracking.Info())
	require.Nil(t, tracking.StepIndex())
	require.Nil(t, tracking.AllSteps())
	require.True(t, tracking.Failed())
	require.Equal(t, terminal, tracking.Error())
}

// TestRegistryUpdateTrackingBridgeTxAfterErrorRevives pins that a tx-level error published on
// an already-resolved bridge (Info and AllSteps populated) is not terminal: TrackingStatus
// keeps deriving from AllSteps and Failed stays false (it requires Info to be nil), so later
// updates fully apply — the bridge simply keeps running. Terminal failures proper only happen
// before resolution (give-up paths), where Info/AllSteps are still nil
func TestRegistryUpdateTrackingBridgeTxAfterErrorRevives(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)

	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(false)))
	require.NoError(t, publishError(r, id, testErrorStep()))

	tracking, err := r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRunning, tracking.TrackingStatus(),
		"a tx-level error on a resolved bridge is not terminal: AllSteps still rules")
	require.False(t, tracking.Failed())

	revived := testBridgeInfo()
	require.NoError(t, publishStatus(r, id, revived, testAllSteps(true)))

	tracking, err = r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusFinished, tracking.TrackingStatus(),
		"the later update fully applies, steps included")
	require.Equal(t, revived, tracking.Info())
	require.False(t, tracking.Failed())
}

// TestRegistryGetWithoutCreateReturnsNotFound pins the read-only lookup: Get with
// createIfNotExists=false neither creates nor guesses, it just reports the miss
func TestRegistryGetWithoutCreateReturnsNotFound(t *testing.T) {
	r := newMemoryRegistry(0)

	tracking, err := r.Get(TrackingID{NetworkID: 1, TxHash: testHash}, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
	require.Nil(t, tracking)

	r.mu.RLock()
	defer r.mu.RUnlock()
	require.Empty(t, r.bridges)
}

// TestRegistryPruneTerminal pins the retention semantics: only entries that became terminal
// (Failed or Finished) before the deadline are forgotten; live entries survive however old
// they are, and a forgotten tx re-registers from scratch — the retry path for a bridge the
// tracker gave up on
func TestRegistryPruneTerminal(t *testing.T) {
	r := newMemoryRegistry(0)
	clock := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }

	live := TrackingID{NetworkID: 1, TxHash: testHash}
	failed := TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x05")}
	finished := TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x06")}
	for _, id := range []TrackingID{live, failed, finished} {
		_, err := r.Get(id, true)
		require.NoError(t, err)
	}
	require.NoError(t, publishStatus(r, live, testBridgeInfo(), testAllSteps(false)))
	require.NoError(t, publishError(r, failed, testErrorStep()))
	require.NoError(t, publishStatus(r, finished, testBridgeInfo(), testAllSteps(true)))

	// nothing became terminal before the deadline yet: everything is kept
	pruned, err := r.PruneTerminal(clock.Add(-time.Minute))
	require.NoError(t, err)
	require.Zero(t, pruned)
	require.Equal(t, 3, r.GetNumTracker())

	// past the deadline both terminals are forgotten; the live entry is kept however old
	pruned, err = r.PruneTerminal(clock.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 2, pruned)
	require.Equal(t, 1, r.GetNumTracker())
	_, err = r.Get(live, false)
	require.NoError(t, err)
	_, err = r.Get(failed, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)

	// a forgotten tx re-registers as new, with no trace of the old failure
	tracking, err := r.Get(failed, true)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	require.Nil(t, tracking.Error())
}

// TestRegistryPruneIdle pins the idle-eviction semantics: an entry with no active subscriber
// last accessed before the deadline is forgotten regardless of its tracking status (terminal or
// still active), while a subscribed entry is never a candidate however stale its lastAccess is
func TestRegistryPruneIdle(t *testing.T) {
	r := newMemoryRegistry(0)
	clock := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }

	idleActive := TrackingID{NetworkID: 1, TxHash: testHash}
	idleFinished := TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x05")}
	subscribed := TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x06")}
	for _, id := range []TrackingID{idleActive, idleFinished, subscribed} {
		_, err := r.Get(id, true)
		require.NoError(t, err)
	}
	require.NoError(t, publishStatus(r, idleFinished, testBridgeInfo(), testAllSteps(true)))
	_, unsubscribe, err := r.Subscribe(subscribed)
	require.NoError(t, err)
	defer unsubscribe()

	// nothing was accessed before the deadline yet: everything is kept
	pruned, err := r.PruneIdle(clock.Add(-time.Minute))
	require.NoError(t, err)
	require.Zero(t, pruned)
	require.Equal(t, 3, r.GetNumTracker())

	// past the deadline, both unsubscribed entries are forgotten — active or terminal makes no
	// difference — but the subscribed one survives regardless of how stale its lastAccess is
	pruned, err = r.PruneIdle(clock.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 2, pruned)
	require.Equal(t, 1, r.GetNumTracker())
	_, err = r.Get(idleActive, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
	_, err = r.Get(idleFinished, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
	_, err = r.Get(subscribed, false)
	require.NoError(t, err)
}

// TestRegistryPruneIdleSkipsRecentlyAccessedEntry pins that a plain Get (no subscription) counts
// as access and extends the idle window: a bridge a client keeps polling is never idle-evicted
// out from under it
func TestRegistryPruneIdleSkipsRecentlyAccessedEntry(t *testing.T) {
	r := newMemoryRegistry(0)
	clock := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	_, err := r.Get(id, true)
	require.NoError(t, err)

	// a later read at T+1min bumps lastAccess, so a deadline that would otherwise have caught
	// the original (T+0) access no longer does
	clock = clock.Add(time.Minute)
	_, err = r.Get(id, false)
	require.NoError(t, err)

	pruned, err := r.PruneIdle(clock.Add(-30 * time.Second))
	require.NoError(t, err)
	require.Zero(t, pruned)
	require.Equal(t, 1, r.GetNumTracker())
}

// TestRegistryUpdateTrackingBridgeTxUnregisteredReturnsNotFound pins that, unlike Get,
// UpdateTrackingBridgeTx never creates the entry on its own: the bridge must already be in
// the supervised list (via Get(id, true) or Subscribe)
func TestRegistryUpdateTrackingBridgeTxUnregisteredReturnsNotFound(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	err := r.UpdateTrackingBridgeTx(id, domain.TrackingBridgeTx{Info: testBridgeInfo()})
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
}

func TestRegistryUpdateTrackingBridgeTxError(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)

	trackingBridgeTx := domain.TrackingBridgeTx{
		Error: &types.ErrorStep{
			ErrorType:   types.StepErrorPermanent,
			RetryCount:  0,
			Description: []string{"test error"},
		},
	}
	require.NoError(t, r.UpdateTrackingBridgeTx(id, trackingBridgeTx))

	v, err := r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, v.TrackingBridgeTx(), trackingBridgeTx)
	require.Equal(t, trackingBridgeTx.Error, v.Error())
	// the permanent tx-level error alone derives the terminal status, nothing else was stored
	require.Equal(t, types.TrackingStatusError, v.TrackingStatus())
}

func TestRegistryKeyIsolation(t *testing.T) {
	r := newMemoryRegistry(0)
	network1 := TrackingID{NetworkID: 1, TxHash: testHash}
	network2 := TrackingID{NetworkID: 2, TxHash: testHash} // same tx hash, different network

	_, err := r.Get(network1, true)
	require.NoError(t, err)
	_, err = r.Get(network2, true)
	require.NoError(t, err)

	require.NoError(t, publishStatus(r, network1, testBridgeInfo(), testAllSteps(false)))

	tracking, err := r.Get(network2, false)
	require.NoError(t, err)
	require.Nil(t, tracking.Info(), "a publish on network 1 must not leak into network 2")
	require.Nil(t, tracking.StepIndex(), "a publish on network 1 must not leak into network 2")
	require.Nil(t, tracking.AllSteps(), "a publish on network 1 must not leak into network 2")
}

func TestRegistrySubscribeReceivesUpdates(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	ch, unsubscribe, err := r.Subscribe(id)
	require.NoError(t, err)
	defer unsubscribe()

	published := testBridgeInfo()
	publishedSteps := testAllSteps(false)
	require.NoError(t, publishStatus(r, id, published, publishedSteps))

	update := <-ch
	require.Equal(t, types.TrackingStatusRunning, update.TrackingStatus())
	require.Same(t, published, update.Info())
	require.Equal(t, 0, *update.StepIndex())
	require.Equal(t, publishedSteps, update.AllSteps())
	require.False(t, update.Failed())
}

// TestRegistrySubscribeReceivesErrorUpdate pins the terminal-failure notification on a bridge
// never resolved (AllSteps still nil): unlike TestRegistryUpdateTrackingBridgeTxAfterErrorRevives,
// there is no stale AllSteps to mask the derived TrackingStatus
func TestRegistrySubscribeReceivesErrorUpdate(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	ch, unsubscribe, err := r.Subscribe(id)
	require.NoError(t, err)
	defer unsubscribe()

	terminal := testErrorStep()
	require.NoError(t, publishError(r, id, terminal))

	update := <-ch
	require.Equal(t, types.TrackingStatusError, update.TrackingStatus())
	require.Nil(t, update.Info())
	require.True(t, update.Failed())
	require.Equal(t, terminal, update.Error())
}

// TestRegistrySubscribeCoalesces pins the latest-value semantics: a slow subscriber never
// blocks the publisher and always observes the most recent snapshot
func TestRegistrySubscribeCoalesces(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	ch, unsubscribe, err := r.Subscribe(id)
	require.NoError(t, err)
	defer unsubscribe()

	first := testBridgeInfo()
	last := testBridgeInfo()
	require.NoError(t, publishStatus(r, id, first, testAllSteps(false)))
	// not read yet: replaces the pending update
	require.NoError(t, publishStatus(r, id, last, testAllSteps(true)))

	update := <-ch
	// not require.Same: last is value-identical to first, so the store keeps first's pointer
	// instead of reallocating (see UpdateTrackingBridgeTx); the coalescing is still pinned by
	// TrackingStatus below, which only Finished (derived from the second publish's AllSteps)
	// can produce
	require.Equal(t, last, update.Info())
	require.Equal(t, types.TrackingStatusFinished, update.TrackingStatus())

	select {
	case stale := <-ch:
		t.Fatalf("unexpected extra update: %+v", stale)
	default:
	}
}

// TestRegistryGetTrackerActives pins which entries the engine keeps tracking: registered
// bridges that never failed to resolve and are not yet claimed
func TestRegistryGetTrackerActives(t *testing.T) {
	r := newMemoryRegistry(0)

	pending := TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}
	claimed := TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x02")}
	failed := TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x03")}
	for _, id := range []TrackingID{pending, claimed, failed} {
		_, err := r.Get(id, true)
		require.NoError(t, err)
	}

	require.NoError(t, publishStatus(r, claimed, testBridgeInfo(), testAllSteps(true)))
	require.NoError(t, publishError(r, failed, testErrorStep()))

	active, err := r.GetTrackerActives(nil)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, pending, active[0].ID())
}

// TestRegistryGetTrackerActivesFiltersByNetwork pins the optional network filter
func TestRegistryGetTrackerActivesFiltersByNetwork(t *testing.T) {
	r := newMemoryRegistry(0)

	network1 := TrackingID{NetworkID: 1, TxHash: testHash}
	network2 := TrackingID{NetworkID: 2, TxHash: testHash}
	_, err := r.Get(network1, true)
	require.NoError(t, err)
	_, err = r.Get(network2, true)
	require.NoError(t, err)

	networkID := uint32(1)
	active, err := r.GetTrackerActives(&networkID)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, network1, active[0].ID())
}

// TestRegistryGetTrackerActivesKeepsStepLevelErrors pins that a bridge resolved with a
// step-level error (e.g. a certificate stuck InError) stays active: the engine must keep
// polling it in case the error clears, unlike a bridge the tracker never managed to resolve
// at all
func TestRegistryGetTrackerActivesKeepsStepLevelErrors(t *testing.T) {
	r := newMemoryRegistry(0)
	erroring := TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x04")}

	_, err := r.Get(erroring, true)
	require.NoError(t, err)
	require.NoError(t, publishStatus(r, erroring, testBridgeInfo(), testAllStepsWithError()))

	active, err := r.GetTrackerActives(nil)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, erroring, active[0].ID())
}

func TestRegistryUnsubscribeStopsUpdates(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	ch, unsubscribe, err := r.Subscribe(id)
	require.NoError(t, err)
	unsubscribe()

	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(false)))

	select {
	case update := <-ch:
		t.Fatalf("unexpected update after unsubscribe: %+v", update)
	default:
	}
}

func TestRegistryGetNetworks(t *testing.T) {
	r := newMemoryRegistry(0)

	running := TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}
	registered := TrackingID{NetworkID: 2, TxHash: common.HexToHash("0x02")}
	_, err := r.Get(running, true)
	require.NoError(t, err)
	_, err = r.Get(registered, true)
	require.NoError(t, err)
	require.NoError(t, publishStatus(r, running, testBridgeInfo(), testAllSteps(false)))

	networks, err := r.GetNetworks(nil)
	require.NoError(t, err)
	require.Equal(t, []uint32{1, 2}, networks)

	runningStatus := types.TrackingStatusRunning
	networks, err = r.GetNetworks(&runningStatus)
	require.NoError(t, err)
	require.Equal(t, []uint32{1}, networks)
}

func TestRegistryGetNumTracker(t *testing.T) {
	r := newMemoryRegistry(0)
	require.Equal(t, 0, r.GetNumTracker())

	_, err := r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}, true)
	require.NoError(t, err)
	_, err = r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x02")}, true)
	require.NoError(t, err)

	require.Equal(t, 2, r.GetNumTracker())
}

func TestRegistryUpdateTrackingStepGrowsAllSteps(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)

	step := BridgeStepPath{Step: types.StepClaimed, Status: types.StepStatusDone}
	require.NoError(t, r.UpdateTrackingStep(id, 2, step))

	tracking, err := r.Get(id, false)
	require.NoError(t, err)
	allSteps := tracking.AllSteps()
	require.Len(t, allSteps, 3)
	require.Equal(t, step, allSteps[2])
	require.Zero(t, allSteps[0])
	require.Zero(t, allSteps[1])
}

func TestRegistryUpdateTrackingStepUnregisteredReturnsNotFound(t *testing.T) {
	r := newMemoryRegistry(0)

	err := r.UpdateTrackingStep(TrackingID{NetworkID: 1, TxHash: testHash}, 0, BridgeStepPath{})
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
}

// TestRegistryGetRefusesNewEntryPastCapacity guards against an unauthenticated caller growing
// the registry without bound: once maxEntries distinct bridges are registered, Get refuses to
// create another one (ErrRegistryFull), while already-registered bridges remain fully usable.
func TestRegistryGetRefusesNewEntryPastCapacity(t *testing.T) {
	r := newMemoryRegistry(2)

	_, err := r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}, true)
	require.NoError(t, err)
	_, err = r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x02")}, true)
	require.NoError(t, err)

	// A third, distinct bridge is refused: the registry is at capacity.
	_, err = r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x03")}, true)
	require.ErrorIs(t, err, domain.ErrRegistryFull)
	require.Equal(t, 2, r.GetNumTracker())

	// An already-registered bridge is unaffected by the cap, whether or not createIfNotExists.
	_, err = r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}, true)
	require.NoError(t, err)
	_, err = r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}, false)
	require.NoError(t, err)
}

// TestRegistryGetAndAwaitBumpsLastAccess pins that GetAndAwait counts as access too, on both
// the newly-created and the already-registered path — not just plain Get
func TestRegistryGetAndAwaitBumpsLastAccess(t *testing.T) {
	r := newMemoryRegistry(0)
	clock := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	_, err := r.GetAndAwait(id, 0)
	require.NoError(t, err)

	clock = clock.Add(time.Minute)
	_, err = r.GetAndAwait(id, 0)
	require.NoError(t, err)

	// a deadline that would have caught the original (T+0) access no longer does: the second
	// GetAndAwait call bumped lastAccess to T+1min
	pruned, err := r.PruneIdle(clock.Add(-30 * time.Second))
	require.NoError(t, err)
	require.Zero(t, pruned)
}

// TestRegistryGetAndAwaitExistingEntryReturnsImmediately pins that GetAndAwait on an
// already-registered id behaves exactly like Get(id, true): no trigger signal, no wait, even
// with a generous timeout
func TestRegistryGetAndAwaitExistingEntryReturnsImmediately(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)
	<-r.trigger // drain the signal from the Get(id, true) registration above

	start := time.Now()
	tracking, err := r.GetAndAwait(id, time.Minute)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	require.Less(t, time.Since(start), time.Second, "an already-registered id must never wait")

	select {
	case unexpected := <-r.trigger:
		t.Fatalf("unexpected trigger signal for an already-registered id: %+v", unexpected)
	default:
	}
}

// TestRegistryGetAndAwaitZeroTimeoutSkipsWait pins that timeout <= 0 disables the wait on a
// newly created entry: it still registers (and still signals the trigger) but returns
// immediately with the bare Registered snapshot
func TestRegistryGetAndAwaitZeroTimeoutSkipsWait(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	tracking, err := r.GetAndAwait(id, 0)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())

	select {
	case signaled := <-r.trigger:
		require.Equal(t, id, signaled)
	default:
		t.Fatal("expected a trigger signal for the newly created entry")
	}
}

// TestRegistryGetAndAwaitNewEntryWaitsForUpdate pins the core behavior: a newly created entry
// wakes the engine (via the trigger channel) and GetAndAwait blocks until that resolution
// publishes an update, well before the timeout elapses
func TestRegistryGetAndAwaitNewEntryWaitsForUpdate(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	published := testBridgeInfo()
	publishedSteps := testAllSteps(false)
	go func() {
		signaled := <-r.trigger
		require.Equal(t, id, signaled)
		require.NoError(t, publishStatus(r, id, published, publishedSteps))
	}()

	tracking, err := r.GetAndAwait(id, time.Second)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRunning, tracking.TrackingStatus())
	require.Same(t, published, tracking.Info())
}

// TestRegistryGetAndAwaitTimeoutFallsBackToSnapshot pins that, if nothing resolves the bridge
// in time, GetAndAwait gives up after timeout and returns the entry's current (still
// Registered) snapshot instead of blocking forever
func TestRegistryGetAndAwaitTimeoutFallsBackToSnapshot(t *testing.T) {
	r := newMemoryRegistry(0)
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	start := time.Now()
	tracking, err := r.GetAndAwait(id, 20*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	require.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)

	// the subscription used internally to wait must be cleaned up once the timeout fires
	r.mu.RLock()
	defer r.mu.RUnlock()
	require.Empty(t, r.bridges[id].subscribers)
}

// TestRegistryGetAndAwaitRefusesNewEntryPastCapacity mirrors the Get capacity guard for
// GetAndAwait
func TestRegistryGetAndAwaitRefusesNewEntryPastCapacity(t *testing.T) {
	r := newMemoryRegistry(1)
	_, err := r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}, true)
	require.NoError(t, err)

	tracking, err := r.GetAndAwait(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x02")}, time.Second)
	require.ErrorIs(t, err, domain.ErrRegistryFull)
	require.Nil(t, tracking)
}

// TestRegistrySubscribeRefusesNewEntryPastCapacity mirrors the Get case for the WebSocket
// subscription path, which registers a new bridge exactly the same way if it does not exist yet.
func TestRegistrySubscribeRefusesNewEntryPastCapacity(t *testing.T) {
	r := newMemoryRegistry(1)

	_, err := r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}, true)
	require.NoError(t, err)

	ch, unsubscribe, err := r.Subscribe(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x02")})
	require.ErrorIs(t, err, domain.ErrRegistryFull)
	require.Nil(t, ch)
	require.Nil(t, unsubscribe)
	require.Equal(t, 1, r.GetNumTracker())
}
