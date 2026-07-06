package policy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	autoclaimconfig "github.com/agglayer/aggkit/autoclaim/config"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
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
	simulationMetadataCapacity = 4

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
	// ReasonOriginRejected is used when a request origin network is not allowed.
	ReasonOriginRejected = "origin network is not allowed"
	// ReasonTokenRejected is used when an asset origin token is not allowed.
	ReasonTokenRejected = "origin token is not allowed"
)

// TargetSimulator simulates a prepared target claim and inspects bounded safety signals.
type TargetSimulator interface {
	SimulateClaim(ctx context.Context, request autoclaimtypes.AutoClaimRequest) (*SimulationResult, error)
}

// SimulationResult contains bounded target-chain simulation evidence for policy evaluation.
type SimulationResult struct {
	GasUsed          uint64
	NestedBridgeCall NestedBridgeCallStatus
	Metadata         map[string]string
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
	switch request.Bridge.LeafType {
	case bridgesynctypes.LeafTypeMessage:
		if !p.config.AllowMessageClaims {
			return newDecision(
				autoclaimconfig.PolicyNameBasicFilter,
				autoclaimtypes.PolicyResultRejected,
				ReasonMessageClaimsRejected,
				nil,
			), nil
		}
	case bridgesynctypes.LeafTypeAsset:
	default:
		return nil, fmt.Errorf("basic-filter policy unsupported bridge leaf type: %d", request.Bridge.LeafType.Uint8())
	}

	if !originAllowed(request.Bridge.OriginNetwork, p.config.AllowedOrigins) {
		return newDecision(
			autoclaimconfig.PolicyNameBasicFilter,
			autoclaimtypes.PolicyResultRejected,
			ReasonOriginRejected,
			nil,
		), nil
	}

	if request.Bridge.LeafType == bridgesynctypes.LeafTypeAsset &&
		!tokenAllowed(request.Bridge.OriginAddress.String(), p.config.AllowedTokens) {
		return newDecision(
			autoclaimconfig.PolicyNameBasicFilter,
			autoclaimtypes.PolicyResultRejected,
			ReasonTokenRejected,
			nil,
		), nil
	}

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

	metadata := copyMetadata(result.Metadata)
	metadata["gas_used"] = strconv.FormatUint(result.GasUsed, 10)
	metadata["max_gas"] = strconv.FormatUint(p.config.MaxGas, 10)
	if metadata["nested_bridge_detection"] == "" {
		metadata["nested_bridge_detection"] = "skipped"
	}
	metadata["nested_bridge_call"] = string(result.NestedBridgeCall)
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

// RequiresPreparedProof reports that basic-filter needs the exact sender proof before policy evaluation.
func (p basicFilterPolicy) RequiresPreparedProof() bool {
	return true
}

func originAllowed(origin uint32, allowed []uint32) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if origin == value {
			return true
		}
	}
	return false
}

func tokenAllowed(token string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if strings.EqualFold(strings.TrimSpace(value), token) {
			return true
		}
	}
	return false
}

func copyMetadata(metadata map[string]string) map[string]string {
	copied := make(map[string]string, len(metadata)+simulationMetadataCapacity)
	for key, value := range metadata {
		copied[key] = value
	}
	return copied
}
