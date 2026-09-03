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
	// or nil if that network's own bridge-service instance has not indexed it yet. Only queried
	// right after StepWaitL1SettledGER, when the destination is Mainnet (see
	// WaitingL1InfoLeafAvailableResolver)
	InjectedGERAtIndex(ctx context.Context, bridge *BridgeInfo, leafIndex uint32) (*types.GERData, error)
}

// WaitingL1InfoLeafAvailableResolver resolves StepWaitingL1InfoLeafAvailable: whether the L1
// info tree leaf produced by the certificate's settlement (StepWaitL1SettledGER) is already
// indexed by the destination network's own bridge-service instance. Only ever the current step
// for L2->L1 bridges, right after StepWaitL1SettledGER and before StepWaitingClaim — settling a
// certificate on L1 does not mean every bridge-service instance already sees that leaf: its own
// L1 info tree sync may lag behind the finality this tracker itself uses for
// StepWaitL1SettledGER, so a bridge can look claimable per this tracker's own settlement check
// yet still fail to claim because the destination cannot produce a proof for it yet (#1823).
// ExpectedPath omits this step for any other destination (an L2), where StepWaitingGERInjection
// already covers the same concern through its own, real GER-injection event
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
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, idx int,
) (any, error) {
	leafIndex, err := r.leafIndexFromSettlement(tracking, idx)
	if err != nil {
		return nil, fmt.Errorf("previous step: %w", err)
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

// leafIndexFromSettlement reads the L1 info tree leaf index off StepWaitL1SettledGER's already
// resolved Result — the step immediately before this one on every path that reaches it (see
// ExpectedPath)
func (r *WaitingL1InfoLeafAvailableResolver) leafIndexFromSettlement(
	tracking *TrackingData, idx int,
) (uint32, error) {
	steps := tracking.AllSteps()
	if idx <= 0 || steps[idx-1].Step != types.StepWaitL1SettledGER {
		return 0, fmt.Errorf("unexpected previous step for StepWaitingL1InfoLeafAvailable")
	}
	settlement := steps[idx-1].ResultL1SettledGer
	if settlement == nil || settlement.L1InfoTreeIndex == nil {
		// StepWaitL1SettledGER never completes without a resolved leaf index (straight from
		// UpdateL1InfoTreeV2, or resolved by that step itself otherwise — see
		// WaitL1SettledGERResolver), so this only guards against an inconsistent read
		return 0, ErrStepPending
	}
	return *settlement.L1InfoTreeIndex, nil
}
