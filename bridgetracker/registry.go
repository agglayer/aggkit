package bridgetracker

import (
	"sync"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
)

// BridgeUpdate is what subscribers of a supervised bridge receive: the current snapshot
type BridgeUpdate = types.BridgeUpdate

// bridgeEntry is the registry record of a supervised bridge
type bridgeEntry struct {
	// trackingStatus is the bridge's full lifecycle; its zero value is TrackingStatusRegistered,
	// which is exactly the state of a freshly created entry
	trackingStatus types.TrackingStatus
	// status is the last known BridgeStatus, nil while trackingStatus is Registered
	status *types.BridgeStatus
	// stepIndex is the last known index into allSteps, nil while trackingStatus is Registered
	stepIndex *int
	// allSteps is the last known expected path, nil while trackingStatus is Registered
	allSteps []types.BridgeStepPath
	// errorStep is set when the tracker gave up trying to resolve the bridge at all; the
	// entry no longer receives updates once set
	errorStep *types.ErrorStep
	// subscribers holds one channel per active subscription (WebSocket connection)
	subscribers map[chan BridgeUpdate]struct{}
}

// memoryRegistry is the in-memory adapter of the SupervisedRegistry port: the supervised
// list and its subscriptions live in this instance's memory, so it serves a single-instance
// deployment (see the statefulness note in the API doc for multi-instance alternatives)
type memoryRegistry struct {
	mu      sync.RWMutex
	bridges map[BridgeKey]*bridgeEntry
}

// compile-time check: the in-memory adapter fulfils the full port
var _ SupervisedRegistry = (*memoryRegistry)(nil)

// NewMemoryRegistry returns an in-memory SupervisedRegistry
func NewMemoryRegistry() SupervisedRegistry {
	return newMemoryRegistry()
}

// newMemoryRegistry returns the concrete type, for tests that inspect internals
func newMemoryRegistry() *memoryRegistry {
	return &memoryRegistry{bridges: make(map[BridgeKey]*bridgeEntry)}
}

// Register implements SupervisedStore
func (r *memoryRegistry) Register(
	networkID uint32, txHash common.Hash,
) (types.TrackingStatus, *types.BridgeStatus, *int, []types.BridgeStepPath, *types.ErrorStep) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := r.getOrCreate(networkID, txHash)
	return entry.trackingStatus, entry.status, entry.stepIndex, entry.allSteps, entry.errorStep
}

// Subscribe implements StatusNotifier. The channel has a buffer of one and updates are
// coalesced: if the subscriber is slow, older pending updates are replaced by the newest one
func (r *memoryRegistry) Subscribe(
	networkID uint32, txHash common.Hash,
) (<-chan BridgeUpdate, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry := r.getOrCreate(networkID, txHash)
	ch := make(chan BridgeUpdate, 1)
	entry.subscribers[ch] = struct{}{}

	unsubscribe := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(entry.subscribers, ch)
	}

	return ch, unsubscribe
}

// SetStatus implements SupervisedStore, notifying every subscriber of the bridge
func (r *memoryRegistry) SetStatus(
	networkID uint32, txHash common.Hash,
	trackingStatus types.TrackingStatus, status *types.BridgeStatus, stepIndex *int, allSteps []types.BridgeStepPath,
) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.bridges[BridgeKey{NetworkID: networkID, TxHash: txHash}]
	if !ok || entry.errorStep != nil {
		return
	}

	entry.trackingStatus = trackingStatus
	entry.status = status
	entry.stepIndex = stepIndex
	entry.allSteps = allSteps
	entry.notify(BridgeUpdate{
		TrackingStatus: trackingStatus, Status: status, StepIndex: stepIndex, AllSteps: allSteps,
	})
}

// SetError implements SupervisedStore, notifying every subscriber of the bridge
func (r *memoryRegistry) SetError(networkID uint32, txHash common.Hash, errStep *types.ErrorStep) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.bridges[BridgeKey{NetworkID: networkID, TxHash: txHash}]
	if !ok {
		return
	}

	entry.trackingStatus = types.TrackingStatusError
	entry.status = nil // the tracker never resolved the bridge; this supersedes any doubt
	entry.stepIndex = nil
	entry.allSteps = nil
	entry.errorStep = errStep
	entry.notify(BridgeUpdate{TrackingStatus: types.TrackingStatusError, Error: errStep})
}

// ActiveBridges implements SupervisedStore: entries that never failed to resolve whose
// TrackingStatus is not yet Finished
func (r *memoryRegistry) ActiveBridges() []BridgeKey {
	r.mu.RLock()
	defer r.mu.RUnlock()

	keys := make([]BridgeKey, 0, len(r.bridges))
	for key, entry := range r.bridges {
		if entry.errorStep != nil {
			continue
		}
		if entry.trackingStatus == types.TrackingStatusFinished {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// getOrCreate returns the entry of the bridge, creating it if missing.
// Callers must hold r.mu
func (r *memoryRegistry) getOrCreate(networkID uint32, txHash common.Hash) *bridgeEntry {
	key := BridgeKey{NetworkID: networkID, TxHash: txHash}
	entry, ok := r.bridges[key]
	if !ok {
		entry = &bridgeEntry{subscribers: make(map[chan BridgeUpdate]struct{})}
		r.bridges[key] = entry
	}
	return entry
}

// notify delivers the update to every subscriber with latest-value semantics:
// a pending (unread) update is dropped in favor of the new one, so a slow
// subscriber always receives the most recent snapshot without blocking the
// tracking engine. Callers must hold r.mu
func (e *bridgeEntry) notify(update BridgeUpdate) {
	for ch := range e.subscribers {
		select {
		case ch <- update:
		default:
			// drop the stale pending update and push the newest one
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- update:
			default:
			}
		}
	}
}
