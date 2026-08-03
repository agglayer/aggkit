package domain

import (
	"context"
	"fmt"
)

// ErrCertificateNotSettled means the bridge already has a certificate but it has not settled
// yet: the same "not ready" family as ErrStepPending (errors.Is matches both), but carries the
// certificate's current status as its Result so clients can see it progress while they wait,
// instead of only once it settles
var ErrCertificateNotSettled = fmt.Errorf("certificate not settled yet: %w", ErrStepPending)

// CertificatePendingResolver resolves StepCertificatePending: covers every status the
// certificate goes through — Pending, Proven, Candidate or InError all park here, only its
// Result changes — until it settles, the only transition that moves the bridge on
type CertificatePendingResolver struct{}

// Resolve implements StepResolver
func (r CertificatePendingResolver) Resolve(
	ctx context.Context, facts BridgeFacts, tracking *TrackingData, _ int,
) (any, error) {
	cert, err := facts.Certificate(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	if cert == nil {
		return nil, ErrStepPending
	}

	if cert.Status.IsSettled() {
		return cert, nil
	}
	return cert, ErrCertificateNotSettled // still awaiting settlement
}
