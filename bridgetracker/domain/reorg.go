package domain

import (
	"context"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

// Reorg support of the tracking engine (agglayer/aggkit#1970). A tracking is only closed once its
// StepClaimed is backed by a claim whose block is final (TrackingData.IsFinal). Until then, on
// every engine tick:
//
//   - CheckOrigin re-checks the origin bridge tx while its block is not final: a block hash that
//     changed means the bridge was reorged out, and the engine discards the resolved facts
//     (ResetBridgeTx) and resolves the tx again — wherever it was re-included, if anywhere.
//   - ReverifySteps re-checks the completed steps a reorg can undo, cheapest signal first, and
//     moves the tracking back when one is gone: StepClaimed back to StepWaitingClaim when the
//     claim is gone, StepWaitingGERInjection / StepWaitingL1InfoLeafAvailable (and every later
//     step) back when the injection / the leaf is gone.
//   - FinalizeClaim closes the tracking once the claim's block is final.
//
// A source error never moves anything: it only means "could not tell this time", the state is
// kept as is and checked again on the next tick (the bridge service answers 503 while it resolves
// a reorg of its own).

// FinalityResolver is the driven port deciding whether an entry (a bridge or a claim) mined in a
// block is final. Satisfied by finality.Resolver, see its doc for the rules (an explicit
// reported value wins, monotonic, receipt fallback when omitted). The bool is the answer to use,
// false whenever err != nil: an error only means it could not be proven final this time
type FinalityResolver interface {
	IsFinal(ctx context.Context, networkID uint32, txHash common.Hash, blockNum uint64, reported *bool) (bool, error)
}

// OriginBlockSource is the driven port to the canonical chain of a bridge's origin network
type OriginBlockSource interface {
	// CanonicalBlockHash returns the hash of the block currently canonical at blockNumber on
	// networkID
	CanonicalBlockHash(ctx context.Context, networkID uint32, blockNumber uint64) (common.Hash, error)
}

// InjectedGERChecker is the driven port re-checking the completed readiness steps through GET
// injected-l1-info-leaf, one request per call. Unlike
// WaitingGERInjectionSource.InjectedGERAtIndex it never scans the L2 for the injection block
type InjectedGERChecker interface {
	// InjectedGERPresent re-checks a completed StepWaitingGERInjection: it reports whether
	// leafIndex (or a later leaf) is still injected on bridge's destination network, and whether
	// the bridge service reports that injection as final (block_finalized true)
	InjectedGERPresent(ctx context.Context, bridge *BridgeInfo, leafIndex uint32) (present, finalized bool, err error)
	// L1InfoLeafFinalized reports whether the bridge service that answered
	// L1InfoLeafAvailableSource.L1InfoTreeIndexForBridge for bridge reports the L1 block of the
	// L1 info tree leaf leafIndex as final (network_id 0, block_finalized true). False when it
	// does not say (block_finalized omitted, as bridge services before it existed do) or does
	// not have the leaf (anymore): it only tells whether the leaf can stop being re-checked,
	// never that it is gone (agglayer/aggkit#1987)
	L1InfoLeafFinalized(ctx context.Context, bridge *BridgeInfo, leafIndex uint32) (finalized bool, err error)
}

// ReorgCheckers groups the driven ports CheckOrigin, ReverifySteps and FinalizeClaim use
type ReorgCheckers struct {
	OriginBlocks OriginBlockSource
	Finality     FinalityResolver
	ClaimChecker ClaimChecker
	Claims       ClaimSource
	InjectedGERs InjectedGERChecker
	Leaves       L1InfoLeafAvailableSource
}

// CheckOrigin re-checks the origin bridge tx of a resolved tracking (BridgeTx().IsDone()) whose
// origin block is not known final yet; a final one costs nothing. Two calls at most: the
// canonical hash at Info.BlockNumber (one header) — reorged is true when it no longer matches
// Info.BlockHash, the caller must then discard the resolved facts (see ResetBridgeTx) — and, while
// it matches, the finality resolver for the bridge tx (no reported value: the tracker resolves the
// bridge from its receipt, not from the bridge service). Once final, Info.BlockFinalized is set
// and the origin is never checked again. Any error keeps tracking unchanged
func CheckOrigin(
	ctx context.Context, logger aggkitcommon.Logger, checkers ReorgCheckers, tracking *TrackingData,
) (updated *TrackingData, reorged bool) {
	tx := tracking.BridgeTx()
	if !tx.IsDone() || tx.Info.BlockFinalized || tracking.IsFinal() {
		return tracking, false
	}
	info := tx.Info
	id := tracking.ID()

	if info.BlockHash != (common.Hash{}) {
		current, err := checkers.OriginBlocks.CanonicalBlockHash(ctx, info.NetworkID, info.BlockNumber)
		if err != nil {
			logger.Debugf("%s: checking that origin block %d is still canonical: %v (kept, retried next tick)",
				id, info.BlockNumber, err)
			return tracking, false
		}
		if current != info.BlockHash {
			logger.Infof("%s: origin block %d changed hash (%s -> %s): the bridge was reorged out, re-resolving it",
				id, info.BlockNumber, info.BlockHash, current)
			return tracking, true
		}
	}

	return OriginFinality(ctx, logger, checkers, tracking), false
}

// OriginFinality is the finality half of CheckOrigin, without the canonical hash check: the
// engine calls it right after FindBridge resolved the bridge (its block hash was just read), so
// a bridge resolved on an already final block is published final at once
func OriginFinality(
	ctx context.Context, logger aggkitcommon.Logger, checkers ReorgCheckers, tracking *TrackingData,
) *TrackingData {
	tx := tracking.BridgeTx()
	if !tx.IsDone() || tx.Info.BlockFinalized {
		return tracking
	}
	info := tx.Info
	id := tracking.ID()
	final, err := checkers.Finality.IsFinal(ctx, info.NetworkID, id.TxHash, info.BlockNumber, nil)
	if err != nil {
		logger.Debugf("%s: origin block %d not proven final: %v", id, info.BlockNumber, err)
	}
	if !final {
		return tracking
	}
	return withOriginFinalized(tracking)
}

// ResetBridgeTx discards everything the tracker resolved for tracking's tx — Info and the whole
// step path — after its origin block was reorged out: the tracking is back to unresolved, with a
// fresh UnresolvedTimeout, and FindBridge resolves it again wherever the tx was re-included (or
// gives up on it as for any tx that is not mined). Stores drop the steps of a tx without Info
func ResetBridgeTx(tracking *TrackingData) *TrackingData {
	return NewTrackingData(tracking.ID(), TrackingBridgeTx{}, nil)
}

// ReverifySteps re-checks the completed steps of a resolved, non-final tracking that a reorg can
// undo, and moves it back to the earliest one found gone (that step reopened InProgress, every
// later one Pending). Only what the current position depends on is checked, cheapest first:
//
//   - StepClaimed seen (Done, or opened by the claim fallback): isClaimed() on the destination.
//     False moves it back to StepWaitingClaim (claimed -> readyToClaim), or to the earliest step
//     the claimed-bridge fallback skipped, if any: those were only inferred from the claim (->
//     pending, they are resolved again). True and Done: the
//     claim record (GET /bridge/v1/claims) is fetched again — the claim tx re-included in
//     another block replaces the stored one — and FinalizeClaim decides whether it is final.
//     A claim implies everything before it, so nothing else is checked then.
//   - At StepWaitingClaim (readyToClaim, including right after moving back from StepClaimed):
//     the Done StepWaitingGERInjection (one GET injected-l1-info-leaf) and
//     StepWaitingL1InfoLeafAvailable (one GET l1-info-tree-index), skipping the ones already
//     known final. A gone injection or leaf moves the tracking back there (-> pending). On a
//     route with no injection step (L2 -> L1) a leaf still there is also asked for its own
//     finality (one more GET injected-l1-info-leaf, network_id 0), until it is final.
//
// A source error (bridge service 503 while it resolves a reorg, RPC failure...) never moves
// anything: the checks stop there and run again on the next tick
func ReverifySteps(
	ctx context.Context, logger aggkitcommon.Logger, checkers ReorgCheckers, tracking *TrackingData, now time.Time,
) *TrackingData {
	steps := tracking.AllSteps()
	if !tracking.BridgeTx().IsDone() || steps == nil || tracking.IsFinal() {
		return tracking
	}
	claimedIdx := indexOfStep(steps, types.StepClaimed)
	waitIdx := indexOfStep(steps, types.StepWaitingClaim)
	if claimedIdx < 0 || waitIdx < 0 {
		return tracking
	}

	if steps[claimedIdx].Status != types.StepStatusPending {
		var movedBack bool
		tracking, movedBack = reverifyClaim(ctx, logger, checkers, tracking, claimedIdx, waitIdx, now)
		if !movedBack {
			return tracking
		}
	}
	if currentStepIndex(tracking.AllSteps()) != waitIdx {
		return tracking
	}
	return reverifyReadiness(ctx, logger, checkers, tracking, waitIdx, now)
}

// reverifyClaim is ReverifySteps for a tracking whose StepClaimed has been reached. movedBack
// reports whether the claim is gone and tracking was moved back to StepWaitingClaim
func reverifyClaim(
	ctx context.Context, logger aggkitcommon.Logger, checkers ReorgCheckers, tracking *TrackingData,
	claimedIdx, waitIdx int, now time.Time,
) (updated *TrackingData, movedBack bool) {
	id := tracking.ID()
	info := tracking.Info()

	claimed, err := checkers.ClaimChecker.IsClaimed(ctx, info)
	if err != nil {
		logger.Debugf("%s: re-checking the claim: %v (kept, retried next tick)", id, err)
		return tracking, false
	}
	if !claimed {
		// Steps the claimed-bridge fallback skipped were only inferred from the claim: with the
		// claim gone they are reopened from the earliest one, so their facts are resolved again
		backTo := waitIdx
		if skipped := firstSkipped(tracking.AllSteps(), waitIdx); skipped >= 0 {
			backTo = skipped
		}
		logger.Infof("%s: the claim is gone (isClaimed() is false on network %d): back to %s",
			id, info.DestinationNetwork, tracking.AllSteps()[backTo].Step)
		return rewindTo(tracking, backTo, now), true
	}

	stored := tracking.AllSteps()[claimedIdx]
	if stored.Status != types.StepStatusDone || stored.ResultClaim == nil {
		// still waiting for the claim record: the forward resolution (ClaimedResolver) fetches it
		return tracking, false
	}
	claim, err := checkers.Claims.ClaimFor(ctx, info)
	if err != nil {
		logger.Debugf("%s: re-fetching the claim record: %v (kept, retried next tick)", id, err)
		return tracking, false
	}
	if claim == nil {
		// isClaimed() says claimed but the bridge service does not list it (lagging behind, or
		// resolving a reorg of its own): nothing to prove final yet, keep the stored claim
		logger.Debugf("%s: claimed on-chain but the claim record is not indexed (anymore), kept", id)
		return tracking, false
	}
	// a re-inclusion at the same height lands in another block, whose timestamp may differ: the
	// timestamp is compared too so the step dates never keep the orphaned block's time
	if claim.ClaimTx != stored.ResultClaim.ClaimTx || claim.BlockNumber != stored.ResultClaim.BlockNumber ||
		claim.BlockTimestamp != stored.ResultClaim.BlockTimestamp {
		logger.Infof("%s: the claim moved from tx %s in block %d (ts %d) to tx %s in block %d (ts %d)",
			id, stored.ResultClaim.ClaimTx, stored.ResultClaim.BlockNumber, stored.ResultClaim.BlockTimestamp,
			claim.ClaimTx, claim.BlockNumber, claim.BlockTimestamp)
		tracking = replaceClaim(tracking, claimedIdx, claim)
	}
	return finalizeClaim(ctx, logger, checkers.Finality, tracking, claimedIdx, claim.ReportedFinalized), false
}

// firstSkipped returns the index of the first StepStatusSkipped step at or before upTo, or -1
func firstSkipped(steps []BridgeStepPath, upTo int) int {
	for i := 0; i <= upTo && i < len(steps); i++ {
		if steps[i].Status == types.StepStatusSkipped {
			return i
		}
	}
	return -1
}

// reverifyReadiness is ReverifySteps for a tracking at StepWaitingClaim: it re-checks the Done,
// not yet final, injection and leaf steps before waitIdx in path order and moves the tracking
// back to the first one found gone. Steps found final are marked so (BlockFinalized) and never
// checked again; a final injection also finalizes the L1 info leaf step after it, since the
// injected leaf's L1 block being final makes every earlier leaf final too. Where the path has no
// injection step (L2 -> L1) the leaf is asked for its own finality instead (see l1InfoLeafFinal)
func reverifyReadiness(
	ctx context.Context, logger aggkitcommon.Logger, checkers ReorgCheckers, tracking *TrackingData,
	waitIdx int, now time.Time,
) *TrackingData {
	id := tracking.ID()
	info := tracking.Info()
	steps := append([]BridgeStepPath(nil), tracking.AllSteps()...)
	changed := false
	updated := func() *TrackingData {
		if !changed {
			return tracking
		}
		return NewTrackingData(id, tracking.BridgeTx(), steps)
	}

	for i := 0; i < waitIdx; i++ {
		step := steps[i]
		if step.Status != types.StepStatusDone || step.BlockFinalized {
			continue
		}
		var present, final bool
		var err error
		switch step.Step {
		case types.StepWaitingGERInjection:
			leafIndex, idxErr := injectionLeafIndex(steps, i)
			if idxErr != nil {
				continue // inconsistent read: there is no leaf to re-check
			}
			present, final, err = checkers.InjectedGERs.InjectedGERPresent(ctx, info, leafIndex)
		case types.StepWaitingL1InfoLeafAvailable:
			var index *uint32
			index, err = checkers.Leaves.L1InfoTreeIndexForBridge(ctx, info)
			present = index != nil
			if err == nil && present && indexOfStep(steps, types.StepWaitingGERInjection) < 0 {
				final = l1InfoLeafFinal(ctx, logger, checkers, tracking, *index)
			}
		default:
			continue
		}
		if err != nil {
			logger.Debugf("%s: re-checking %s: %v (kept, retried next tick)", id, step.Step, err)
			return updated()
		}
		if !present {
			logger.Infof("%s: %s is gone (reorg): back to it", id, step.Step)
			return rewindTo(NewTrackingData(id, tracking.BridgeTx(), steps), i, now)
		}
		if final {
			changed = true
			steps[i].BlockFinalized = true
			if step.Step == types.StepWaitingGERInjection {
				if j := indexOfStep(steps, types.StepWaitingL1InfoLeafAvailable); j > i && steps[j].Status == types.StepStatusDone {
					steps[j].BlockFinalized = true
				}
			}
		}
	}
	return updated()
}

// l1InfoLeafFinal reports whether the L1 info tree leaf leafIndex, just found still covering
// tracking's bridge, sits in a final L1 block. It is only asked on the paths with no GER
// injection step (L2 -> L1): elsewhere the final injection settles the leaf step too. An error
// only means "could not tell this time": the leaf is still there, the step is kept not final and
// asked again on the next tick
func l1InfoLeafFinal(
	ctx context.Context, logger aggkitcommon.Logger, checkers ReorgCheckers, tracking *TrackingData, leafIndex uint32,
) bool {
	final, err := checkers.InjectedGERs.L1InfoLeafFinalized(ctx, tracking.Info(), leafIndex)
	if err != nil {
		logger.Debugf("%s: L1 info leaf %d not proven final: %v (kept, asked again next tick)",
			tracking.ID(), leafIndex, err)
		return false
	}
	return final
}

// FinalizeClaim closes a tracking whose StepClaimed is Done with a claim not known final yet: it
// asks the finality resolver about the stored claim (destination network, claim tx and block,
// with the block_finalized the bridge service reported alongside it, if still at hand) and, once
// final, marks the step (claim_status claimed_finalized, TrackingStatus Finished: the tracking
// leaves the active list and is never re-evaluated again). The origin bridge is marked final too
// (BridgeInfo.BlockFinalized): a final claim implies every fact it stands on. The engine calls it
// right after the claim is first found; later ticks go through ReverifySteps, which re-fetches
// the claim record first. A resolver error keeps the tracking unchanged
func FinalizeClaim(
	ctx context.Context, logger aggkitcommon.Logger, checkers ReorgCheckers, tracking *TrackingData,
) *TrackingData {
	claimedIdx := indexOfStep(tracking.AllSteps(), types.StepClaimed)
	if claimedIdx < 0 {
		return tracking
	}
	var reported *bool
	if claim := tracking.AllSteps()[claimedIdx].ResultClaim; claim != nil {
		reported = claim.ReportedFinalized
	}
	return finalizeClaim(ctx, logger, checkers.Finality, tracking, claimedIdx, reported)
}

// finalizeClaim is FinalizeClaim with the reported block_finalized given explicitly
func finalizeClaim(
	ctx context.Context, logger aggkitcommon.Logger, finality FinalityResolver, tracking *TrackingData,
	claimedIdx int, reported *bool,
) *TrackingData {
	step := tracking.AllSteps()[claimedIdx]
	if step.Status != types.StepStatusDone || step.BlockFinalized || step.ResultClaim == nil {
		return tracking
	}
	id := tracking.ID()
	info := tracking.Info()
	claim := step.ResultClaim

	final, err := finality.IsFinal(ctx, info.DestinationNetwork, claim.ClaimTx, claim.BlockNumber, reported)
	if err != nil {
		logger.Debugf("%s: claim tx %s in block %d not proven final: %v", id, claim.ClaimTx, claim.BlockNumber, err)
	}
	if !final {
		return tracking
	}
	logger.Infof("%s: claim tx %s in block %d of network %d is final: tracking closed",
		id, claim.ClaimTx, claim.BlockNumber, info.DestinationNetwork)

	steps := append([]BridgeStepPath(nil), tracking.AllSteps()...)
	steps[claimedIdx].BlockFinalized = true
	tracking = NewTrackingData(id, tracking.BridgeTx(), steps)
	if !info.BlockFinalized {
		tracking = withOriginFinalized(tracking)
	}
	return tracking
}

// replaceClaim stores claim as StepClaimed's result in place of the one it had (the claim tx
// re-included in another block), with its own block timestamp as the step's dates, not final
func replaceClaim(tracking *TrackingData, claimedIdx int, claim *types.ClaimResult) *TrackingData {
	steps := append([]BridgeStepPath(nil), tracking.AllSteps()...)
	step := steps[claimedIdx]
	step.SetResult(claim)
	step.BlockFinalized = false
	if ts := blockTime(claim.BlockTimestamp); ts != nil {
		step.StartDate = ts
		endDate := *ts
		step.EndDate = &endDate
	}
	steps[claimedIdx] = step
	return NewTrackingData(tracking.ID(), tracking.BridgeTx(), steps)
}

// rewindTo moves tracking back to the step at idx: that step is reopened InProgress (keeping
// the StartDate it was first opened with, or now if it never had one) with its result, error and
// finality cleared, and every later step is back to Pending as if never reached. Steps before idx
// are left as they are
func rewindTo(tracking *TrackingData, idx int, now time.Time) *TrackingData {
	steps := append([]BridgeStepPath(nil), tracking.AllSteps()...)
	for i := idx; i < len(steps); i++ {
		prev := steps[i]
		if i > idx {
			steps[i] = BridgeStepPath{
				Step: prev.Step, Status: types.StepStatusPending, ExpectedDuration: prev.ExpectedDuration,
			}
			continue
		}
		reopened := BridgeStepPath{
			Step: prev.Step, Status: types.StepStatusInProgress, StartDate: prev.StartDate,
			ExpectedDuration: prev.ExpectedDuration,
		}
		if reopened.StartDate == nil {
			startDate := now
			reopened.StartDate = &startDate
		}
		steps[i] = reopened
	}
	return NewTrackingData(tracking.ID(), tracking.BridgeTx(), steps)
}

// withOriginFinalized returns tracking with its origin bridge marked final (Info is copied, never
// modified in place: the previous snapshot may still be held by readers)
func withOriginFinalized(tracking *TrackingData) *TrackingData {
	tx := tracking.BridgeTx()
	info := *tx.Info
	info.BlockFinalized = true
	tx.Info = &info
	return NewTrackingData(tracking.ID(), tx, tracking.AllSteps())
}

// StepsForTx returns the steps a store keeps for a tracking whose tx facts are tx: none while
// the tx is not resolved (Info nil), steps otherwise. A path only exists for a resolved bridge,
// and ResetBridgeTx relies on it to drop the path of a bridge reorged out of its origin block
func StepsForTx(tx TrackingBridgeTx, steps []BridgeStepPath) []BridgeStepPath {
	if tx.Info == nil {
		return nil
	}
	return steps
}
