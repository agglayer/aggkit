package domain

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// ErrCertificateNotSettled means the bridge already has a certificate but it has not settled
// yet — or it has, but its settlement tx is not visible on L1 yet (see CertificateSource.
// settlementBlockInfo, which can lag a tick behind the certificate itself turning Settled): the
// same "not ready" family as ErrStepPending (errors.Is matches both), but carries the
// certificate's current status as its Result so clients can see it progress while they wait,
// instead of only once it settles
var ErrCertificateNotSettled = fmt.Errorf("certificate not settled yet: %w", ErrStepPending)

// CertificateSource is the driven port to the agglayer certificate covering a bridge, shared by
// every resolver that needs to know which certificate includes a bridge and in which state it is
// (PendingInclusionResolver, CertificatePendingResolver)
type CertificateSource interface {
	// CertificateFor returns the certificate that includes bridge, or nil if it is not part of
	// any certificate yet
	CertificateFor(ctx context.Context, bridge *BridgeInfo) (*types.CertificateInclusionData, error)
}

// CertificatePendingResolver resolves StepCertificatePending: covers every status the
// certificate goes through — Pending, Proven, Candidate or InError all park here, only its
// Result changes — until it settles AND its settlement tx's block is visible on L1
// (CertificateData.BlockNumber/BlockTimestamp), the transition that moves the bridge on
type CertificatePendingResolver struct {
	port CertificateSource
}

// NewCertificatePendingResolver returns a CertificatePendingResolver reading certificates through port
func NewCertificatePendingResolver(port CertificateSource) *CertificatePendingResolver {
	return &CertificatePendingResolver{port: port}
}

// Resolve implements StepResolver
func (r *CertificatePendingResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, _ int,
) (any, error) {
	cert, err := r.port.CertificateFor(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	if cert == nil {
		return nil, ErrStepPending
	}

	if cert.Status.IsSettled() && cert.BlockNumber != nil {
		return &cert.CertificateData, nil
	}
	return &cert.CertificateData, ErrCertificateNotSettled // still awaiting settlement, or its L1 block
}

// StartDate has no deterministic value of its own: this step's beginning is always "the
// previous step just finished" (chained by UpdateStep)
func (r *CertificatePendingResolver) StartDate(_ *BridgeInfo, _ any) *time.Time {
	return nil
}

// EndDate returns the settlement tx's own L1 block timestamp, once known (CertificateData.
// BlockTimestamp, only set once the certificate is Settled and its tx receipt visible on L1 —
// see CertificateData's own doc)
func (r *CertificatePendingResolver) EndDate(result any) *time.Time {
	cert, ok := result.(*types.CertificateData)
	if !ok {
		return nil
	}
	return blockTimePtr(cert.BlockTimestamp)
}

// Warning never has anything to report: EndDate's own value either exists or falls back to now,
// with no partial-failure mode of its own worth explaining
func (r *CertificatePendingResolver) Warning(_ any) *string {
	return nil
}
