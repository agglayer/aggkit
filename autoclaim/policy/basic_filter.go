package policy

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	autoclaimconfig "github.com/agglayer/aggkit/autoclaim/config"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
)

// NestedBridgeCallStatus identifies whether target simulation found a nested bridge call.
type NestedBridgeCallStatus string

const (
	// NestedBridgeCallUnknown means nested bridge-call inspection could not be completed safely.
	NestedBridgeCallUnknown NestedBridgeCallStatus = "unknown"
	// NestedBridgeCallNotDetected means target simulation did not find a nested bridge call.
	NestedBridgeCallNotDetected NestedBridgeCallStatus = "not-detected"
	// NestedBridgeCallDetected means target simulation found a nested bridge call.
	NestedBridgeCallDetected NestedBridgeCallStatus = "detected"
)

const (
	// ReasonBasicFilterApproved is used when all basic-filter checks pass.
	ReasonBasicFilterApproved = "basic filter approved"
	// ReasonTargetSimulationUnavailable is used when target-chain simulation cannot be performed.
	ReasonTargetSimulationUnavailable = "target simulation unavailable"
	// ReasonGasLimitExceeded is used when simulated gas exceeds the configured limit.
	ReasonGasLimitExceeded = "simulated gas exceeds max gas"
	// ReasonNestedBridgeCallRejected is used when a nested bridge call is detected.
	ReasonNestedBridgeCallRejected = "nested bridge call detected"
	// ReasonNestedBridgeInspectionUnsafe is used when nested bridge-call inspection is unavailable or unsafe.
	ReasonNestedBridgeInspectionUnsafe = "nested bridge inspection unavailable"
)

// TargetSimulator simulates a prepared target claim and inspects bounded safety signals.
type TargetSimulator interface {
	SimulateClaim(ctx context.Context, request autoclaimtypes.AutoClaimRequest) (*SimulationResult, error)
}

// SimulationResult contains bounded target-chain simulation evidence for policy evaluation.
type SimulationResult struct {
	GasUsed          uint64
	NestedBridgeCall NestedBridgeCallStatus
}

type basicFilterPolicy struct {
	config          autoclaimconfig.PolicyConfig
	targetSimulator TargetSimulator
}

func newBasicFilterPolicy(
	config autoclaimconfig.PolicyConfig,
	targetSimulator TargetSimulator,
) autoclaimtypes.Policy {
	return basicFilterPolicy{
		config:          config,
		targetSimulator: targetSimulator,
	}
}

func (p basicFilterPolicy) Evaluate(
	ctx context.Context,
	request autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.PolicyDecision, error) {
	if p.targetSimulator == nil {
		return nil, errors.New(ReasonTargetSimulationUnavailable)
	}

	result, err := p.targetSimulator.SimulateClaim(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ReasonTargetSimulationUnavailable, err)
	}
	if result == nil {
		return nil, fmt.Errorf("%s: empty simulation result", ReasonTargetSimulationUnavailable)
	}

	metadata := map[string]string{
		"gas_used": strconv.FormatUint(result.GasUsed, 10),
		"max_gas":  strconv.FormatUint(p.config.MaxGas, 10),
	}
	if p.config.MaxGas > 0 && result.GasUsed > p.config.MaxGas {
		return newDecision(
			autoclaimconfig.PolicyNameBasicFilter,
			autoclaimtypes.PolicyResultRejected,
			ReasonGasLimitExceeded,
			metadata,
		), nil
	}

	switch result.NestedBridgeCall {
	case NestedBridgeCallDetected:
		return newDecision(
			autoclaimconfig.PolicyNameBasicFilter,
			autoclaimtypes.PolicyResultRejected,
			ReasonNestedBridgeCallRejected,
			metadata,
		), nil
	case NestedBridgeCallNotDetected:
		return newDecision(
			autoclaimconfig.PolicyNameBasicFilter,
			autoclaimtypes.PolicyResultApproved,
			ReasonBasicFilterApproved,
			metadata,
		), nil
	default:
		return nil, fmt.Errorf("%s: %s", ReasonNestedBridgeInspectionUnsafe, result.NestedBridgeCall)
	}
}
