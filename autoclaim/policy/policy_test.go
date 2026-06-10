package policy

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	autoclaimconfig "github.com/agglayer/aggkit/autoclaim/config"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestRegistryRejectsInvalidPolicyName(t *testing.T) {
	policy, err := NewRegistry().NewPolicy("unknown", autoclaimconfig.PolicyConfig{})

	require.Error(t, err)
	require.Nil(t, policy)
	require.ErrorContains(t, err, "unknown auto claim policy: unknown")
}

func TestAllowAllApproves(t *testing.T) {
	policy, err := NewPolicy(autoclaimconfig.PolicyNameAllowAll, autoclaimconfig.PolicyConfig{})
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeMessage))
	require.NoError(t, err)

	require.Equal(t, string(autoclaimconfig.PolicyNameAllowAll), decision.PolicyName)
	require.Equal(t, autoclaimtypes.PolicyResultApproved, decision.Result)
	require.Equal(t, ReasonAllowAll, decision.Reason)
	require.NotZero(t, decision.CreatedAt)
	require.NotZero(t, decision.UpdatedAt)
}

func TestAPIApproveRequiresManualDecision(t *testing.T) {
	policy, err := NewPolicy(autoclaimconfig.PolicyNameAPIApprove, autoclaimconfig.PolicyConfig{})
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))
	require.NoError(t, err)

	require.Equal(t, string(autoclaimconfig.PolicyNameAPIApprove), decision.PolicyName)
	require.Equal(t, autoclaimtypes.PolicyResultManual, decision.Result)
	require.Equal(t, ReasonAPIApprovalRequired, decision.Reason)
}

func TestNoMessageRejectsMessageClaims(t *testing.T) {
	policy, err := NewPolicy(autoclaimconfig.PolicyNameNoMessage, autoclaimconfig.PolicyConfig{})
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeMessage))
	require.NoError(t, err)

	require.Equal(t, string(autoclaimconfig.PolicyNameNoMessage), decision.PolicyName)
	require.Equal(t, autoclaimtypes.PolicyResultRejected, decision.Result)
	require.Equal(t, ReasonMessageClaimsRejected, decision.Reason)
}

func TestNoMessageApprovesAssetClaims(t *testing.T) {
	policy, err := NewPolicy(autoclaimconfig.PolicyNameNoMessage, autoclaimconfig.PolicyConfig{})
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))
	require.NoError(t, err)

	require.Equal(t, string(autoclaimconfig.PolicyNameNoMessage), decision.PolicyName)
	require.Equal(t, autoclaimtypes.PolicyResultApproved, decision.Result)
	require.Equal(t, ReasonAssetClaimApproved, decision.Reason)
}

func TestNoMessageReturnsErrorForUnsupportedLeafType(t *testing.T) {
	policy, err := NewPolicy(autoclaimconfig.PolicyNameNoMessage, autoclaimconfig.PolicyConfig{})
	require.NoError(t, err)

	_, err = policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafType(99)))

	require.ErrorContains(t, err, "unsupported bridge leaf type")
}

func TestBasicFilterRejectsGasOverMaxGas(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{MaxGas: 100},
		WithTargetSimulator(staticSimulator(SimulationResult{
			GasUsed:          101,
			NestedBridgeCall: NestedBridgeCallNotDetected,
		})),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))
	require.NoError(t, err)

	require.Equal(t, autoclaimtypes.PolicyResultRejected, decision.Result)
	require.Equal(t, ReasonGasLimitExceeded, decision.Reason)
	require.Equal(t, "101", decision.Metadata["gas_used"])
	require.Equal(t, "100", decision.Metadata["max_gas"])
}

func TestBasicFilterApprovesWhenGasAndNestedBridgeChecksPass(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{MaxGas: 100},
		WithTargetSimulator(staticSimulator(SimulationResult{
			GasUsed:          100,
			NestedBridgeCall: NestedBridgeCallNotDetected,
			Metadata: map[string]string{
				"nested_bridge_detection": "skipped",
			},
		})),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))
	require.NoError(t, err)

	require.Equal(t, string(autoclaimconfig.PolicyNameBasicFilter), decision.PolicyName)
	require.Equal(t, autoclaimtypes.PolicyResultApproved, decision.Result)
	require.Equal(t, ReasonBasicFilterApproved, decision.Reason)
	require.Equal(t, "skipped", decision.Metadata["nested_bridge_detection"])
	require.Equal(t, string(NestedBridgeCallNotDetected), decision.Metadata["nested_bridge_call"])
}

func TestBasicFilterRejectsMessageClaimsWhenNotAllowed(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{AllowMessageClaims: false},
		WithTargetSimulator(errorSimulator{err: errors.New("should not be called")}),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeMessage))
	require.NoError(t, err)

	require.Equal(t, autoclaimtypes.PolicyResultRejected, decision.Result)
	require.Equal(t, ReasonMessageClaimsRejected, decision.Reason)
}

func TestBasicFilterSimulatesMessageClaimsWhenAllowed(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{AllowMessageClaims: true},
		WithTargetSimulator(staticSimulator(SimulationResult{
			GasUsed:          100,
			NestedBridgeCall: NestedBridgeCallNotDetected,
		})),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeMessage))
	require.NoError(t, err)

	require.Equal(t, autoclaimtypes.PolicyResultApproved, decision.Result)
}

func TestBasicFilterRejectsDisallowedOrigin(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{AllowedOrigins: []uint32{1}},
		WithTargetSimulator(errorSimulator{err: errors.New("should not be called")}),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))
	require.NoError(t, err)

	require.Equal(t, autoclaimtypes.PolicyResultRejected, decision.Result)
	require.Equal(t, ReasonOriginRejected, decision.Reason)
}

func TestBasicFilterRejectsDisallowedAssetToken(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{
			AllowedOrigins: []uint32{autoclaimtypes.L1OriginNetwork},
			AllowedTokens:  []string{"0x9000000000000000000000000000000000000009"},
		},
		WithTargetSimulator(errorSimulator{err: errors.New("should not be called")}),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))
	require.NoError(t, err)

	require.Equal(t, autoclaimtypes.PolicyResultRejected, decision.Result)
	require.Equal(t, ReasonTokenRejected, decision.Reason)
}

func TestBasicFilterReturnsErrorForUnsupportedLeafType(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{},
		WithTargetSimulator(errorSimulator{err: errors.New("should not be called")}),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafType(99)))

	require.Nil(t, decision)
	require.ErrorContains(t, err, "unsupported bridge leaf type")
}

func TestBasicFilterRejectsDetectedNestedBridgeCalls(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{MaxGas: 100},
		WithTargetSimulator(staticSimulator(SimulationResult{
			GasUsed:          99,
			NestedBridgeCall: NestedBridgeCallDetected,
		})),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))
	require.NoError(t, err)

	require.Equal(t, autoclaimtypes.PolicyResultRejected, decision.Result)
	require.Equal(t, ReasonNestedBridgeCallRejected, decision.Reason)
}

func TestBasicFilterReturnsErrorWhenTargetSimulationUnavailable(t *testing.T) {
	policy, err := NewPolicy(autoclaimconfig.PolicyNameBasicFilter, autoclaimconfig.PolicyConfig{MaxGas: 100})
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))

	require.Nil(t, decision)
	require.ErrorContains(t, err, ReasonTargetSimulationUnavailable)
}

func TestBasicFilterReturnsErrorWhenTargetSimulationFails(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{MaxGas: 100},
		WithTargetSimulator(errorSimulator{err: errors.New("rpc unavailable")}),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))

	require.Nil(t, decision)
	require.ErrorContains(t, err, ReasonTargetSimulationUnavailable)
	require.ErrorContains(t, err, "rpc unavailable")
}

func TestBasicFilterReturnsErrorWhenTargetSimulationReturnsNil(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{MaxGas: 100},
		WithTargetSimulator(nilSimulator{}),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))

	require.Nil(t, decision)
	require.ErrorContains(t, err, ReasonTargetSimulationUnavailable)
	require.ErrorContains(t, err, "empty simulation result")
}

func TestBasicFilterReturnsErrorWhenNestedBridgeInspectionIsUnsafe(t *testing.T) {
	policy, err := NewPolicy(
		autoclaimconfig.PolicyNameBasicFilter,
		autoclaimconfig.PolicyConfig{MaxGas: 100},
		WithTargetSimulator(staticSimulator(SimulationResult{
			GasUsed:          99,
			NestedBridgeCall: NestedBridgeCallUnknown,
		})),
	)
	require.NoError(t, err)

	decision, err := policy.Evaluate(context.Background(), makeRequest(bridgesynctypes.LeafTypeAsset))

	require.Nil(t, decision)
	require.ErrorContains(t, err, ReasonNestedBridgeInspectionUnsafe)
	require.ErrorContains(t, err, string(NestedBridgeCallUnknown))
}

func makeRequest(leafType bridgesynctypes.LeafType) autoclaimtypes.AutoClaimRequest {
	bridge := autoclaimtypes.BridgeExit{
		LeafType:           leafType,
		OriginNetwork:      autoclaimtypes.L1OriginNetwork,
		OriginAddress:      common.HexToAddress("0x1000000000000000000000000000000000000001"),
		DestinationNetwork: 1,
		DestinationAddress: common.HexToAddress("0x2000000000000000000000000000000000000002"),
		Amount:             big.NewInt(100),
		DepositCount:       7,
		GlobalIndex:        autoclaimtypes.DeriveL1GlobalIndex(7),
	}
	return autoclaimtypes.NewRequestFromBridgeExit(bridge, testNow())
}

func testNow() time.Time {
	return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
}

type staticSimulator SimulationResult

func (s staticSimulator) SimulateClaim(
	_ context.Context,
	_ autoclaimtypes.AutoClaimRequest,
) (*SimulationResult, error) {
	result := SimulationResult(s)
	return &result, nil
}

type nilSimulator struct{}

func (nilSimulator) SimulateClaim(
	_ context.Context,
	_ autoclaimtypes.AutoClaimRequest,
) (*SimulationResult, error) {
	return nil, nil
}

type errorSimulator struct {
	err error
}

func (s errorSimulator) SimulateClaim(
	_ context.Context,
	_ autoclaimtypes.AutoClaimRequest,
) (*SimulationResult, error) {
	return nil, s.err
}
