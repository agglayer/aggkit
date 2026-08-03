package domain

import (
	"fmt"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

type TrackingBridgeTx struct {
	// Error is nil while nothing has failed
	Error *types.ErrorStep
	// Info holds the bridge facts resolved once by BridgeEventSource.FindBridge; nil until
	// then. It is the sole source of the bridge's public BridgeStatus (see api.BridgeStatus):
	// nothing else needs to be stored to derive it
	Info *BridgeInfo
	// StartDate is when the tracker first saw this bridge unresolved (Info nil); zero until
	// then. It never moves once set: it is the anchor Timeout counts from, not a per-attempt
	// timestamp
	StartDate time.Time
	// Timeout is how long the tracker waits, since StartDate, for Info to resolve before
	// giving up on the bridge altogether (TrackingStatus becomes Error), regardless of why it
	// hasn't resolved (not found yet, or a transient source failure that keeps recurring)
	Timeout time.Duration
}

// String implements fmt.Stringer, dereferencing Error/Info so it reflects their values instead
// of their pointer identity. Used to compare two TrackingBridgeTx by value (registry
// UpdateTrackingBridgeTx no-op check): the struct itself is not comparable with == for that
// purpose, since Error/Info are re-allocated on every resolution even when unchanged
func (t TrackingBridgeTx) String() string {
	return fmt.Sprintf("error=%+v info=%+v startDate=%s timeout=%s",
		t.Error, t.Info, t.StartDate, t.Timeout)
}

// IsDone reports whether the bridge is already resolved: Info is known and there is no
// outstanding Error to retry. Mirrors the early-return guard in Engine.resolveBridgeTx
func (t TrackingBridgeTx) IsDone() bool {
	return t.Info != nil && t.Error == nil
}

// IsInPermanentError reports whether the tx-level Error is permanent: retrying cannot change
// the outcome (e.g. the tx exists but is not a bridge transaction)
func (t TrackingBridgeTx) IsInPermanentError() bool {
	return t.Error != nil && t.Error.ErrorType == types.StepErrorPermanent
}

// IsInTerminalError reports whether the tx-level Error is one the tracker will not retry:
// permanent, or exhausted (it gave up after Timeout). A transient error still being retried
// is not terminal. It is the fact TrackingStatus derives Error from while AllSteps is not
// resolved yet
func (t TrackingBridgeTx) IsInTerminalError() bool {
	return t.IsInPermanentError() || (t.Error != nil && t.Error.ErrorType == types.StepErrorExhausted)
}

// IsOutdated reports whether tstamp is past the point where the tracker gives up on resolving
// the bridge: Timeout has elapsed since StartDate. False while StartDate is still zero (the
// tracker has not started counting yet). It is the give-up check of Engine.handleUnresolved,
// which records the death as an Exhausted Error — the persisted fact TrackingStatus derives
// Error from
func (t TrackingBridgeTx) IsOutdated(tstamp time.Time) bool {
	if t.StartDate.IsZero() {
		return false
	}
	return tstamp.Sub(t.StartDate) >= t.Timeout
}
