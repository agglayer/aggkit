package types

import "github.com/ethereum/go-ethereum/common"

// BridgeKey identifies a supervised bridge: the network the creating tx was sent to plus its hash
type BridgeKey struct {
	NetworkID uint32
	TxHash    common.Hash
}

// BridgeUpdate is what subscribers of a supervised bridge receive: the same snapshot
// TrackingData carries (minus NetworkID/TxHash, which the subscription itself already
// identifies)
type BridgeUpdate struct {
	TrackingStatus TrackingStatus
	Status         *BridgeStatus
	StepIndex      *int
	AllSteps       []BridgeStepPath
	Error          *ErrorStep
}

// SupervisedStore is the driven port to the supervised-bridges state. The HTTP handlers use
// its read side (Register) and the tracking engine its write side (SetStatus / SetError).
//
// Implementations must be safe for concurrent use.
type SupervisedStore interface {
	// Register adds the bridge to the supervised list if it was not already being tracked
	// and returns the current snapshot: the tracking status (Registered for a brand new
	// entry), the last known bridge status, step index and steps (all nil while
	// TrackingStatus is Registered) and the terminal resolution error (nil unless the
	// tracker gave up trying to resolve the bridge at all)
	Register(
		networkID uint32, txHash common.Hash,
	) (TrackingStatus, *BridgeStatus, *int, []BridgeStepPath, *ErrorStep)

	// SetStatus stores the new tracking status, bridge status, step index and steps of a
	// supervised bridge. It is a no-op if the bridge is not in the supervised list (only
	// reads register bridges) or already failed terminally
	SetStatus(
		networkID uint32, txHash common.Hash,
		trackingStatus TrackingStatus, status *BridgeStatus, stepIndex *int, allSteps []BridgeStepPath,
	)

	// SetError marks a supervised bridge as terminally failed to resolve at all (e.g. the tx
	// does not exist on the network or is not a bridge tx): TrackingStatus becomes Error,
	// BridgeStatus/StepIndex/AllSteps stay nil, and errStep is exposed as TrackingData.Error.
	// It supersedes any previously known status and subsequent SetStatus calls for the
	// bridge are ignored
	SetError(networkID uint32, txHash common.Hash, errStep *ErrorStep)

	// ActiveBridges returns the keys of the supervised bridges that still need tracking:
	// entries that never failed to resolve and whose TrackingStatus is not yet Finished
	ActiveBridges() []BridgeKey
}

// StatusNotifier is the driven port push consumers (the WebSocket handler) use to follow a
// supervised bridge.
//
// Implementations must deliver every SetStatus / SetError of the same bridge as a
// BridgeUpdate to all its active subscriptions: both ports are two views of one subsystem
// and are always implemented together (see SupervisedRegistry).
type StatusNotifier interface {
	// Subscribe registers the bridge and returns a channel receiving every subsequent
	// BridgeUpdate plus an unsubscribe function. Deliveries follow latest-value semantics:
	// a slow subscriber never blocks the producer and always observes the most recent
	// snapshot (every update carries the full BridgeStatus, so intermediate snapshots can
	// be dropped safely)
	Subscribe(networkID uint32, txHash common.Hash) (<-chan BridgeUpdate, func())
}

// SupervisedRegistry is the full supervised-bridges subsystem: state plus change
// notification. The in-memory adapter (NewMemoryRegistry) implements it for a single
// instance; a shared-store adapter can replace it so several tracker instances behind a
// proxy answer for any registered tx (see the statefulness note in the API doc)
type SupervisedRegistry interface {
	SupervisedStore
	StatusNotifier
}
