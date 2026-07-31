package domain

import (
	"context"
	"fmt"
)

// PendingInclusionResolver resolves StepPendingInclusion: whether the bridge has been included
// in any certificate yet. Only ever the current step of an L2-originated path, since
// ExpectedPath omits it otherwise. Met the moment a certificate exists, regardless of its
// status, with its CertificateID as Result — see CertificatePendingResolver for what happens
// from there
type PendingInclusionResolver struct{}

// Resolve implements StepResolver
func (r PendingInclusionResolver) Resolve(
	ctx context.Context, facts BridgeFacts, tracking *TrackingData, _ int,
) (any, error) {
	cert, err := facts.Certificate(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	if cert == nil {
		return nil, ErrStepPending
	}

	return cert.CertificateID, nil // met: the certificate that includes the bridge, move on
}
