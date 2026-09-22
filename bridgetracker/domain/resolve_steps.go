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

	// StartDate returns the deterministic on-chain time this step's own milestone began at, for
	// the rare step whose beginning is not simply "the moment the step before it finished" — nil
	// for every other step, which instead keeps whatever StartDate it was opened with, chained
	// from the previous step's own EndDate (see UpdateStep). info is the bridge's own immutable
	// facts, the only thing available to whichever resolver happens to be first on its path (see
	// WaitingGERUpdateResolver, called before that step's own Resolve ever runs — result is nil
	// then); result is this same step's own freshly resolved Resolve result, needed by a step
	// whose milestone is itself a single point-in-time fact (see ClaimedResolver)
	StartDate(info *BridgeInfo, result any) *time.Time

	// EndDate returns the deterministic on-chain time result carries proof this step's own
	// milestone was met at, or nil when result carries none — UpdateStep stamps now instead
	EndDate(result any) *time.Time

	// Warning returns a human-readable reason this step could not (fully) resolve some optional
	// deterministic data even though it completed normally, or nil when there is nothing to
	// report — nil for every resolver whose EndDate/StartDate never has a partial-failure mode of
	// its own to explain (only WaitingGERInjectionResolver does today, see its own doc). UpdateStep
	// attaches it as this step's own Error, with ErrorType StepErrorWarning: informational only,
	// it never causes a retry or reads as a failure (isTerminalStepError/isTransientStepError both
	// gate on Status == StepStatusError first, and this step's Status stays StepStatusDone)
	Warning(result any) *string
}

// blockTime converts a block's unix-second timestamp to *time.Time, or nil when ts is zero — the
// sentinel every non-pointer BlockTimestamp field in this package effectively uses for "not
// resolved yet" (e.g. types.GERUpdateResult.BlockTimestamp), since a real on-chain block at
// exactly the unix epoch does not happen in practice
func blockTime(ts uint64) *time.Time {
	if ts == 0 {
		return nil
	}
	t := time.Unix(int64(ts), 0).UTC()
	return &t
}

// blockTimePtr is blockTime for a field that is already nilable (e.g.
// types.CertificateData.BlockTimestamp), where nil unambiguously means "not resolved yet"
func blockTimePtr(ts *uint64) *time.Time {
	if ts == nil {
		return nil
	}
	return blockTime(*ts)
}

// ResolveSteps walks a resolved bridge (BridgeTx().IsDone(), AllSteps already seeded — see
// domain.ResolveBridgeTx/domain.PendingPath) through as much of its expected path as its
// current facts allow: it resolves the current step (the first not yet Done or Skipped) via
// UpdateStep, and if that completed it, whichever step it lands on next, and so on, stopping at
// the first milestone still unmet (ErrStepPending), or the first real error the claimed-bridge
// fallback below could not explain away. On a real error, every step completed earlier this same
// call stays Done — only the step whose resolver just failed is marked, via UpdateStep's
// stepErr, incrementing its retry count instead of discarding the in-tick progress.
//
// Whenever the current step is not StepClaimed itself, a real error — freshly returned by its
// resolver this call, or recorded from an earlier call — is given one more chance before
// being left as-is. For an existing error, the claim check runs before retrying the resolver,
// so a resolver that exhausts the context cannot starve the fallback on every tick. A terminal
// step's resolver is never retried (see isTerminalStepError). claimChecker.IsClaimed
// asks the destination network directly, on-chain, independently of whatever historical fact
// this step could not verify. If it reports the bridge already claimed, every not-yet-Done step
// from the failing one up to (but excluding) StepClaimed is marked StepStatusSkipped instead
// (see skipToClaimed) — the tracker gives up on verifying them specifically, not on the bridge —
// and StepClaimed itself is opened as the new current step, resolved through its own resolver
// like normal right after (its own ClaimFor fact is never skipped: it is the one step this
// fallback exists to still finalize genuinely). If IsClaimed reports the bridge not (yet)
// claimed, or the check itself fails, nothing changes: a fresh error is recorded exactly as it
// would be without this fallback, and an already-terminal one is left exactly as found — this
// same claim check simply runs again the next time ResolveSteps is asked about this bridge,
// instead of the tracker ever giving up on asking altogether
func ResolveSteps(
	ctx context.Context,
	logger aggkitcommon.Logger,
	resolvers map[types.BridgeStep]StepResolver,
	claimChecker ClaimChecker,
	tracking *TrackingData, now time.Time,
) (*TrackingData, error) {
	for {
		idx := currentStepIndex(tracking.AllSteps())
		if idx < 0 {
			return tracking, nil
		}
		step := tracking.AllSteps()[idx]

		claimChecked := step.Status == types.StepStatusError && step.Step != types.StepClaimed
		if claimChecked {
			idxError := &types.ErrorStep{
				ErrorType:   types.StepErrorTransient,
				Description: []string{lastDescription(step.Error)},
			}
			if step.Error != nil {
				idxError.ErrorType = step.Error.ErrorType
				idxError.RetryCount = step.Error.RetryCount
			}
			if skipped, ok := trySkipToClaimed(ctx, claimChecker, tracking, idx, idxError, now); ok {
				tracking = skipped
				continue
			}
		}
		if isTerminalStepError(step) {
			return tracking, nil
		}

		resolver, ok := resolvers[step.Step]
		if !ok {
			return tracking, nil
		}

		result, err := resolver.Resolve(logger, ctx, tracking, idx)
		switch {
		case errors.Is(err, ErrStepPending):
			return UpdateStep(tracking, idx, result, false, nil, now, resolver), nil
		case err != nil:
			if step.Step != types.StepClaimed && !claimChecked {
				errType := types.StepErrorTransient
				if IsPermanent(err) {
					errType = types.StepErrorPermanent
				}
				idxError := &types.ErrorStep{ErrorType: errType, RetryCount: 1, Description: []string{err.Error()}}
				if skipped, ok := trySkipToClaimed(ctx, claimChecker, tracking, idx, idxError, now); ok {
					tracking = skipped
					continue
				}
			}
			return UpdateStep(tracking, idx, result, false, err, now, resolver), err
		}
		tracking = UpdateStep(tracking, idx, result, true, nil, now, resolver)
	}
}

// lastDescription returns the most recent entry of e's Description — the reason a step
// last failed by, used as the Skipped reason when the claimed-bridge fallback rescues a step
// that already failed in an earlier call rather than this one. e is never nil here in practice
// (a step reaching StepStatusError always carries one, see UpdateStep), but a defensive fallback
// message is returned rather than risk a nil dereference over something this cosmetic
func lastDescription(e *types.ErrorStep) string {
	if e == nil || len(e.Description) == 0 {
		return "step failed for a reason retrying cannot fix"
	}
	return e.Description[len(e.Description)-1]
}

// trySkipToClaimed reports whether tracking's bridge is already claimed on its destination
// network (per claimChecker.IsClaimed, independent of idxError's cause), and if so returns
// tracking with every step from idx up to (excluding) StepClaimed marked skipped (see
// skipToClaimed). ok is false — tracking returned unchanged — whenever IsClaimed reports the
// bridge not claimed, or the check itself errors: either way there is nothing to explain away,
// the caller leaves idxError's cause recorded exactly as it would without this fallback
func trySkipToClaimed(
	ctx context.Context, claimChecker ClaimChecker, tracking *TrackingData, idx int,
	idxError *types.ErrorStep, now time.Time,
) (*TrackingData, bool) {
	claimed, err := claimChecker.IsClaimed(ctx, tracking.Info())
	if err != nil || !claimed {
		return tracking, false
	}
	return skipToClaimed(tracking, idx, idxError, now), true
}

// skipToClaimed marks every step from idx up to (excluding) StepClaimed as StepStatusSkipped —
// idx itself carries idxError as its own (the failure that triggered the fallback), ErrorType
// included, so a genuine Transient/Permanent error is not relabeled as StepErrorSkipped just
// because the tracker gave up chasing it: the two remain distinguishable on the wire. Every
// skipped step, idx included, has both StartDate and EndDate cleared to nil — Skipped means this
// step's own milestone was never actually verified (that is exactly why the fallback exists), so
// there is no real span to report; stamping EndDate with now (when the tracker merely gave up)
// would present a fact this step never established as if it had. StepClaimed is then opened as
// StepStatusInProgress, same as UpdateStep does for whichever step follows one it just
// completed, so the next loop iteration resolves it for real through its own resolver — this
// fallback never skips StepClaimed itself
func skipToClaimed(tracking *TrackingData, idx int, idxError *types.ErrorStep, now time.Time) *TrackingData {
	steps := tracking.AllSteps()
	claimedIdx := indexOfStep(steps, types.StepClaimed)
	newSteps := append([]BridgeStepPath(nil), steps...)

	for i := idx; i < claimedIdx; i++ {
		sp := newSteps[i]
		sp.Status = types.StepStatusSkipped
		sp.Error = nil
		sp.StartDate = nil
		sp.EndDate = nil
		if i == idx {
			sp.Error = idxError
		}
		newSteps[i] = sp
	}

	claimedStep := newSteps[claimedIdx]
	claimedStep.Status = types.StepStatusInProgress
	claimedStep.Error = nil
	if claimedStep.StartDate == nil {
		startDate := now
		claimedStep.StartDate = &startDate
	}
	newSteps[claimedIdx] = claimedStep

	return NewTrackingData(tracking.ID(), tracking.BridgeTx(), newSteps)
}

// currentStepIndex returns the index of the first step not yet Done or Skipped — the one that
// needs attention next — or -1 once the whole path (through StepClaimed) is Done/Skipped. A step
// already failed for a reason retrying cannot fix (isTerminalStepError) is still returned here,
// deliberately: unlike a step merely InProgress or transiently erroring, ResolveSteps never asks
// its resolver again for it (see UpdateStep's wasTerminal guard), but it is not otherwise treated
// as settled — ResolveSteps itself still runs the claimed-bridge fallback over it every call, on
// the chance the destination network has confirmed the claim since (see its own doc). Skipped,
// unlike a terminal error, is treated exactly like Done: skipToClaimed already decided nothing
// more will ever verify that step, there is truly nothing left for anything to retry
func currentStepIndex(steps []BridgeStepPath) int {
	for i, sp := range steps {
		if sp.Status != types.StepStatusDone && sp.Status != types.StepStatusSkipped {
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
// step was already terminally failed going into this call (isTerminalStepError), that terminal
// ErrorType is kept as-is and only its history extended: a plain stepErr from a later poll must
// never resurrect a terminal failure as merely StepErrorTransient — ResolveSteps itself never
// asks a terminal step's resolver again (see its own doc), so this only guards UpdateStep's own
// invariant as a standalone function against any other caller doing so.
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
// PendingInclusionResolver, see its doc) simply has the next resolver asked in turn.
//
// resolver is idx's own StepResolver, consulted only once complete: its EndDate(result) becomes
// current.EndDate (now if it returns nil, e.g. a step with no deterministic value of its own —
// see StepResolver's own doc), and idx+1 is opened chained onto that same value — its StartDate,
// deterministic or not. resolver.StartDate(tracking.Info(), result) is then given a chance to
// override current's own StartDate too, for the rare step whose beginning is itself a
// deterministic fact rather than simply "whenever the step before it happened to finish" (see
// ClaimedResolver). resolver may be nil, in which case now is used throughout, same as before
// this per-step deterministic-date support existed
func UpdateStep(
	tracking *TrackingData, idx int, result any, complete bool, stepErr error, now time.Time,
	resolver StepResolver,
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
			// ResolveSteps itself never asks a terminal step's resolver again (it routes
			// through trySkipToClaimed instead — see its own doc), so stepErr reaching here for
			// an already-terminal step only happens if some other caller invokes UpdateStep
			// directly. Guard it anyway: resurrecting a terminal failure as merely
			// StepErrorTransient would misreport TrackingStatus/ClaimStatus back to
			// Running/Pending for a step that will never complete. Keep the existing terminal
			// ErrorType, only extend its history
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
		if resolver != nil {
			if d := resolver.EndDate(result); d != nil {
				endDate = *d
			}
			if sd := resolver.StartDate(tracking.Info(), result); sd != nil {
				current.StartDate = sd
			}
			if w := resolver.Warning(result); w != nil {
				current.Error = &types.ErrorStep{ErrorType: types.StepErrorWarning, Description: []string{*w}}
			}
		}
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

	// stepErr == nil here too: a step cannot both fail and complete (see this func's own doc),
	// and only the complete branch above ever stamps current.EndDate
	if complete && stepErr == nil && idx+1 < len(newSteps) {
		next := newSteps[idx+1]
		next.Status = types.StepStatusInProgress
		next.Error = nil
		startDate := *current.EndDate
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
