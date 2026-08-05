package domain

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

// ErrLeafIndexNotResolved means the settlement tx is confirmed but its GER has not been
// resolved to an L1 info tree leaf index yet (only reached when UpdateL1InfoTreeV2 did not fire
// — see WaitL1SettledGERResolver): the same "not ready" family as ErrStepPending (errors.Is
// matches both), but carries the settlement evidence gathered so far as its Result
var ErrLeafIndexNotResolved = fmt.Errorf("settlement GER not resolved to a leaf index yet: %w", ErrStepPending)

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

// WaitL1SettledGERResolver resolves StepWaitL1SettledGER: whether the certificate's settlement
// tx has been confirmed on L1 (see SettlementSource.SettlementGERUpdate) and its GER resolved to
// a concrete L1 info tree leaf index — straight from the settlement tx's UpdateL1InfoTreeV2 event
// when present, or with one extra lookup (L1InfoTreeIndexSource.L1InfoTreeIndexForGER) when it is
// not. Only ever the current step for L2-originated paths, since ExpectedPath omits it for L1ToL2
type WaitL1SettledGERResolver struct {
	settlement SettlementSource
	gerIndex   L1InfoTreeIndexSource
}

// NewWaitL1SettledGERResolver returns a WaitL1SettledGERResolver reading settlement evidence
// through settlement and the L1 info tree leaf index through gerIndex
func NewWaitL1SettledGERResolver(
	settlement SettlementSource, gerIndex L1InfoTreeIndexSource,
) *WaitL1SettledGERResolver {
	return &WaitL1SettledGERResolver{settlement: settlement, gerIndex: gerIndex}
}

// Resolve implements StepResolver. The settlement tx hash is read off the already-completed
// StepCertificatePending's Result (see CertificatePendingResolver) rather than re-querying
// CertificateSource: unlike PendingInclusionResolver/CertificatePendingResolver, which each
// still need their own fresh read because their own milestone is what settling changes, by the
// time this step is current the certificate has already settled and its data is sitting right
// there, one step back
func (r *WaitL1SettledGERResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, _ int,
) (any, error) {
	steps := tracking.AllSteps()
	idx := indexOfStep(steps, types.StepCertificatePending)
	if idx < 0 {
		return nil, ErrStepPending
	}
	cert := steps[idx].ResultCertificateData
	if cert == nil || cert.SettlementTxHash == nil {
		// the settlement tx hash may lag a tick behind the certificate turning Settled (see
		// agglayer/types.CertificateHeader.SettlementTxHash), so this is a transient wait, not
		// an inconsistent state
		return nil, ErrStepPending
	}

	settlement, err := r.settlement.SettlementGERUpdate(ctx, tracking.Info(), *cert.SettlementTxHash)
	if err != nil {
		return nil, fmt.Errorf("settlement GER update: %w", err)
	}
	if settlement == nil {
		return nil, ErrStepPending
	}
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
