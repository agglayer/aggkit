package domain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
)

// ErrBridgeTxNotFound is returned by BridgeEventSource.FindBridge when the transaction does
// not exist on the network yet. ResolveBridgeTx keeps retrying for up to unresolvedTimeout (the
// tx may simply not be mined yet) before marking the bridge as terminally failed
var ErrBridgeTxNotFound = errors.New("bridge tx not found")

// ErrBridgeTxNotABridge is returned by BridgeEventSource.FindBridge when the transaction
// exists but is definitely not a bridge transaction (reverted, or mined without emitting a
// BridgeEvent log): unlike ErrBridgeTxNotFound, retrying cannot change this, so it is wrapped
// as Permanent — ResolveBridgeTx marks the bridge as terminally failed immediately, no retries
var ErrBridgeTxNotABridge = Permanent(errors.New("tx is not a bridge transaction"))

// ErrSourceUnavailable is returned by BridgeEventSource.FindBridge when the bridge's origin
// network has no source configured to resolve it (e.g. no JSON-RPC client for a
// statically-configured network): like ErrBridgeTxNotABridge, this is a permanent condition
// that retrying cannot change, so it is wrapped as Permanent too
var ErrSourceUnavailable = Permanent(errors.New("bridge source unavailable"))

// BridgeEventSource is the driven port resolving the BridgeEvent behind a supervised tx on its
// origin network (RPC receipt or bridge service, resolved per network via the finder)
type BridgeEventSource interface {
	// FindBridge returns the facts of the bridge created by the tx, ErrBridgeTxNotFound if
	// the tx does not exist yet, ErrBridgeTxNotABridge if it exists but is definitely not
	// a bridge transaction, or ErrSourceUnavailable if the origin network has no source
	// configured to resolve it. Other errors are transient
	FindBridge(ctx context.Context, id TrackingID) (*BridgeInfo, error)
}

// ResolveBridgeTx resolves a supervised bridge's tx-level facts through source.FindBridge,
// deciding the resulting TrackingData — everything but its persistence, left to the caller (see
// Engine.resolveBridgeTx, which logs and persists the returned snapshot).
//
//   - Already resolved (IsDone): returned unchanged, no call to FindBridge at all — retrying an
//     already-successful resolution is a no-op.
//   - Success: Info/Error are updated and, the first time the bridge resolves, AllSteps is
//     seeded with the full pending path for its BridgeType (PendingPath) — clients see the
//     whole route the bridge will walk from the moment it resolves.
//   - Permanent failure (see Permanent/IsPermanent): the tx-level Error becomes Permanent, with
//     no retries.
//   - Transient failure (ErrBridgeTxNotFound, or anything else FindBridge returns): the
//     tx-level Error accumulates a retry, turning Exhausted once Timeout has elapsed since
//     StartDate (the moment the bridge was first seen unresolved).
func ResolveBridgeTx(
	ctx context.Context, source BridgeEventSource, tracking *TrackingData,
	unresolvedTimeout time.Duration, now time.Time,
) (*TrackingData, error) {
	if tracking == nil {
		return nil, errors.New("nil tracking data")
	}
	tx := tracking.BridgeTx()
	if tx.IsDone() {
		return tracking, nil
	}
	if tx.StartDate.IsZero() {
		tx.StartDate = now
		tx.Timeout = unresolvedTimeout
	}

	id := tracking.ID()
	info, err := source.FindBridge(ctx, id)
	if err == nil {
		tx.Info = info
		tx.Error = nil
		allSteps := tracking.AllSteps()
		if allSteps == nil {
			allSteps = PendingPath(info.BridgeType(), now)
		}
		return NewTrackingData(id, tx, allSteps), nil
	}

	// the tx may simply not be mined yet: give it until Timeout before giving up, same as any
	// other transient failure — only the persisted description reads more specifically
	description := err.Error()
	if errors.Is(err, ErrBridgeTxNotFound) {
		description = fmt.Sprintf("%s does not exist on the network", id)
	}

	if IsPermanent(err) {
		tx.Error = &types.ErrorStep{
			ErrorType:   types.StepErrorPermanent,
			Description: []string{description},
		}
		return NewTrackingData(id, tx, tracking.AllSteps()), err
	}

	retryCount, descriptions := 1, []string{description}
	if tx.Error != nil {
		retryCount = tx.Error.RetryCount + 1
		descriptions = append(append([]string{}, tx.Error.Description...), description)
	}
	errorType := types.StepErrorTransient
	if tx.IsOutdated(now) {
		errorType = types.StepErrorExhausted
	}
	tx.Error = &types.ErrorStep{
		ErrorType:   errorType,
		RetryCount:  retryCount,
		Description: descriptions,
	}
	return NewTrackingData(id, tx, tracking.AllSteps()), err
}
