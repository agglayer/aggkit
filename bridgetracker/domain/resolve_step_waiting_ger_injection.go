package domain

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// WaitingGERInjectionSource is the driven port to the Global Exit Root injection state on a
// bridge's destination network
type WaitingGERInjectionSource interface {
	// InjectedGER returns the GER injected on the destination network that covers bridge, or
	// nil if no covering GER has been injected yet. Only queried when the destination is an L2
	InjectedGER(ctx context.Context, bridge *BridgeInfo) (*types.GERData, error)

	// InjectedGERAtIndex returns the GER injected at leafIndex on bridge's destination
	// network, or nil if not injected yet. Only queried right after StepWaitL1SettledGER,
	// when the destination is an L2
	InjectedGERAtIndex(ctx context.Context, bridge *BridgeInfo, leafIndex uint32) (*types.GERData, error)
}

// WaitingGERInjectionResolver resolves StepWaitingGERInjection: whether the Global Exit Root
// covering the bridge has been injected on its destination network. Only ever the current step
// when the destination is an L2, since ExpectedPath omits it for L2->L1 paths. Which GER to
// check depends on the step right before it: StepWaitingGERUpdate for an L1-originated bridge
// (the GER the deposit itself produced on L1), or StepWaitL1SettledGER for an L2-originated one
// (the GER produced by the certificate's settlement, see types.L1SettledGERResult)
type WaitingGERInjectionResolver struct {
	port WaitingGERInjectionSource
}

// NewWaitingGERInjectionResolver returns a WaitingGERInjectionResolver reading injected GERs
// through port
func NewWaitingGERInjectionResolver(port WaitingGERInjectionSource) *WaitingGERInjectionResolver {
	return &WaitingGERInjectionResolver{port: port}
}

// Resolve implements StepResolver
func (r *WaitingGERInjectionResolver) Resolve(
	logger aggkitcommon.Logger, ctx context.Context, tracking *TrackingData, idx int,
) (any, error) {
	leafIndex, err := r.getLeafIndexFromPreviousStep(tracking, idx)
	if err != nil {
		return nil, fmt.Errorf("previous step: %w", err)
	}
	logger.Infof("WaitingGERInjectionResolver: checking injected GER at leaf index %d", leafIndex)
	injected, err := r.port.InjectedGERAtIndex(ctx, tracking.Info(), leafIndex)

	if err != nil {
		return nil, fmt.Errorf("injected GER: %w", err)
	}
	if injected == nil {
		return nil, ErrStepPending
	}

	result := &types.InjectedGERResult{GER: *injected.GER}
	if injected.BlockNumber != nil {
		result.BlockNumber = *injected.BlockNumber
	}
	if injected.BlockTimestamp != nil {
		result.BlockTimestamp = *injected.BlockTimestamp
	}
	return result, nil
}

func (r *WaitingGERInjectionResolver) getLeafIndexFromPreviousStep(
	tracking *TrackingData, idx int,
) (uint32, error) {
	steps := tracking.AllSteps()
	switch steps[idx-1].Step {
	case types.StepWaitL1SettledGER:
		settlement := steps[idx-1].ResultL1SettledGer
		if settlement == nil || settlement.L1InfoTreeIndex == nil {
			// StepWaitL1SettledGER never completes without a resolved leaf index (straight from
			// UpdateL1InfoTreeV2, or resolved by that step itself otherwise — see
			// WaitL1SettledGERResolver), so this only guards against an inconsistent read
			return 0, ErrStepPending
		}
		return *settlement.L1InfoTreeIndex, nil
	case types.StepWaitingGERUpdate:
		update := steps[idx-1].ResultGerUpdate
		if update == nil {
			// StepWaitingGERUpdate never completes without a resolved GER (straight from
			// UpdateL1InfoTree/UpdateL1InfoTreeV2, or resolved by that step itself otherwise — see
			// WaitingGERUpdateResolver), so this only guards against an inconsistent read
			return 0, ErrStepPending
		}
		return update.L1InfoTreeIndex, nil
	default:
		return 0, fmt.Errorf("unexpected previous step %v for StepWaitingGERInjection", steps[idx-1].Step)
	}
}
