package domain

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgetracker/types"
)

// CertificateResolver resolves the three certificate-tracking steps of an L2-originated bridge
// (StepPendingInclusion, StepCertificatePending, StepCertificateProcessing) from a single
// Certificate fact: which of the three is current depends on the certificate's status, and a
// bridge can skip straight past one or two of them the first time its certificate is observed
// (e.g. already Settled without ever being seen Pending). ResolveSteps calls Resolve again for
// each waypoint crossed that same call (a shared instance registered for all three steps — see
// stepResolvers), so the certificate is cached after the first fetch instead of being queried
// once per waypoint
type CertificateResolver struct {
	Facts BridgeFacts

	fetched  bool
	cert     *types.CertificateData
	fetchErr error
}

// Resolve implements StepResolver. idx may land on any of the three certificate steps
// depending on what a previous tick last observed; which of them is met by the (possibly
// cached) certificate depends on its status and on idx itself, since a Settled or otherwise
// past-Pending certificate completes every waypoint from idx up to whichever step it reaches
func (r *CertificateResolver) Resolve(ctx context.Context, tracking *TrackingData, idx int) (any, error) {
	if !r.fetched {
		r.cert, r.fetchErr = r.Facts.Certificate(ctx, tracking.Info())
		r.fetched = true
	}
	if r.fetchErr != nil {
		return nil, fmt.Errorf("certificate: %w", r.fetchErr)
	}
	if r.cert == nil {
		return nil, ErrStepPending
	}

	step := tracking.AllSteps()[idx].Step
	switch {
	case r.cert.Status.IsSettled():
		if step == types.StepCertificateProcessing {
			return r.cert, nil
		}
		return nil, nil // PendingInclusion/CertificatePending: met too, no result of their own
	case r.cert.Status == agglayertypes.Pending:
		if step == types.StepCertificatePending {
			return nil, ErrStepPending // arrived, still awaiting its first status change
		}
		return nil, nil // PendingInclusion: met, move on to CertificatePending
	default: // Proven, Candidate or InError: still processing, not settled yet
		if step == types.StepCertificateProcessing {
			return nil, ErrStepPending // arrived, still not settled
		}
		return nil, nil // PendingInclusion or CertificatePending: met, move on
	}
}
