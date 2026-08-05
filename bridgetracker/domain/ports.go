package domain

import (
	"errors"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

// ErrTrackingNotFound is returned by SupervisedStore.Get when the bridge is not in the
// supervised list and createIfNotExists was false
var ErrTrackingNotFound = errors.New("tracking not found")

// ErrRegistryFull is returned by SupervisedStore.Get and StatusNotifier.Subscribe when
// creating a new entry would exceed the registry's configured capacity. Without this bound,
// an unauthenticated caller could register an unbounded number of distinct (network, tx hash)
// pairs — each staying in memory until it resolves or its unresolved/retention timeout elapses
// — and exhaust the process's memory and the engine's active work queue
var ErrRegistryFull = errors.New("supervised registry is full")

// SupervisedStore is the driven port to the supervised-bridges state. The HTTP handlers use
// its read side (Get) and the tracking engine its write side (UpdateTrackingBridgeTx /
// UpdateTrackingStep).
//
// Implementations must be safe for concurrent use.
type SupervisedStore interface {
	// Get returns the current snapshot of the bridge. If it is not in the supervised list,
	// it returns ErrTrackingNotFound and a nil snapshot, unless createIfNotExists is true, in
	// which case it creates a fresh entry (Registered, with a nil BridgeStatus) and returns it
	Get(id TrackingID, createIfNotExists bool) (*TrackingData, error)

	// GetAndAwait behaves exactly like Get(id, true) on an already-registered id: no trigger,
	// no wait, immediate return. On a newly created id, it additionally wakes the tracking
	// engine to resolve it right away (see Triggerable) instead of leaving it for the next poll
	// tick, and waits up to timeout for that first resulting update before returning — so a
	// first-time caller gets a head start on real progress instead of the bare Registered
	// snapshot it would otherwise have to poll for. timeout <= 0 skips the wait entirely. If
	// timeout elapses with no update, it returns the entry's snapshot as-is
	GetAndAwait(id TrackingID, timeout time.Duration) (*TrackingData, error)

	// UpdateTrackingBridgeTx overwrites the tx-level facts of a supervised bridge (Error,
	// Info, StartDate, Timeout) and notifies subscribers with the resulting snapshot;
	// AllSteps is untouched by this call (see UpdateTrackingStep) and TrackingStatus needs no
	// writing at all, it is derived from the snapshot (see TrackingData.TrackingStatus). It
	// is a no-op if tx is identical to what is already stored. Returns ErrTrackingNotFound if
	// the bridge is not in the supervised list — unlike Get, it never creates the entry
	UpdateTrackingBridgeTx(id TrackingID, tx TrackingBridgeTx) error

	// UpdateTrackingStep replaces the step at stepIndex in the bridge's expected path,
	// growing AllSteps if stepIndex is beyond its current length. Unlike
	// UpdateTrackingBridgeTx, it does not itself notify subscribers — follow it with an
	// UpdateTrackingBridgeTx call so a batch of step changes surfaces as one consistent
	// snapshot instead of one partial notification per step. Returns ErrTrackingNotFound if
	// the bridge is not in the supervised list
	UpdateTrackingStep(id TrackingID, stepIndex uint, step BridgeStepPath) error

	// GetTrackerActives returns the snapshots of the supervised bridges that still need
	// tracking (never failed to resolve and not yet Finished), optionally filtered to a
	// single network
	GetTrackerActives(networkID *uint32) ([]*TrackingData, error)

	// GetNetworks returns the networks that have at least one supervised bridge, optionally
	// filtered to those with at least one bridge in the given TrackingStatus
	GetNetworks(status *types.TrackingStatus) ([]uint32, error)

	// GetNumTracker returns the number of supervised bridges
	GetNumTracker() int

	// PruneTerminal forgets the supervised bridges that reached a terminal state — Finished,
	// or the tracker gave up resolving them (Failed) — before olderThan, returning how many
	// were forgotten. A forgotten bridge is gone as if it had never been requested: a later
	// Get with createIfNotExists re-registers it and tracking starts from scratch, which is
	// how a client retries a bridge the tracker gave up on. Terminal snapshots stay queryable
	// until they are pruned, so clients observe the final TrackingStatus during the whole
	// retention window
	PruneTerminal(olderThan time.Time) (int, error)

	// PruneIdle forgets the supervised bridges — terminal or still active — that have neither
	// been accessed (Get/GetAndAwait) nor had an active WebSocket subscription since before
	// olderThan, returning how many were forgotten. A bridge with at least one active
	// subscription is never pruned this way, no matter how stale olderThan is: an open
	// WebSocket connection is itself ongoing interest in the bridge. This bounds the memory an
	// abandoned-but-still-active tracker (nobody polling, no subscriber, but not yet resolved)
	// would otherwise hold onto indefinitely, complementing PruneTerminal's grace period for
	// bridges that did resolve
	PruneIdle(olderThan time.Time) (int, error)
}

// StatusNotifier is the driven port push consumers (the WebSocket handler) use to follow a
// supervised bridge.
//
// Implementations must deliver every SupervisedStore write of the same bridge as a
// TrackingData snapshot to all its active subscriptions: both ports are two views of one
// subsystem and are always implemented together (see SupervisedRegistry).
type StatusNotifier interface {
	// Subscribe registers the bridge and returns a channel receiving every subsequent
	// TrackingData snapshot plus an unsubscribe function. Deliveries follow latest-value
	// semantics: a slow subscriber never blocks the producer and always observes the most
	// recent snapshot (every update carries the full snapshot, so intermediate ones can be
	// dropped safely). Returns ErrRegistryFull (nil channel/unsubscribe) if the bridge is not
	// yet registered and the registry is at capacity
	Subscribe(id TrackingID) (<-chan *TrackingData, func(), error)
}

// SupervisedRegistry is the full supervised-bridges subsystem: state plus change
// notification. The in-memory adapter (NewMemoryRegistry) implements it for a single
// instance; a shared-store adapter can replace it so several tracker instances behind a
// proxy answer for any registered tx (see the statefulness note in the API doc)
type SupervisedRegistry interface {
	SupervisedStore
	StatusNotifier
}

// Triggerable is an optional capability of a SupervisedStore: it exposes the ids of bridges
// that were just registered, so the tracking engine can resolve them immediately instead of
// waiting for its next poll tick (see GetAndAwait and Engine.Start). A store that has no such
// fast path simply does not implement it; the engine falls back to plain polling
type Triggerable interface {
	// Triggers returns the channel of freshly registered ids the engine should resolve right
	// away. A send may be dropped if the channel is full — that id is not lost, it is simply
	// left for the next regular poll tick like before
	Triggers() <-chan TrackingID
}
