package bridgetracker

import (
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var testHash = common.HexToHash(testTxHash)

func TestRegistryGetSnapshot(t *testing.T) {
	r := newMemoryRegistry()
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
	// only way the engine reaches a tx-level terminal error (handleNotFound/handleNotABridge,
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

// TestRegistryUpdateTrackingBridgeTxAfterErrorRevives pins a known gap: UpdateTrackingBridgeTx
// does not guard against being called after a terminal error, unlike UpdateTrackingStep. If
// AllSteps was already populated, a later tx-level update can make the bridge read as
// non-Failed again, because TrackingStatus/Failed derive from AllSteps whenever it is non-nil
func TestRegistryUpdateTrackingBridgeTxAfterErrorRevives(t *testing.T) {
	r := newMemoryRegistry()
	id := TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)

	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(false)))
	require.NoError(t, publishError(r, id, testErrorStep()))
	revived := testBridgeInfo()
	// the step update inside this publishStatus is itself a no-op (UpdateTrackingStep does
	// guard on terminal failure), so AllSteps stays at its pre-error, in-progress value
	require.NoError(t, publishStatus(r, id, revived, testAllSteps(true)))

	tracking, err := r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, types.TrackingStatusRunning, tracking.TrackingStatus())
	// not require.Same: revived is value-identical to the already-stored Info, so the store
	// keeps its existing pointer instead of reallocating (see UpdateTrackingBridgeTx)
	require.Equal(t, revived, tracking.Info())
	require.False(t, tracking.Failed())
}

// TestRegistryGetWithoutCreateReturnsNotFound pins the read-only lookup: Get with
// createIfNotExists=false neither creates nor guesses, it just reports the miss
func TestRegistryGetWithoutCreateReturnsNotFound(t *testing.T) {
	r := newMemoryRegistry()

	tracking, err := r.Get(TrackingID{NetworkID: 1, TxHash: testHash}, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
	require.Nil(t, tracking)

	r.mu.RLock()
	defer r.mu.RUnlock()
	require.Empty(t, r.bridges)
}

// TestRegistryUpdateTrackingBridgeTxUnregisteredReturnsNotFound pins that, unlike Get,
// UpdateTrackingBridgeTx never creates the entry on its own: the bridge must already be in
// the supervised list (via Get(id, true) or Subscribe)
func TestRegistryUpdateTrackingBridgeTxUnregisteredReturnsNotFound(t *testing.T) {
	r := newMemoryRegistry()
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	err := r.UpdateTrackingBridgeTx(id, types.TrackingStatusRegistered, domain.TrackingBridgeTx{Info: testBridgeInfo()})
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
}

func TestRegistryUpdateTrackingBridgeTxError(t *testing.T) {
	r := newMemoryRegistry()
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
	require.NoError(t, r.UpdateTrackingBridgeTx(id, types.TrackingStatusError, trackingBridgeTx))

	v, err := r.Get(id, false)
	require.NoError(t, err)
	require.Equal(t, v.TrackingBridgeTx(), trackingBridgeTx)
	require.Equal(t, trackingBridgeTx.Error, v.Error())
	require.Equal(t, types.TrackingStatusError, v.RawTrackingStatus())
}

func TestRegistryKeyIsolation(t *testing.T) {
	r := newMemoryRegistry()
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
	r := newMemoryRegistry()
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	ch, unsubscribe := r.Subscribe(id)
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
	r := newMemoryRegistry()
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	ch, unsubscribe := r.Subscribe(id)
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
	r := newMemoryRegistry()
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	ch, unsubscribe := r.Subscribe(id)
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
	r := newMemoryRegistry()

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
	r := newMemoryRegistry()

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
	r := newMemoryRegistry()
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
	r := newMemoryRegistry()
	id := TrackingID{NetworkID: 1, TxHash: testHash}

	ch, unsubscribe := r.Subscribe(id)
	unsubscribe()

	require.NoError(t, publishStatus(r, id, testBridgeInfo(), testAllSteps(false)))

	select {
	case update := <-ch:
		t.Fatalf("unexpected update after unsubscribe: %+v", update)
	default:
	}
}

func TestRegistryGetNetworks(t *testing.T) {
	r := newMemoryRegistry()

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
	r := newMemoryRegistry()
	require.Equal(t, 0, r.GetNumTracker())

	_, err := r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x01")}, true)
	require.NoError(t, err)
	_, err = r.Get(TrackingID{NetworkID: 1, TxHash: common.HexToHash("0x02")}, true)
	require.NoError(t, err)

	require.Equal(t, 2, r.GetNumTracker())
}

func TestRegistryUpdateTrackingStepGrowsAllSteps(t *testing.T) {
	r := newMemoryRegistry()
	id := TrackingID{NetworkID: 1, TxHash: testHash}
	_, err := r.Get(id, true)
	require.NoError(t, err)

	step := types.BridgeStepPath{Step: types.StepClaimed, Status: types.StepStatusDone}
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
	r := newMemoryRegistry()

	err := r.UpdateTrackingStep(TrackingID{NetworkID: 1, TxHash: testHash}, 0, types.BridgeStepPath{})
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
}
