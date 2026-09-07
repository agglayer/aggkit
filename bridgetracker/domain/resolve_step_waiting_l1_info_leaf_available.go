package domain

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// L1InfoLeafAvailableSource is the driven port to the L1 info tree index for a bridge's own
// origin deposit, as resolved by the bridge-service instance that will build the claim proof
// for it
type L1InfoLeafAvailableSource interface {
	// L1InfoTreeIndexForBridge returns the L1 info tree leaf index covering bridge's origin
	// deposit — GET /bridge/v1/l1-info-tree-index, queried against the proof-building
	// instance: the origin network's own bridge-service instance (see
	// docs/bridge_service.md's L2->L2 flow, where this same endpoint on the origin is what
	// bridge_getProof is later called against), or, when the origin is mainnet — which has no
	// bridge-service deployment of its own — the destination's instead — or nil if that
	// instance's own L1 info tree sync has not caught up to this deposit yet. Only queried by
	// StepWaitingL1InfoLeafAvailable
	L1InfoTreeIndexForBridge(ctx context.Context, bridge *BridgeInfo) (*uint32, error)
}

// WaitingL1InfoLeafAvailableResolver resolves StepWaitingL1InfoLeafAvailable: whether the
// bridge-service instance that will actually build the claim proof — the origin network's own
// instance — has its own L1 info tree sync caught up to this bridge's deposit yet, per GET
// /bridge/v1/l1-info-tree-index. Always the current step right before StepWaitingClaim, on
// every route: that sync can lag behind whatever this tracker itself uses elsewhere
// (StepWaitL1SettledGER's own L1 RPC scan, StepWaitingGERInjection's destination-side injection
// check), so a bridge can look claimable per those checks yet still fail to claim because the
// proof-building instance cannot produce a proof for it yet — this is why it is never skipped
// or inferred from a sibling step, on any route (#1823)
type WaitingL1InfoLeafAvailableResolver struct {
	port L1InfoLeafAvailableSource
}

// NewWaitingL1InfoLeafAvailableResolver returns a WaitingL1InfoLeafAvailableResolver checking
// the proof-building instance's L1 info tree index through port
func NewWaitingL1InfoLeafAvailableResolver(port L1InfoLeafAvailableSource) *WaitingL1InfoLeafAvailableResolver {
	return &WaitingL1InfoLeafAvailableResolver{port: port}
}

// Resolve implements StepResolver
func (r *WaitingL1InfoLeafAvailableResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, _ int,
) (any, error) {
	logger.Infof("WaitingL1InfoLeafAvailableResolver: checking the proof-building instance's L1 info tree index")
	index, err := r.port.L1InfoTreeIndexForBridge(ctx, tracking.Info())
	if err != nil {
		return nil, fmt.Errorf("l1 info tree index for bridge: %w", err)
	}
	if index == nil {
		return nil, ErrStepPending
	}
	return &types.L1InfoLeafAvailableResult{L1InfoTreeIndex: *index}, nil
}
