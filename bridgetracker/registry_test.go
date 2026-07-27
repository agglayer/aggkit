package bridgetracker

import (
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var testHash = common.HexToHash(testTxHash)

func TestRegistryRegisterSnapshot(t *testing.T) {
	r := newMemoryRegistry()

	// first register: entry created, no info -> Registered
	trackingStatus, status, stepIndex, allSteps, errStep := r.Register(1, testHash)
	require.Equal(t, types.TrackingStatusRegistered, trackingStatus)
	require.Nil(t, status)
	require.Nil(t, stepIndex)
	require.Nil(t, allSteps)
	require.Nil(t, errStep)

	// publish -> register returns the stored tracking status, status, step index and steps
	published := testBridgeStatus()
	publishedSteps := testAllSteps(false)
	r.SetStatus(1, testHash, types.TrackingStatusRunning, published, intPtr(0), publishedSteps)
	trackingStatus, status, stepIndex, allSteps, errStep = r.Register(1, testHash)
	require.Equal(t, types.TrackingStatusRunning, trackingStatus)
	require.Same(t, published, status)
	require.Equal(t, 0, *stepIndex)
	require.Equal(t, publishedSteps, allSteps)
	require.Nil(t, errStep)

	// terminal error -> register returns it and later publishes are ignored
	terminal := testErrorStep()
	r.SetError(1, testHash, terminal)
	r.SetStatus(1, testHash, types.TrackingStatusFinished, testBridgeStatus(), intPtr(0), testAllSteps(true))
	trackingStatus, status, stepIndex, allSteps, errStep = r.Register(1, testHash)
	require.Equal(t, types.TrackingStatusError, trackingStatus)
	require.Nil(t, status)
	require.Nil(t, stepIndex)
	require.Nil(t, allSteps)
	require.Same(t, terminal, errStep)
}

// TestRegistryPublishUnregistered pins that the tracking engine cannot create entries: only
// reads (REST polls / WS connects) add bridges to the supervised list
func TestRegistryPublishUnregistered(t *testing.T) {
	r := newMemoryRegistry()

	r.SetStatus(1, testHash, types.TrackingStatusRunning, testBridgeStatus(), intPtr(0), testAllSteps(false))
	r.SetError(1, testHash, testErrorStep())

	r.mu.RLock()
	defer r.mu.RUnlock()
	require.Empty(t, r.bridges)
}

func TestRegistryKeyIsolation(t *testing.T) {
	r := newMemoryRegistry()

	r.Register(1, testHash)
	r.Register(2, testHash) // same tx hash, different network

	r.SetStatus(1, testHash, types.TrackingStatusRunning, testBridgeStatus(), intPtr(0), testAllSteps(false))

	_, status, stepIndex, allSteps, _ := r.Register(2, testHash)
	require.Nil(t, status, "a publish on network 1 must not leak into network 2")
	require.Nil(t, stepIndex, "a publish on network 1 must not leak into network 2")
	require.Nil(t, allSteps, "a publish on network 1 must not leak into network 2")
}

func TestRegistrySubscribeReceivesUpdates(t *testing.T) {
	r := newMemoryRegistry()

	ch, unsubscribe := r.Subscribe(1, testHash)
	defer unsubscribe()

	published := testBridgeStatus()
	publishedSteps := testAllSteps(false)
	r.SetStatus(1, testHash, types.TrackingStatusRunning, published, intPtr(0), publishedSteps)

	update := <-ch
	require.Equal(t, types.TrackingStatusRunning, update.TrackingStatus)
	require.Same(t, published, update.Status)
	require.Equal(t, 0, *update.StepIndex)
	require.Equal(t, publishedSteps, update.AllSteps)
	require.Nil(t, update.Error)

	terminal := testErrorStep()
	r.SetError(1, testHash, terminal)

	update = <-ch
	require.Equal(t, types.TrackingStatusError, update.TrackingStatus)
	require.Nil(t, update.Status)
	require.Same(t, terminal, update.Error)
}

// TestRegistrySubscribeCoalesces pins the latest-value semantics: a slow subscriber never
// blocks the publisher and always observes the most recent snapshot
func TestRegistrySubscribeCoalesces(t *testing.T) {
	r := newMemoryRegistry()

	ch, unsubscribe := r.Subscribe(1, testHash)
	defer unsubscribe()

	first := testBridgeStatus()
	last := testBridgeStatus()
	r.SetStatus(1, testHash, types.TrackingStatusRunning, first, intPtr(0), testAllSteps(false))
	// not read yet: replaces the pending update
	r.SetStatus(1, testHash, types.TrackingStatusFinished, last, intPtr(0), testAllSteps(true))

	update := <-ch
	require.Same(t, last, update.Status)
	require.Equal(t, types.TrackingStatusFinished, update.TrackingStatus)

	select {
	case stale := <-ch:
		t.Fatalf("unexpected extra update: %+v", stale)
	default:
	}
}

// TestRegistryActiveBridges pins which entries the engine keeps tracking: registered
// bridges that never failed to resolve and are not yet claimed
func TestRegistryActiveBridges(t *testing.T) {
	r := newMemoryRegistry()

	pending := common.HexToHash("0x01")
	claimed := common.HexToHash("0x02")
	failed := common.HexToHash("0x03")
	r.Register(1, pending)
	r.Register(1, claimed)
	r.Register(1, failed)

	r.SetStatus(1, claimed, types.TrackingStatusFinished, testBridgeStatus(), intPtr(0), testAllSteps(true))
	r.SetError(1, failed, testErrorStep())

	active := r.ActiveBridges()
	require.Equal(t, []BridgeKey{{NetworkID: 1, TxHash: pending}}, active)
}

// TestRegistryActiveBridgesKeepsStepLevelErrors pins that a bridge resolved with a step-level
// error (e.g. a certificate stuck InError) stays active: the engine must keep polling it in
// case the error clears, unlike a bridge the tracker never managed to resolve at all
func TestRegistryActiveBridgesKeepsStepLevelErrors(t *testing.T) {
	r := newMemoryRegistry()

	erroring := common.HexToHash("0x04")
	r.Register(1, erroring)
	r.SetStatus(1, erroring, types.TrackingStatusError, testBridgeStatus(), intPtr(0), testAllSteps(false))

	active := r.ActiveBridges()
	require.Equal(t, []BridgeKey{{NetworkID: 1, TxHash: erroring}}, active)
}

func TestRegistryUnsubscribeStopsUpdates(t *testing.T) {
	r := newMemoryRegistry()

	ch, unsubscribe := r.Subscribe(1, testHash)
	unsubscribe()

	r.SetStatus(1, testHash, types.TrackingStatusRunning, testBridgeStatus(), intPtr(0), testAllSteps(false))

	select {
	case update := <-ch:
		t.Fatalf("unexpected update after unsubscribe: %+v", update)
	default:
	}
}
