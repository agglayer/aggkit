package domain

import (
	"context"
	"fmt"

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
