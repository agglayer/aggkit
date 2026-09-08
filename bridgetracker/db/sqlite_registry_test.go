package db

import (
	"path"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const testTxHash = "0x1234567890123456789012345678901234567890123456789012345678901234"

var testHash = common.HexToHash(testTxHash)

// testBridgeInfo returns a BridgeInfo snapshot for tests (BridgeType derives to L2ToL1 since
// DestinationNetwork is the zero value, Mainnet)
func testBridgeInfo() *domain.BridgeInfo {
	return &domain.BridgeInfo{
		NetworkID: 1,
		LeafType:  types.BridgeLeafTypeAsset,
	}
}

// testAllSteps returns the expected-path snapshot matching testBridgeInfo, claimed or in progress
func testAllSteps(claimed bool) []domain.BridgeStepPath {
	step := types.StepPendingInclusion
	stepStatus := types.StepStatusInProgress
	if claimed {
		step = types.StepClaimed
		stepStatus = types.StepStatusDone
	}
	return []domain.BridgeStepPath{{Step: step, Status: stepStatus}}
}

// testErrorStep returns the ErrorStep snapshot for tests exercising a bridge the tracker gave
// up resolving (e.g. tx not found / not a bridge transaction)
func testErrorStep() *types.ErrorStep {
	return &types.ErrorStep{
		ErrorType:   types.StepErrorExhausted,
		RetryCount:  3,
		Description: []string{"bridge tx not found"},
	}
}

// publishStatus mirrors bridgetracker.BridgeTracker.Publish/publishStatus: it upserts info/
// allSteps through the store's fine-grained update methods, steps first (silent) then the tx
// last (which notifies), so subscribers see exactly one consistent, fully-merged snapshot
func publishStatus(
	store domain.SupervisedStore, id domain.TrackingID, info *domain.BridgeInfo, allSteps []domain.BridgeStepPath,
) error {
	tracking, err := store.Get(id, false)
	if err != nil {
		return err
	}
	for i, step := range allSteps {
		if err := store.UpdateTrackingStep(id, uint(i), step); err != nil {
			return err
		}
	}
	tx := tracking.BridgeTx()
	tx.Info = info
	return store.UpdateTrackingBridgeTx(id, tx)
}

// publishError mirrors bridgetracker.BridgeTracker.PublishError/publishError: it marks the
// bridge as terminally failed to resolve at all through the store
func publishError(store domain.SupervisedStore, id domain.TrackingID, errStep *types.ErrorStep) error {
	tracking, err := store.Get(id, false)
	if err != nil {
		return err
	}
	tx := tracking.BridgeTx()
	tx.Error = errStep
	return store.UpdateTrackingBridgeTx(id, tx)
}

// newTestSQLiteRegistry returns a sqliteRegistry backed by a fresh temp-dir DB file
func newTestSQLiteRegistry(t *testing.T) *sqliteRegistry {
	t.Helper()

	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	registry, err := NewSQLiteRegistry(dbPath, 0, log.WithFields("module", "bridgetracker_test"))
	require.NoError(t, err)
	r, ok := registry.(*sqliteRegistry)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, r.Close()) })
	return r
}

// TestSQLiteRegistryGetSnapshot pins the basic lifecycle: a fresh id reads back as Registered, a
// published snapshot round-trips through the DB (by value, not by pointer identity — unlike the
// in-memory adapter, a SQLite-backed snapshot is always freshly decoded), and a terminal
// tx-level error on a never-resolved bridge reads back as Failed
func TestSQLiteRegistryGetSnapshot(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}

	tracking, err := r.Get(id, true)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus())
	require.Nil(t, tracking.Info())
	require.Nil(t, tracking.StepIndex())
	require.Nil(t, tracking.AllSteps())
	require.False(t, tracking.Failed())

	published := testBridgeInfo()
	publishedSteps := testAllSteps(false)
	require.NoError(t, publishStatus(r, id, published, publishedSteps))
	tracking, err = r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRunning, tracking.TrackingStatus())
	require.Equal(t, published, tracking.Info())
	require.Equal(t, 0, *tracking.StepIndex())
	require.Equal(t, publishedSteps, tracking.AllSteps())
	require.False(t, tracking.Failed())

	unresolved := domain.TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x05")}
	_, err = r.Get(unresolved, true)
	require.NoError(t, err)
	terminal := testErrorStep()
	require.NoError(t, publishError(r, unresolved, terminal))
	tracking, err = r.Get(unresolved, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusError, tracking.TrackingStatus())
	require.Nil(t, tracking.Info())
	require.True(t, tracking.Failed())
	// unlike the in-memory adapter (which keeps the exact pointer passed to publishError), a
	// SQLite-backed snapshot always comes back freshly decoded from its JSON round-trip — which
	// is what populates ErrorTypeString in the first place (see ErrorStep.MarshalJSON)
	terminal.ErrorTypeString = terminal.ErrorType.String()
	require.Equal(t, terminal, tracking.Error())
}

// TestSQLiteRegistryGetNotFound pins that a never-registered id reports ErrTrackingNotFound
// when createIfNotExists is false, without creating it as a side effect
func TestSQLiteRegistryGetNotFound(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}

	_, err := r.Get(id, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
	require.Equal(t, 0, r.GetNumTracker())
}

// TestSQLiteRegistryPersistsAcrossInstances pins the actual point of this adapter: a second
// sqliteRegistry opened over the same DB file sees exactly what the first one wrote, as if the
// process had restarted
func TestSQLiteRegistryPersistsAcrossInstances(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "bridgetracker_test")

	first, err := NewSQLiteRegistry(dbPath, 0, logger)
	require.NoError(t, err)

	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	_, err = first.Get(id, true)
	require.NoError(t, err)
	published := testBridgeInfo()
	steps := testAllSteps(true)
	require.NoError(t, publishStatus(first, id, published, steps))
	firstSQLite, ok := first.(*sqliteRegistry)
	require.True(t, ok)
	require.NoError(t, firstSQLite.Close())

	second, err := NewSQLiteRegistry(dbPath, 0, logger)
	require.NoError(t, err)
	secondSQLite, ok := second.(*sqliteRegistry)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, secondSQLite.Close()) })

	tracking, err := second.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, published, tracking.Info())
	require.Equal(t, steps, tracking.AllSteps())
	require.Equal(t, types.TrackingStatusFinished, tracking.TrackingStatus())
	require.Equal(t, 1, second.GetNumTracker())
}

// TestSQLiteRegistrySchemaVersionMismatchIsAMiss pins that a row written under a different
// schema_version is treated exactly like a missing one: Get(id, false) reports
// ErrTrackingNotFound, and Get(id, true) resolves it as a fresh registration instead of
// attempting to decode the stale row
func TestSQLiteRegistrySchemaVersionMismatchIsAMiss(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}

	_, err := r.Get(id, true)
	require.NoError(t, err)
	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(true)))

	_, err = r.db.Exec("UPDATE tracked_bridge SET schema_version = ? WHERE network_id = ? AND tx_hash = ?",
		trackedBridgeSchemaVersion+1, id.NetworkID, id.TxHash.Hex())
	require.NoError(t, err)

	_, err = r.Get(id, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)

	tracking, err := r.Get(id, true)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRegistered, tracking.TrackingStatus(),
		"a stale row is discarded and re-registered from scratch, not decoded")
	require.Equal(t, 1, r.GetNumTracker(), "replacing a stale row must not double-count it")
}

// TestSQLiteRegistryUpdateTrackingBridgeTxAfterErrorRevives pins that a tx-level error on an
// already-resolved bridge (Info and AllSteps populated) is not terminal, and a later update
// fully applies
func TestSQLiteRegistryUpdateTrackingBridgeTxAfterErrorRevives(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)

	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(false)))
	require.NoError(t, publishError(r, id, testErrorStep()))

	tracking, err := r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRunning, tracking.TrackingStatus())
	require.False(t, tracking.Failed())

	revived := testBridgeInfo()
	require.NoError(t, publishStatus(r, id, revived, testAllSteps(true)))

	tracking, err = r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusFinished, tracking.TrackingStatus())
	require.Equal(t, revived, tracking.Info())
	require.False(t, tracking.Failed())
}

// TestSQLiteRegistryUpdateTrackingStepTerminallyFailedIsNoOp pins that UpdateTrackingStep is a
// no-op once the tx-level facts are a terminal give-up (Info nil, terminal error) — nothing may
// resurrect a bridge the tracker already gave up resolving
func TestSQLiteRegistryUpdateTrackingStepTerminallyFailedIsNoOp(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)
	require.NoError(t, publishError(r, id, testErrorStep()))

	require.NoError(t, r.UpdateTrackingStep(id, 0, domain.BridgeStepPath{Step: types.StepPendingInclusion}))

	tracking, err := r.Get(id, false)
	require.NoError(t, err)
	require.Nil(t, tracking.AllSteps(), "a terminally failed bridge's steps must stay untouched")
}

// TestSQLiteRegistryCapacity pins the maxEntries cap: a request that would exceed it is
// rejected outright, never evicting an existing entry to make room
func TestSQLiteRegistryCapacity(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	registry, err := NewSQLiteRegistry(dbPath, 1, log.WithFields("module", "bridgetracker_test"))
	require.NoError(t, err)
	r, ok := registry.(*sqliteRegistry)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	_, err = r.Get(domain.TrackingID{NetworkID: 1, TxHash: testHash}, true)
	require.NoError(t, err)

	_, err = r.Get(domain.TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x05")}, true)
	require.ErrorIs(t, err, domain.ErrRegistryFull)
	require.Equal(t, 1, r.GetNumTracker())
}

// TestSQLiteRegistryPruneTerminalIsANoOp pins that PruneTerminal never deletes a persisted row:
// pruning/retention stays an in-memory-only concern for now (see sqliteRegistry.PruneTerminal)
func TestSQLiteRegistryPruneTerminalIsANoOp(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)
	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(true)))

	pruned, err := r.PruneTerminal(r.now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, 0, pruned)
	require.Equal(t, 1, r.GetNumTracker(), "the terminal row must still be there")

	tracking, err := r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusFinished, tracking.TrackingStatus())
}

// TestSQLiteRegistryPruneIdleIsANoOp pins that PruneIdle never deletes a persisted row either
// (same reasoning as PruneTerminal)
func TestSQLiteRegistryPruneIdleIsANoOp(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)

	pruned, err := r.PruneIdle(r.now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, 0, pruned)
	require.Equal(t, 1, r.GetNumTracker())

	_, err = r.Get(id, false)
	require.NoError(t, err, "the idle row must still be there")
}

// TestSQLiteRegistrySubscribeNotify pins that UpdateTrackingBridgeTx delivers the merged
// snapshot to an active subscriber, and that unsubscribing stops delivery
func TestSQLiteRegistrySubscribeNotify(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}

	ch, unsubscribe, err := r.Subscribe(id)
	require.NoError(t, err)

	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(true)))

	select {
	case update := <-ch:
		require.Equal(t, types.TrackingStatusFinished, update.TrackingStatus())
	case <-time.After(time.Second):
		t.Fatal("expected a notification")
	}

	unsubscribe()
	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(false)))
	select {
	case <-ch:
		t.Fatal("must not receive updates after unsubscribing")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestSQLiteRegistryGetAndAwaitDeliversTriggeredUpdate pins that GetAndAwait on a fresh id
// receives the update the engine's trigger-driven resolution produces, without needing a
// regular poll tick — the whole point of Triggers()/signalTrigger
func TestSQLiteRegistryGetAndAwaitDeliversTriggeredUpdate(t *testing.T) {
	r := newTestSQLiteRegistry(t)
	id := domain.TrackingID{NetworkID: 1, TxHash: testHash}

	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case triggered := <-r.Triggers():
			require.Equal(t, id, triggered)
			require.NoError(t, publishStatus(r, triggered, testBridgeInfo(), testAllSteps(true)))
		case <-time.After(time.Second):
		}
	}()

	tracking, err := r.GetAndAwait(id, time.Second)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusFinished, tracking.TrackingStatus())
	<-done
}

// TestSQLiteRegistryGetNetworks mirrors the in-memory adapter's filtering behaviour
func TestSQLiteRegistryGetNetworks(t *testing.T) {
	r := newTestSQLiteRegistry(t)

	registered := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	finished := domain.TrackingID{NetworkID: 2, TxHash: common.HexToHash("0x05")}
	_, err := r.Get(registered, true)
	require.NoError(t, err)
	_, err = r.Get(finished, true)
	require.NoError(t, err)
	require.NoError(t, publishStatus(r, finished, testBridgeInfo(), testAllSteps(true)))

	networks, err := r.GetNetworks(nil)
	require.NoError(t, err)
	require.Equal(t, []uint32{1, 2}, networks)

	finishedStatus := types.TrackingStatusFinished
	networks, err = r.GetNetworks(&finishedStatus)
	require.NoError(t, err)
	require.Equal(t, []uint32{2}, networks)
}

// TestSQLiteRegistryGetTrackerActives pins that a terminal entry (Finished or Failed) is
// excluded, optionally filtered to one network
func TestSQLiteRegistryGetTrackerActives(t *testing.T) {
	r := newTestSQLiteRegistry(t)

	active := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	finished := domain.TrackingID{NetworkID: 2, TxHash: common.HexToHash("0x05")}
	_, err := r.Get(active, true)
	require.NoError(t, err)
	_, err = r.Get(finished, true)
	require.NoError(t, err)
	require.NoError(t, publishStatus(r, finished, testBridgeInfo(), testAllSteps(true)))

	actives, err := r.GetTrackerActives(nil)
	require.NoError(t, err)
	require.Len(t, actives, 1)
	require.Equal(t, active, actives[0].ID())

	networkTwo := uint32(2)
	actives, err = r.GetTrackerActives(&networkTwo)
	require.NoError(t, err)
	require.Empty(t, actives)
}
