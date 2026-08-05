package domain

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// PendingInclusionResolver resolves StepPendingInclusion: whether the bridge has been included
// in any certificate yet. Only ever the current step of an L2-originated path, since
// ExpectedPath omits it otherwise. Met the moment a certificate exists, regardless of its
// status, with a *types.PendingInclusionResult as Result — see CertificatePendingResolver for
// what happens from there
type PendingInclusionResolver struct {
	port CertificateSource
}

// NewPendingInclusionResolver returns a PendingInclusionResolver reading certificates through port
func NewPendingInclusionResolver(port CertificateSource) *PendingInclusionResolver {
	return &PendingInclusionResolver{port: port}
}

// Resolve implements StepResolver
func (r *PendingInclusionResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, _ int,
) (any, error) {
	cert, err := r.port.CertificateFor(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	if cert == nil {
		return nil, ErrStepPending
	}

	return &types.PendingInclusionResult{
		CertificateID: cert.CertificateID,
		NewLER:        cert.NewLocalExitRoot,
		PreviousLER:   cert.PreviousLocalExitRoot,
	}, nil // met: the certificate that includes the bridge, move on
}
