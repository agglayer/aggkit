package bridgetracker

import (
	"slices"
	"sync"
	"time"

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
	// terminalSince is when the snapshot first became terminal (see isTerminal); zero while
	// it is not. It anchors the retention window PruneTerminal evicts by
	terminalSince time.Time
}

// triggerBufferSize bounds the backlog of newly registered ids awaiting the engine's
// immediate attention (see memoryRegistry.trigger). A full buffer never blocks registration
// or loses the entry itself — a dropped signal just leaves that one bridge for the next
// regular poll tick, exactly as if GetAndAwait had not signaled it at all
const triggerBufferSize = 256

// memoryRegistry is the in-memory adapter of the SupervisedRegistry port: the supervised
// list and its subscriptions live in this instance's memory, so it serves a single-instance
// deployment (see the statefulness note in the API doc for multi-instance alternatives)
type memoryRegistry struct {
	mu      sync.RWMutex
	bridges map[TrackingID]*bridgeEntry
	// now is the clock terminalSince is stamped with, injectable for tests
	now func() time.Time
	// maxEntries bounds how many distinct bridges can be registered at once (see create): an
	// unauthenticated caller registering an unbounded number of distinct (network, tx hash)
	// pairs would otherwise grow the registry (and the engine's active work queue) without
	// limit until each entry's unresolved/retention timeout elapses
	maxEntries int
	// trigger carries the ids of freshly created entries out to the tracking engine (see
	// Triggers), which resolves them immediately instead of waiting for its next poll tick
	trigger chan TrackingID
}

// isTerminal reports whether the snapshot will never change again: the bridge finished, or
// the tracker gave up resolving it. It is exactly the predicate GetTrackerActives excludes
// by — an entry out of the active list is never updated again, so it is safe to forget once
// its retention elapses
func isTerminal(tracking *domain.TrackingData) bool {
	return tracking.Failed() || tracking.TrackingStatus() == types.TrackingStatusFinished
}

// compile-time check: the in-memory adapter fulfils the full port
var _ SupervisedRegistry = (*memoryRegistry)(nil)

// terminallyFailed reports whether the tracker gave up resolving the bridge's tx at all: a
// tx-level terminal Error recorded while Info was still nil. Deliberately not
// domain.TrackingData.Failed(): TrackingStatus prioritizes AllSteps once it is non-nil, so a
// step-level error persisted in the same batch that also carries the bridge's first-ever Info —
// still nil in the store until the batch's UpdateTrackingBridgeTx call lands, see Engine.persist —
// would read as Failed() too, permanently blocking that same batch's remaining step writes.
// Checking the tx fields directly is immune to that write-order dependency
func terminallyFailed(tracking *domain.TrackingData) bool {
	tx := tracking.BridgeTx()
	return tx.Info == nil && tx.IsInTerminalError()
}

// NewMemoryRegistry returns an in-memory SupervisedRegistry that refuses to register more than
// maxEntries distinct bridges at once (see memoryRegistry.maxEntries); maxEntries <= 0 falls
// back to DefaultMaxTrackedBridges.
func NewMemoryRegistry(maxEntries int) SupervisedRegistry {
	return newMemoryRegistry(maxEntries)
}

// newMemoryRegistry returns the concrete type, for tests that inspect internals
func newMemoryRegistry(maxEntries int) *memoryRegistry {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxTrackedBridges
	}
	return &memoryRegistry{
		bridges:    make(map[TrackingID]*bridgeEntry),
		now:        time.Now,
		maxEntries: maxEntries,
		trigger:    make(chan TrackingID, triggerBufferSize),
	}
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
		var err error
		entry, err = r.create(id)
		if err != nil {
			return nil, err
		}
	}
	return entry.tracking, nil
}

// GetAndAwait implements SupervisedStore. On an already-registered id it behaves exactly like
// Get(id, true): no trigger, no wait. On a newly created id it subscribes before releasing the
// lock — so the update the trigger provokes (see create) cannot be missed between creation and
// the wait below — then blocks up to timeout for that first update, falling back to the
// entry's current snapshot if timeout elapses first
func (r *memoryRegistry) GetAndAwait(id TrackingID, timeout time.Duration) (*domain.TrackingData, error) {
	r.mu.Lock()
	entry, existed := r.bridges[id]
	if !existed {
		var err error
		entry, err = r.create(id)
		if err != nil {
			r.mu.Unlock()
			return nil, err
		}
	}

	if existed || timeout <= 0 {
		tracking := entry.tracking
		r.mu.Unlock()
		return tracking, nil
	}

	ch := make(chan *domain.TrackingData, 1)
	entry.subscribers[ch] = struct{}{}
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(entry.subscribers, ch)
		r.mu.Unlock()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case updated := <-ch:
		return updated, nil
	case <-timer.C:
		r.mu.RLock()
		defer r.mu.RUnlock()
		return entry.tracking, nil
	}
}

// Subscribe implements StatusNotifier. The channel has a buffer of one and updates are
// coalesced: if the subscriber is slow, older pending updates are replaced by the newest one
func (r *memoryRegistry) Subscribe(id TrackingID) (<-chan *domain.TrackingData, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.bridges[id]
	if !ok {
		var err error
		entry, err = r.create(id)
		if err != nil {
			return nil, nil, err
		}
	}
	ch := make(chan *domain.TrackingData, 1)
	entry.subscribers[ch] = struct{}{}

	unsubscribe := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(entry.subscribers, ch)
	}

	return ch, unsubscribe, nil
}

// UpdateTrackingBridgeTx implements SupervisedStore, notifying every subscriber of the bridge.
// The stored snapshot is only rebuilt (new pointer) when tx actually differs from what is
// stored — a pure allocation optimization, since AllSteps is carried over either way — but
// notify always fires: a batch that only changed AllSteps (via UpdateTrackingStep) still
// calls this method precisely to deliver that merged snapshot to subscribers (see the "even
// a no-op one" note on UpdateTrackingStep)
func (r *memoryRegistry) UpdateTrackingBridgeTx(id TrackingID, tx domain.TrackingBridgeTx) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.bridges[id]
	if !ok {
		return domain.ErrTrackingNotFound
	}
	if entry.tracking.TrackingBridgeTx().String() != tx.String() {
		entry.tracking = domain.NewTrackingData(id, tx, entry.tracking.AllSteps())
	}
	// every batch of changes ends in this call (see UpdateTrackingStep), so this is the single
	// place where an entry can be seen turning terminal, whichever field did it
	if entry.terminalSince.IsZero() && isTerminal(entry.tracking) {
		entry.terminalSince = r.now()
	}
	entry.notify(entry.tracking)
	return nil
}

// UpdateTrackingStep implements SupervisedStore. Unlike UpdateTrackingBridgeTx, it does not
// itself notify subscribers: a multi-step change (e.g. a transition that closes one step and
// opens the next) would otherwise surface as one partial snapshot per call. Callers that
// change one or more steps must follow up with an UpdateTrackingBridgeTx call — even a
// no-op one — so subscribers see exactly one consistent, fully-merged snapshot per batch
func (r *memoryRegistry) UpdateTrackingStep(id TrackingID, stepIndex uint, step BridgeStepPath) error {
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
	allSteps := make([]BridgeStepPath, max(len(prevSteps), int(stepIndex)+1))
	copy(allSteps, prevSteps)
	allSteps[stepIndex] = step

	entry.tracking = domain.NewTrackingData(id, entry.tracking.BridgeTx(), allSteps)
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
		if isTerminal(entry.tracking) {
			continue
		}
		active = append(active, entry.tracking)
	}
	return active, nil
}

// PruneTerminal implements SupervisedStore: it forgets every entry that became terminal
// before olderThan. A pruned entry is gone as if never requested — a later Get with
// createIfNotExists re-registers it from scratch, which is how a client retries a bridge the
// tracker gave up on. Subscribers are not a concern: the WebSocket handler closes the
// connection right after pushing a terminal snapshot, and an unsubscribe on an already
// pruned entry is harmless (it operates on the orphaned record)
func (r *memoryRegistry) PruneTerminal(olderThan time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pruned := 0
	for id, entry := range r.bridges {
		if !entry.terminalSince.IsZero() && entry.terminalSince.Before(olderThan) {
			delete(r.bridges, id)
			pruned++
		}
	}
	return pruned, nil
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

// create adds a fresh (Registered, nil BridgeStatus) entry for id, or domain.ErrRegistryFull if
// the registry is already at maxEntries. It also wakes the tracking engine to resolve id right
// away (see signalTrigger) instead of leaving it for the next poll tick. Callers must hold r.mu
func (r *memoryRegistry) create(id TrackingID) (*bridgeEntry, error) {
	if len(r.bridges) >= r.maxEntries {
		return nil, domain.ErrRegistryFull
	}

	entry := &bridgeEntry{
		tracking:    domain.NewTrackingData(id, domain.TrackingBridgeTx{}, nil),
		subscribers: make(map[chan *domain.TrackingData]struct{}),
	}
	r.bridges[id] = entry
	r.signalTrigger(id)
	return entry, nil
}

// signalTrigger notifies the tracking engine that id was just registered. It never blocks: a
// full buffer just means this particular id waits for the next regular poll tick like before.
// Callers must hold r.mu
func (r *memoryRegistry) signalTrigger(id TrackingID) {
	select {
	case r.trigger <- id:
	default:
	}
}

// Triggers implements domain.Triggerable
func (r *memoryRegistry) Triggers() <-chan TrackingID {
	return r.trigger
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
