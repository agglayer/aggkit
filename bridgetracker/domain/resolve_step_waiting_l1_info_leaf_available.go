package domain

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// L1InfoLeafAvailableSource is the driven port to the L1 info tree leaf availability on a
// bridge's destination network, per that network's own bridge-service instance
type L1InfoLeafAvailableSource interface {
	// InjectedGERAtIndex returns the GER at leafIndex as seen by bridge's destination network,
	// or nil if that network's own bridge-service instance has not indexed it yet. Queried on
	// every route, right before StepWaitingClaim (see WaitingL1InfoLeafAvailableResolver)
	InjectedGERAtIndex(ctx context.Context, bridge *BridgeInfo, leafIndex uint32) (*types.GERData, error)
}

// WaitingL1InfoLeafAvailableResolver resolves StepWaitingL1InfoLeafAvailable: whether the
// covering L1 info tree leaf is already indexed by the destination network's own
// bridge-service instance. Always the current step right before StepWaitingClaim, on every
// route: unlike StepWaitingGERInjection, which only checks the L2-side fact that a GER
// injection tx landed (and is skipped entirely for an L2->L1 bridge, where mainnet needs no
// injection), this checks the destination's own L1 info tree sync — the thing that actually
// has to have caught up for that instance to produce a claim proof. That sync can lag behind
// the finality this tracker itself uses for StepWaitL1SettledGER, so a bridge can look
// claimable per this tracker's own settlement/injection checks yet still fail to claim because
// the destination cannot produce a proof for it yet — this is why it is never skipped or
// inferred from a sibling step, on any route (#1823)
type WaitingL1InfoLeafAvailableResolver struct {
	port L1InfoLeafAvailableSource
}

// NewWaitingL1InfoLeafAvailableResolver returns a WaitingL1InfoLeafAvailableResolver checking
// leaf availability through port
func NewWaitingL1InfoLeafAvailableResolver(port L1InfoLeafAvailableSource) *WaitingL1InfoLeafAvailableResolver {
	return &WaitingL1InfoLeafAvailableResolver{port: port}
}

// Resolve implements StepResolver
func (r *WaitingL1InfoLeafAvailableResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, _ int,
) (any, error) {
	leafIndex, err := r.resolvedLeafIndex(tracking)
	if err != nil {
		return nil, fmt.Errorf("resolved leaf index: %w", err)
	}

	logger.Infof("WaitingL1InfoLeafAvailableResolver: checking L1 info tree leaf %d availability", leafIndex)
	leaf, err := r.port.InjectedGERAtIndex(ctx, tracking.Info(), leafIndex)
	if err != nil {
		return nil, fmt.Errorf("l1 info tree leaf availability: %w", err)
	}
	if leaf == nil {
		return nil, ErrStepPending
	}

	result := &types.InjectedGERL1Leaf{GER: *leaf.GER}
	if leaf.BlockNumber != nil {
		result.BlockNumber = *leaf.BlockNumber
	}
	if leaf.BlockTimestamp != nil {
		result.BlockTimestamp = *leaf.BlockTimestamp
	}
	return result, nil
}

// resolvedLeafIndex reads the L1 info tree leaf index this step must check off whichever
// earlier step resolved it — StepWaitL1SettledGER for an L2-originated bridge (L2->L1, L2->L2),
// or StepWaitingGERUpdate for an L1-originated one (L1->L2). It is looked up by step type
// rather than assumed to sit right before this one (see indexOfStep), since StepWaitingGERInjection
// sits in between on the routes that have it (L1->L2, L2->L2)
func (r *WaitingL1InfoLeafAvailableResolver) resolvedLeafIndex(tracking *TrackingData) (uint32, error) {
	steps := tracking.AllSteps()
	if idx := indexOfStep(steps, types.StepWaitL1SettledGER); idx >= 0 {
		settlement := steps[idx].ResultL1SettledGer
		if settlement == nil || settlement.L1InfoTreeIndex == nil {
			// StepWaitL1SettledGER never completes without a resolved leaf index (straight from
			// UpdateL1InfoTreeV2, or resolved by that step itself otherwise — see
			// WaitL1SettledGERResolver), so this only guards against an inconsistent read
			return 0, ErrStepPending
		}
		return *settlement.L1InfoTreeIndex, nil
	}
	if idx := indexOfStep(steps, types.StepWaitingGERUpdate); idx >= 0 {
		update := steps[idx].ResultGerUpdate
		if update == nil {
			// same guard as above, for StepWaitingGERUpdate's own leaf index
			return 0, ErrStepPending
		}
		return update.L1InfoTreeIndex, nil
	}
	return 0, fmt.Errorf("neither StepWaitL1SettledGER nor StepWaitingGERUpdate found in AllSteps")
}
