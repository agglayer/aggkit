package domain

import (
	"github.com/agglayer/aggkit/bridgetracker/types"
)

// ExpectedPath returns the fixed sequence of steps a bridge of the given type goes through
func ExpectedPath(bridgeType types.BridgeType) []types.BridgeStep {
	switch bridgeType {
	case types.BridgeTypeL1ToL2:
		return []types.BridgeStep{
			types.StepWaitingGERUpdate,
			types.StepWaitingGERInjection,
			types.StepWaitingClaim,
			types.StepClaimed,
		}
	case types.BridgeTypeL2ToL1:
		return []types.BridgeStep{
			types.StepWaitingLERUpdate,
			types.StepPendingInclusion,
			types.StepCertificatePending,
			types.StepWaitL1SettledGER,
			types.StepWaitingClaim,
			types.StepClaimed,
		}
	default: // L2 -> L2'
		return []types.BridgeStep{
			types.StepWaitingLERUpdate,
			types.StepPendingInclusion,
			types.StepCertificatePending,
			types.StepWaitL1SettledGER,
			types.StepWaitingGERInjection,
			types.StepWaitingClaim,
			types.StepClaimed,
		}
	}
}
