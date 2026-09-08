package domain

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// maxErrorDescriptions caps how many entries an ErrorStep.Description accumulates across
// retries of the same transient failure — kept low so a step (or the tx-level error in
// ResolveBridgeTx, which shares this same accumulate-on-retry shape) stuck retrying for a long
// time does not grow its stored/serialized error without bound. RetryCount itself is unaffected,
// it keeps counting every retry; only the description history is trimmed, to its most recent
// entries — the oldest ones are the least useful for diagnosing why it is still failing now
const maxErrorDescriptions = 10

// appendErrorDescription appends desc to descriptions (copying first, same as the call sites
// already did, so the previous ErrorStep.Description slice is never mutated), keeping only the
// most recent maxErrorDescriptions entries
func appendErrorDescription(descriptions []string, desc string) []string {
	descriptions = append(append([]string{}, descriptions...), desc)
	if len(descriptions) > maxErrorDescriptions {
		descriptions = descriptions[len(descriptions)-maxErrorDescriptions:]
	}
	return descriptions
}

// ErrStepPending is returned by StepResolver.Resolve, or wrapped by a more specific sentinel
// (see ErrCertificateNotSettled), when the fact check succeeded but the milestone has not
// happened yet — not a failure, so ResolveSteps neither retries it as one (see UpdateStep's
// stepErr) nor logs it; it simply means there is nothing further to check this call. A resolver may
// still return a non-nil result alongside it, which UpdateStep attaches even though the step
// stays InProgress — e.g. a certificate's current (unsettled) status, so clients can see it
// progress while they wait instead of only once it settles
var ErrStepPending = errors.New("step not ready")

// StepResolver resolves whether a resolved bridge's current step has met its milestone. Every
// step resolver shares this exact shape — one per resolve_step_<name>.go file — so ResolveSteps
// can drive whichever one applies uniformly, regardless of which fact it checks. Each resolver
// holds the one driven port it actually needs (see its own NewXxxResolver), rather than taking
// it as a parameter here: unlike the fact itself, that dependency never varies between calls, so
// there is nothing gained by threading it through Resolve, and every resolver stays a small,
// independently constructible unit instead of all of them sharing one do-everything port
type StepResolver interface {
	// Resolve resolves the step at idx (tracking.AllSteps()[idx], always the bridge's current
	// one — see ResolveSteps). A nil error means the milestone is met: result becomes its
	// Result (nil for a step that never produces one, e.g. StepPendingInclusion — see
	// PendingInclusionResolver). An error matching ErrStepPending means the fact check succeeded
	// but the milestone has not happened yet; result may still be non-nil (see
	// ErrCertificateNotSettled), attached even though the step stays InProgress. Any other
	// error means the check itself failed
	Resolve(
		logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, idx int,
	) (result any, err error)
}

// ResolveSteps walks a resolved bridge (BridgeTx().IsDone(), AllSteps already seeded — see
// domain.ResolveBridgeTx/domain.PendingPath) through as much of its expected path as its
// current facts allow: it resolves the current step (the first not yet Done) via UpdateStep,
// and if that completed it, whichever step it lands on next, and so on, stopping at the first
// milestone still unmet (ErrStepPending) or the first real error. On a real error, every step
// completed earlier this same call stays Done — only the step whose resolver just failed is
// marked, via UpdateStep's stepErr, incrementing its retry count instead of discarding the
// in-tick progress
func ResolveSteps(
	ctx context.Context,
	logger aggkitcommon.Logger,
	resolvers map[types.BridgeStep]StepResolver,
	tracking *TrackingData, now time.Time,
) (*TrackingData, error) {
	for {
		idx := currentStepIndex(tracking.AllSteps())
		if idx < 0 {
			return tracking, nil
		}
		resolver, ok := resolvers[tracking.AllSteps()[idx].Step]
		if !ok {
			return tracking, nil
		}

		result, err := resolver.Resolve(logger, ctx, tracking, idx)
		switch {
		case errors.Is(err, ErrStepPending):
			return UpdateStep(tracking, idx, result, false, nil, now), nil
		case err != nil:
			return UpdateStep(tracking, idx, result, false, err, now), err
		}
		tracking = UpdateStep(tracking, idx, result, true, nil, now)
	}
}

// currentStepIndex returns the index of the first step not yet Done — the one that needs
// resolving next — or -1 once the whole path (through StepClaimed) is Done
func currentStepIndex(steps []BridgeStepPath) int {
	for i, sp := range steps {
		if sp.Status != types.StepStatusDone {
			return i
		}
	}
	return -1
}

// UpdateStep marks tracking's step at idx: Done with result and EndDate stamped if complete, or
// the current step still in progress (StartDate stamped if not already) otherwise — either way
// result becomes its Result, so a resolver can surface data before its milestone is fully met
// (see ErrCertificateNotSettled). If stepErr is non-nil, the step is instead marked
// StepStatusError — the step-level counterpart of the tx-level error handling in
// ResolveBridgeTx. A resolver marks stepErr as unrecoverable the same way a BridgeEventSource
// does (see Permanent/IsPermanent): IsPermanent(stepErr) makes the step StepErrorPermanent with
// just this failure, no point accumulating a retry history nothing will retry. Otherwise, if the
// step was already terminally failed going into this call (isTerminalStepError — nothing stops
// ResolveSteps from calling a failed step's resolver again, since currentStepIndex only checks
// Done, not Error), that terminal ErrorType is kept as-is and only its history extended: a plain
// stepErr from a later poll must never resurrect a terminal failure as merely StepErrorTransient.
// Any other stepErr is StepErrorTransient, accumulating onto the step's retry count and
// description instead of discarding the history of a transient source failure — description is
// capped to its most recent maxErrorDescriptions entries (see appendErrorDescription), retry
// count is not. Either way complete is meaningless here
// (a step cannot both fail and complete) and idx+1 is left untouched. With stepErr nil, any
// previous Error is cleared instead: a successful fact check, even an inconclusive one, clears a
// previous transient failure, evidence the retry is working, not just that a milestone was met.
// Completing idx opens idx+1 as the new current step (InProgress) — including StepClaimed, which
// gets its own resolver call (ClaimedResolver) like any other step, ResolveSteps simply asks it
// in the same loop iteration. Returns tracking unchanged only when there is truly nothing new to
// record: not complete, no stepErr, no Error to clear, and result unchanged from what is already
// stored. ResolveSteps calls this once per loop iteration, so completing one step (e.g.
// PendingInclusionResolver, see its doc) simply has the next resolver asked in turn
func UpdateStep(
	tracking *TrackingData, idx int, result any, complete bool, stepErr error, now time.Time,
) *TrackingData {
	steps := tracking.AllSteps()
	if idx < 0 || idx >= len(steps) {
		return tracking
	}
	if stepErr == nil && !complete && steps[idx].Error == nil && reflect.DeepEqual(steps[idx].Result(), result) {
		return tracking
	}

	newSteps := append([]BridgeStepPath(nil), steps...)

	current := newSteps[idx]
	current.SetResult(result)
	switch {
	case stepErr != nil:
		// captured before current.Status/Error are touched below: whether this step was
		// already terminally failed (nothing will resolve it further — see isTerminalStepError)
		// going into this call
		wasTerminal := isTerminalStepError(current)
		current.Status = types.StepStatusError
		switch {
		case IsPermanent(stepErr):
			// unrecoverable: no retry history to accumulate, nothing will retry this step
			current.Error = &types.ErrorStep{
				ErrorType:   types.StepErrorPermanent,
				Description: []string{stepErr.Error()},
			}
		case wasTerminal:
			// nothing stops ResolveSteps from calling this step's resolver again once it has
			// already failed terminally (currentStepIndex only checks Done, not Error) — a
			// resolver call that then happens to return a plain, non-Permanent-wrapped error
			// (e.g. a transient hiccup while re-checking an already-doomed fact) must not
			// resurrect a terminal failure as merely StepErrorTransient: that would misreport
			// TrackingStatus/ClaimStatus back to Running/Pending for a step that will never
			// complete. Keep the existing terminal ErrorType, only extend its history
			current.Error = &types.ErrorStep{
				ErrorType:   current.Error.ErrorType,
				RetryCount:  current.Error.RetryCount + 1,
				Description: appendErrorDescription(current.Error.Description, stepErr.Error()),
			}
		default:
			retryCount, description := 1, []string{stepErr.Error()}
			if current.Error != nil {
				retryCount = current.Error.RetryCount + 1
				description = appendErrorDescription(current.Error.Description, stepErr.Error())
			}
			current.Error = &types.ErrorStep{
				ErrorType:   types.StepErrorTransient,
				RetryCount:  retryCount,
				Description: description,
			}
		}
	case complete:
		current.Error = nil
		current.Status = types.StepStatusDone
		endDate := now
		current.EndDate = &endDate
	default:
		current.Error = nil
		current.Status = types.StepStatusInProgress
		if current.StartDate == nil {
			startDate := now
			current.StartDate = &startDate
		}
	}
	newSteps[idx] = current

	if complete && idx+1 < len(newSteps) {
		next := newSteps[idx+1]
		next.Status = types.StepStatusInProgress
		next.Error = nil
		startDate := now
		next.StartDate = &startDate
		newSteps[idx+1] = next
	}

	return NewTrackingData(tracking.ID(), tracking.BridgeTx(), newSteps)
}

// indexOfStep returns the index of stepID within steps, or -1 if it is not part of the path
func indexOfStep(steps []BridgeStepPath, stepID types.BridgeStep) int {
	for i, sp := range steps {
		if sp.Step == stepID {
			return i
		}
	}
	return -1
}
