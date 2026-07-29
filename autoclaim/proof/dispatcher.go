package proof

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/autoclaim/types"
)

// SourceAwarePreparer dispatches proof preparation between the L1-origin Preparer and the
// rollup-origin RollupPreparer based on a request's Bridge.SourceNetwork. Both preparers implement
// types.ProofPreparer and share the same Result/ClaimProof shape, so the dispatch is mechanical.
type SourceAwarePreparer struct {
	l1Preparer     types.ProofPreparer
	rollupPreparer types.ProofPreparer
}

var _ types.ProofPreparer = (*SourceAwarePreparer)(nil)

// NewSourceAwarePreparer builds a proof preparer that routes L1-origin requests
// (Bridge.SourceNetwork == types.L1OriginNetwork) to l1Preparer and rollup-origin requests
// (SourceNetwork != 0) to rollupPreparer.
func NewSourceAwarePreparer(l1Preparer, rollupPreparer types.ProofPreparer) (*SourceAwarePreparer, error) {
	if l1Preparer == nil {
		return nil, fmt.Errorf("autoclaim source-aware proof preparer L1 preparer is nil")
	}
	if rollupPreparer == nil {
		return nil, fmt.Errorf("autoclaim source-aware proof preparer rollup preparer is nil")
	}

	return &SourceAwarePreparer{
		l1Preparer:     l1Preparer,
		rollupPreparer: rollupPreparer,
	}, nil
}

// PrepareProof implements types.ProofPreparer, dispatching on request.Bridge.SourceNetwork.
func (p *SourceAwarePreparer) PrepareProof(
	ctx context.Context, request types.AutoClaimRequest,
) (*types.ClaimProof, error) {
	if request.Bridge.SourceNetwork == types.L1OriginNetwork {
		return p.l1Preparer.PrepareProof(ctx, request)
	}
	return p.rollupPreparer.PrepareProof(ctx, request)
}
