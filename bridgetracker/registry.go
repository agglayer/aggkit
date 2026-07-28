package bridgetracker

import (
	"slices"
	"sync"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
)

// bridgeEntry is the registry record of a supervised bridge
type bridgeEntry struct {
	// tracking is the bridge's current snapshot; its zero value reports
	// TrackingStatusRegistered, which is exactly the state of a freshly created entry
	tracking *domain.TrackingData
	// subscribers holds one channel per active subscription (WebSocket connection)
	subscribers map[chan *domain.TrackingData]struct{}
}

// memoryRegistry is the in-memory adapter of the SupervisedRegistry port: the supervised
// list and its subscriptions live in this instance's memory, so it serves a single-instance
// deployment (see the statefulness note in the API doc for multi-instance alternatives)
type memoryRegistry struct {
	mu      sync.RWMutex
	bridges map[TrackingID]*bridgeEntry
}

// compile-time check: the in-memory adapter fulfils the full port
var _ SupervisedRegistry = (*memoryRegistry)(nil)

// terminallyFailed reports whether the tracker has ever given up resolving the bridge at all
// (the stored raw TrackingStatus was explicitly set to Error by handleNotFound / publishError).
// Unlike domain.TrackingData.Failed(), it never depends on AllSteps, so it stays accurate for
// a batch still being written (steps already updated, status not yet): a step-level error
// would otherwise make Failed() report a false terminal failure mid-batch
func terminallyFailed(tracking *domain.TrackingData) bool {
	return tracking.RawTrackingStatus() == types.TrackingStatusError
}

// NewMemoryRegistry returns an in-memory SupervisedRegistry
func NewMemoryRegistry() SupervisedRegistry {
	return newMemoryRegistry()
}

// newMemoryRegistry returns the concrete type, for tests that inspect internals
func newMemoryRegistry() *memoryRegistry {
	return &memoryRegistry{bridges: make(map[TrackingID]*bridgeEntry)}
}

// Get implements SupervisedStore
func (r *memoryRegistry) Get(id TrackingID, createIfNotExists bool) (*domain.TrackingData, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.bridges[id]
	if !ok {
		if !createIfNotExists {
			return nil, domain.ErrTrackingNotFound
		}
		entry = r.create(id)
	}
	return entry.tracking, nil
}

// Subscribe implements StatusNotifier. The channel has a buffer of one and updates are
// coalesced: if the subscriber is slow, older pending updates are replaced by the newest one
func (r *memoryRegistry) Subscribe(id TrackingID) (<-chan *domain.TrackingData, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.bridges[id]
	if !ok {
		entry = r.create(id)
	}
	ch := make(chan *domain.TrackingData, 1)
	entry.subscribers[ch] = struct{}{}

	unsubscribe := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(entry.subscribers, ch)
	}

	return ch, unsubscribe
}

// UpdateTrackingBridgeTx implements SupervisedStore, notifying every subscriber of the bridge.
// The stored snapshot is only rebuilt (new pointer) when trackingStatus/tx actually differ
// from what is stored — a pure allocation optimization, since AllSteps is carried over either
// way — but notify always fires: a batch that only changed AllSteps (via UpdateTrackingStep)
// still calls this method precisely to deliver that merged snapshot to subscribers (see the
// "even a no-op one" note on UpdateTrackingStep)
func (r *memoryRegistry) UpdateTrackingBridgeTx(
	id TrackingID, trackingStatus types.TrackingStatus, tx domain.TrackingBridgeTx,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.bridges[id]
	if !ok {
		return domain.ErrTrackingNotFound
	}
	if entry.tracking.RawTrackingStatus() != trackingStatus ||
		entry.tracking.TrackingBridgeTx().String() != tx.String() {
		entry.tracking = domain.NewTrackingData(id, trackingStatus, tx, entry.tracking.AllSteps())
	}
	entry.notify(entry.tracking)
	return nil
}

// UpdateTrackingStep implements SupervisedStore. Unlike UpdateTrackingBridgeTx, it does not
// itself notify subscribers: a multi-step change (e.g. a transition that closes one step and
// opens the next) would otherwise surface as one partial snapshot per call. Callers that
// change one or more steps must follow up with an UpdateTrackingBridgeTx call — even a
// no-op one — so subscribers see exactly one consistent, fully-merged snapshot per batch
func (r *memoryRegistry) UpdateTrackingStep(id TrackingID, stepIndex uint, step types.BridgeStepPath) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.bridges[id]
	if !ok {
		return domain.ErrTrackingNotFound
	}
	if terminallyFailed(entry.tracking) {
		// terminal failure is final: nothing may resurrect the bridge afterwards
		return nil
	}

	prevSteps := entry.tracking.AllSteps()
	allSteps := make([]types.BridgeStepPath, max(len(prevSteps), int(stepIndex)+1))
	copy(allSteps, prevSteps)
	allSteps[stepIndex] = step

	entry.tracking = domain.NewTrackingData(id, entry.tracking.RawTrackingStatus(), entry.tracking.BridgeTx(), allSteps)
	return nil
}

// GetTrackerActives implements SupervisedStore: snapshots of the entries that never failed
// to resolve whose TrackingStatus is not yet Finished, optionally filtered to one network
func (r *memoryRegistry) GetTrackerActives(networkID *uint32) ([]*domain.TrackingData, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	active := make([]*domain.TrackingData, 0, len(r.bridges))
	for id, entry := range r.bridges {
		if networkID != nil && id.NetworkID != *networkID {
			continue
		}
		if entry.tracking.Failed() || entry.tracking.TrackingStatus() == types.TrackingStatusFinished {
			continue
		}
		active = append(active, entry.tracking)
	}
	return active, nil
}

// GetNetworks implements SupervisedStore: the networks with at least one supervised bridge,
// optionally filtered to those with at least one bridge in the given TrackingStatus
func (r *memoryRegistry) GetNetworks(status *types.TrackingStatus) ([]uint32, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[uint32]struct{})
	for id, entry := range r.bridges {
		if status != nil && entry.tracking.TrackingStatus() != *status {
			continue
		}
		seen[id.NetworkID] = struct{}{}
	}

	networks := make([]uint32, 0, len(seen))
	for networkID := range seen {
		networks = append(networks, networkID)
	}
	slices.Sort(networks)
	return networks, nil
}

// GetNumTracker implements SupervisedStore
func (r *memoryRegistry) GetNumTracker() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.bridges)
}

// create adds a fresh (Registered, nil BridgeStatus) entry for id. Callers must hold r.mu
func (r *memoryRegistry) create(id TrackingID) *bridgeEntry {
	entry := &bridgeEntry{
		tracking:    domain.NewTrackingData(id, types.TrackingStatusRegistered, domain.TrackingBridgeTx{}, nil),
		subscribers: make(map[chan *domain.TrackingData]struct{}),
	}
	r.bridges[id] = entry
	return entry
}

// notify delivers the update to every subscriber with latest-value semantics:
// a pending (unread) update is dropped in favor of the new one, so a slow
// subscriber always receives the most recent snapshot without blocking the
// tracking engine. Callers must hold r.mu
func (e *bridgeEntry) notify(update *domain.TrackingData) {
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
