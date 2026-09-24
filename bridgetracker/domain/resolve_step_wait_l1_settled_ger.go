package domain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

var (
	// ErrLeafIndexNotResolved means the settlement tx is confirmed but its GER has not been
	// resolved to an L1 info tree leaf index yet (only reached when UpdateL1InfoTreeV2 did not fire
	// — see WaitL1SettledGERResolver): the same "not ready" family as ErrStepPending (errors.Is
	// matches both), but carries the settlement evidence gathered so far as its Result
	ErrLeafIndexNotResolved = fmt.Errorf("settlement GER not resolved to a leaf index yet: %w", ErrStepPending)

	ErrBadSettlementTx = Permanent(errors.New("settlement tx receipt does not carry required events"))
)

// SettlementSource is the driven port to the L1 evidence a certificate's settlement produces
type SettlementSource interface {
	// SettlementGERUpdate returns the evidence, read off settlementTxHash's L1 receipt, that
	// the certificate's settlement propagated to the L1 Global Exit Root, or nil if that
	// evidence is not there yet (the tx not final, or not carrying the required events yet).
	// Only queried for L2-originated bridges once their certificate has settled
	SettlementGERUpdate(
		ctx context.Context, bridge *BridgeInfo, settlementTxHash common.Hash,
	) (*types.L1SettledGERResult, error)
}

// L1InfoTreeIndexSource is the driven port resolving a GER to its L1 info tree leaf index
type L1InfoTreeIndexSource interface {
	// L1InfoTreeIndexForGER resolves the L1 info tree leaf index ger (produced by bridge's
	// certificate settlement, see types.L1SettledGERResult) landed at, or nil if the L1 info
	// tree has not caught up with it yet. Only queried when the settlement tx did not emit
	// UpdateL1InfoTreeV2 (which already carries the index)
	L1InfoTreeIndexForGER(ctx context.Context, bridge *BridgeInfo, ger common.Hash) (*uint32, error)
}

// SettlementHistorySource is the driven port to a bridge's origin network's full L1 settlement
// history, consulted by WaitL1SettledGERResolver only when the certificate
// CertificatePendingResolver resolved is known not to be the one that actually first included
// the bridge (see issue #1817): agglayer's CertificateFor always reports the most recently
// settled certificate that covers a bridge, never the earliest one, so once a bridge has been
// included for a while, the certificate the tracker follows drifts further and further from the
// one that actually did it, making WaitL1SettledGER wait on a GER far more recent than
// necessary (see WaitL1SettledGERResolver.exactSettlementTxHash)
type SettlementHistorySource interface {
	// Covers reports whether ler already includes bridge's deposit (append-only local exit tree
	// semantics: true once ler's own deposit-count position is at or past bridge's)
	Covers(ctx context.Context, bridge *BridgeInfo, ler common.Hash) (bool, error)

	// EarliestSettlementTxCovering walks bridge's origin network's L1 settlement history
	// backwards from fromBlock -- a settlement already known to cover bridge, typically the
	// currently-tracked certificate's own settlement block -- for the tx hash of the earliest
	// settlement whose new LER already covers bridge, i.e. the one right after the last
	// settlement that does not cover it yet. resume, when non-nil, continues a search an earlier
	// call could not finish within its own engine tick (see types.SettlementSearchProgress)
	// instead of restarting it from fromBlock; pass nil on the very first attempt. Returns
	// (nil, nil, nil) if the search has fully resolved without ever finding a covering
	// settlement (transient -- the caller only calls this once fromBlock's certificate is known
	// to cover bridge, so this only happens racing a fresh read; retried by the engine), or
	// (nil, progress, nil) if the search needs at least one more tick to finish -- the caller
	// persists progress and passes it back as resume next time. progress can also come back
	// non-nil alongside a non-nil error: a transient failure mid-scan (e.g. an RPC hiccup) still
	// reports the cursor built up so far, so the caller persists it even though this same call
	// also needs to be retried, instead of losing that progress to the very failure the resumable
	// search exists to be resilient against
	EarliestSettlementTxCovering(
		ctx context.Context, bridge *BridgeInfo, fromBlock uint64, resume *types.SettlementSearchProgress,
	) (*common.Hash, *types.SettlementSearchProgress, error)
}

// WaitL1SettledGERResolver resolves StepWaitL1SettledGER: whether the certificate's settlement
// tx has been confirmed on L1 (see SettlementSource.SettlementGERUpdate) and its GER resolved to
// a concrete L1 info tree leaf index — straight from the settlement tx's UpdateL1InfoTreeV2 event
// when present, or with one extra lookup (L1InfoTreeIndexSource.L1InfoTreeIndexForGER) when it is
// not. Only ever the current step for L2-originated paths, since ExpectedPath omits it for L1ToL2
type WaitL1SettledGERResolver struct {
	settlement SettlementSource
	gerIndex   L1InfoTreeIndexSource
	history    SettlementHistorySource
}

// NewWaitL1SettledGERResolver returns a WaitL1SettledGERResolver reading settlement evidence
// through settlement, the L1 info tree leaf index through gerIndex, and — only when the
// certificate being tracked turns out not to be the exact one that first included the bridge —
// the network's earlier settlement history through history
func NewWaitL1SettledGERResolver(
	settlement SettlementSource, gerIndex L1InfoTreeIndexSource, history SettlementHistorySource,
) *WaitL1SettledGERResolver {
	return &WaitL1SettledGERResolver{settlement: settlement, gerIndex: gerIndex, history: history}
}

// Resolve implements StepResolver. The settlement tx hash is read off the already-completed
// StepCertificatePending's Result (see CertificatePendingResolver) rather than re-querying
// CertificateSource: unlike PendingInclusionResolver/CertificatePendingResolver, which each
// still need their own fresh read because their own milestone is what settling changes, by the
// time this step is current the certificate has already settled and its data is sitting right
// there, one step back. exactSettlementTxHash may swap that tx hash for an earlier, more exact
// one first (see its own doc, and issue #1817)
func (r *WaitL1SettledGERResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, idx int,
) (any, error) {
	steps := tracking.AllSteps()
	certIdx := indexOfStep(steps, types.StepCertificatePending)
	if certIdx < 0 {
		return nil, ErrStepPending
	}
	cert := steps[certIdx].ResultCertificateData
	if cert == nil || cert.SettlementTxHash == nil {
		// the settlement tx hash may lag a tick behind the certificate turning Settled (see
		// agglayer/types.CertificateHeader.SettlementTxHash), so this is a transient wait, not
		// an inconsistent state
		return nil, ErrStepPending
	}

	// steps[idx] is this same step's own prior Result: a *types.SettlementSearchProgress left
	// over from an earlier call whose backwards search did not finish within its own engine
	// tick (see exactSettlementTxHash's own doc); nil on the first attempt, or once resolved
	resume, _ := steps[idx].Result().(*types.SettlementSearchProgress)

	settlementTxHash := *cert.SettlementTxHash
	exact, progress, err := r.exactSettlementTxHash(ctx, tracking, steps, cert, resume)
	if progress != nil {
		// persisted regardless of err: a transient failure mid-scan (e.g. an RPC hiccup) must
		// not discard search progress just because it also needs to be retried -- err is either
		// ErrStepPending (ran out of time this tick, not a failure) or a real error (recorded as
		// this step's transient error, but the cursor survives it either way)
		return progress, err
	}
	if err != nil {
		return nil, err
	}
	if exact != nil {
		settlementTxHash = *exact
	}

	settlement, err := r.settlement.SettlementGERUpdate(ctx, tracking.Info(), settlementTxHash)
	if err != nil {
		return nil, fmt.Errorf("settlement GER update: %w", err)
	}
	if settlement == nil {
		return nil, ErrStepPending
	}
	// exact is only non-nil once exactSettlementTxHash actually swapped in an earlier settlement
	// than cert's own (see its own doc) -- StartDate reads this back to pin the step's own start
	// to its end instead of the chained (later) value in that case
	settlement.UsedEarlierSettlement = exact != nil
	if settlement.L1InfoTreeIndex != nil {
		return settlement, nil // UpdateL1InfoTreeV2 already gave us the leaf index
	}

	leafIndex, err := r.gerIndex.L1InfoTreeIndexForGER(ctx, tracking.Info(), settlement.GER)
	if err != nil {
		return nil, fmt.Errorf("L1 info tree index for GER: %w", err)
	}
	if leafIndex == nil {
		return settlement, ErrLeafIndexNotResolved // settlement confirmed, still resolving the leaf
	}

	settlement.L1InfoTreeIndex = leafIndex
	return settlement, nil
}

// StartDate has no deterministic value of its own in the normal path: this step's beginning is
// "the previous step (StepCertificatePending) just finished" (chained by UpdateStep). The
// exception is when result.UsedEarlierSettlement is true (see its own doc and issue #1817):
// StepCertificatePending's own EndDate is the tracked (later) certificate's settlement, which
// can land after the earlier, already-covering settlement this step's own EndDate reports --
// pinning StartDate to that same EndDate here keeps the step's duration from reading negative,
// instead of claiming a beginning later than its own end
func (r *WaitL1SettledGERResolver) StartDate(_ *BridgeInfo, result any) *time.Time {
	settlement, ok := result.(*types.L1SettledGERResult)
	if !ok || !settlement.UsedEarlierSettlement {
		return nil
	}
	return blockTime(settlement.SettlementBlockTimestamp)
}

// EndDate returns the settlement tx's own VerifyBatchesTrustedAggregator block timestamp — the
// event that confirms this tx is a genuine certificate settlement, effectively the same instant
// StepCertificatePending's own EndDate locates (see L1SettledGERResult's own doc)
func (r *WaitL1SettledGERResolver) EndDate(result any) *time.Time {
	settlement, ok := result.(*types.L1SettledGERResult)
	if !ok {
		return nil
	}
	return blockTime(settlement.SettlementBlockTimestamp)
}

// Warning never has anything to report: EndDate's own value either exists or falls back to now,
// with no partial-failure mode of its own worth explaining
func (r *WaitL1SettledGERResolver) Warning(_ any) *string {
	return nil
}

// exactSettlementTxHash reports whether cert -- the certificate CertificatePendingResolver
// resolved -- is definitely not the one that first included the bridge, and if so, returns the
// exact earlier one's settlement tx hash instead (see issue #1817).
//
// StepPendingInclusion's PreviousLER is the LER right before cert; if it already covers the
// bridge, some earlier certificate -- settled before cert, possibly long before it -- already
// included the bridge, and cert is simply whichever one agglayer happened to report as "latest
// settled that covers" by the time CertificatePendingResolver asked. Waiting for cert's own GER
// to be injected would then wait far longer than necessary, since an earlier, already-injected
// GER would have been enough (the whole point of #1817).
//
// When PreviousLER does not cover the bridge, cert is the network's first certificate to include
// it, so the normal path applies: this returns (nil, nil, nil), leaving the resolver's own
// cert.SettlementTxHash in charge.
//
// resume carries a backwards search's own progress across engine ticks (see
// types.SettlementSearchProgress): EarliestSettlementTxCovering's L1-log fallback can need far
// more than one tick to reach genesis or the covering/non-covering transition, so when it
// returns a non-nil progress instead of resolving, this returns that same progress as its own
// second value for Resolve to persist as the step's Result and hand back as resume next time,
// instead of restarting the search from cert.BlockNumber every tick
func (r *WaitL1SettledGERResolver) exactSettlementTxHash(
	ctx context.Context, tracking *TrackingData, steps []BridgeStepPath, cert *types.CertificateData,
	resume *types.SettlementSearchProgress,
) (*common.Hash, *types.SettlementSearchProgress, error) {
	pendingIdx := indexOfStep(steps, types.StepPendingInclusion)
	if pendingIdx < 0 {
		return nil, nil, nil
	}
	pending := steps[pendingIdx].ResultPendingInclusion
	if pending == nil || pending.PreviousLER == nil {
		return nil, nil, nil // the network's first-ever certificate: nothing earlier can cover it
	}

	covers, err := r.history.Covers(ctx, tracking.Info(), *pending.PreviousLER)
	if err != nil {
		return nil, nil, fmt.Errorf("checking previous LER coverage: %w", err)
	}
	if !covers {
		return nil, nil, nil // normal path: cert is the one that first covered the bridge
	}

	if cert.BlockNumber == nil {
		// cert's own settlement block is not visible on L1 yet, nothing to anchor the backwards
		// search on -- transient, same as the settlement tx hash lagging a tick behind Settled
		return nil, nil, ErrStepPending
	}
	exact, progress, err := r.history.EarliestSettlementTxCovering(ctx, tracking.Info(), *cert.BlockNumber, resume)
	if progress != nil {
		// progress survives err: a transient failure mid-scan (e.g. an RPC hiccup) must not
		// discard how far the search already got just because it also needs to be retried
		if err != nil {
			return nil, progress, fmt.Errorf("finding earliest settlement covering the bridge: %w", err)
		}
		return nil, progress, ErrStepPending // search needs at least one more tick
	}
	if err != nil {
		return nil, nil, fmt.Errorf("finding earliest settlement covering the bridge: %w", err)
	}
	if exact == nil {
		return nil, nil, ErrStepPending // search resolved without ever finding a covering settlement: transient
	}
	return exact, nil, nil
}
