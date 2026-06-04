package types

import (
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestRequestStatusStringValues(t *testing.T) {
	require.Equal(t, "detected", RequestStatusDetected.String())
	require.Equal(t, "policy-approved", RequestStatusPolicyApproved.String())
	require.Equal(t, "policy-rejected", RequestStatusPolicyRejected.String())
	require.Equal(t, "manual-approval-required", RequestStatusManualApprovalRequired.String())
	require.Equal(t, "queued", RequestStatusQueued.String())
	require.Equal(t, "sending", RequestStatusSending.String())
	require.Equal(t, "sent", RequestStatusSent.String())
	require.Equal(t, "confirmed", RequestStatusConfirmed.String())
	require.Equal(t, "failed", RequestStatusFailed.String())
}

func TestPolicyResultStringValues(t *testing.T) {
	require.Equal(t, "approved", PolicyResultApproved.String())
	require.Equal(t, "rejected", PolicyResultRejected.String())
	require.Equal(t, "manual", PolicyResultManual.String())
}

func TestRequestStatusTransitions(t *testing.T) {
	require.True(t, CanTransition(RequestStatusDetected, RequestStatusPolicyApproved))
	require.True(t, CanTransition(RequestStatusDetected, RequestStatusManualApprovalRequired))
	require.True(t, CanTransition(RequestStatusManualApprovalRequired, RequestStatusPolicyRejected))
	require.True(t, CanTransition(RequestStatusPolicyApproved, RequestStatusQueued))
	require.True(t, CanTransition(RequestStatusQueued, RequestStatusSending))
	require.True(t, CanTransition(RequestStatusSending, RequestStatusSent))
	require.True(t, CanTransition(RequestStatusSent, RequestStatusConfirmed))

	require.False(t, CanTransition(RequestStatusDetected, RequestStatusConfirmed))
	require.False(t, CanTransition(RequestStatusPolicyRejected, RequestStatusQueued))
	require.False(t, CanTransition(RequestStatusConfirmed, RequestStatusSending))
}

func TestRequestStatusTerminalStates(t *testing.T) {
	require.False(t, RequestStatusDetected.IsTerminal())
	require.False(t, RequestStatusSent.IsTerminal())
	require.True(t, RequestStatusPolicyRejected.IsTerminal())
	require.True(t, RequestStatusConfirmed.IsTerminal())
	require.True(t, RequestStatusFailed.IsTerminal())
}

func TestDeriveL1GlobalIndex(t *testing.T) {
	const depositCount uint32 = 42

	expected := bridgesync.GenerateGlobalIndexForNetworkID(0, depositCount)
	actual := DeriveL1GlobalIndex(depositCount)

	require.Equal(t, 0, expected.Cmp(actual))
}

func TestDeriveRequestKey(t *testing.T) {
	key := DeriveRequestKey(0, 1101, 42)

	require.Equal(t, RequestKey("0:1101:42"), key)
}

func TestNewBridgeExitFromSyncWithEtrogMarksLegacyZkEVMBridge(t *testing.T) {
	bridge := bridgesync.Bridge{
		BlockNum:           100,
		TxHash:             common.HexToHash("0xabc"),
		OriginNetwork:      L1OriginNetwork,
		DestinationNetwork: LegacyZkEVMRollupNetwork,
		DepositCount:       42,
		Amount:             big.NewInt(1),
	}

	exit := NewBridgeExitFromSyncWithEtrog(bridge, 100)

	require.True(t, exit.PreEtrog)
	require.Equal(t, uint64(42), exit.GlobalIndex.Uint64())
}

func TestNewBridgeExitFromSyncWithEtrogKeepsEtrogGlobalIndexAfterUpgrade(t *testing.T) {
	bridge := bridgesync.Bridge{
		BlockNum:           101,
		TxHash:             common.HexToHash("0xabc"),
		OriginNetwork:      L1OriginNetwork,
		DestinationNetwork: LegacyZkEVMRollupNetwork,
		DepositCount:       42,
		Amount:             big.NewInt(1),
	}

	exit := NewBridgeExitFromSyncWithEtrog(bridge, 100)

	require.False(t, exit.PreEtrog)
	require.Equal(t, 0, DeriveL1GlobalIndex(42).Cmp(exit.GlobalIndex))
}

func TestNewRequestFromBridgeExitCopiesSelectedL1InfoTreeIndex(t *testing.T) {
	index := uint32(77)
	bridge := BridgeExit{
		OriginNetwork:      L1OriginNetwork,
		DestinationNetwork: 1101,
		DepositCount:       42,
		GlobalIndex:        DeriveL1GlobalIndex(42),
		L1InfoTreeIndex:    &index,
	}

	request := NewRequestFromBridgeExit(bridge, time.Unix(1, 0))

	require.NotNil(t, request.L1InfoTreeIndex)
	require.Equal(t, index, *request.L1InfoTreeIndex)
	require.NotSame(t, bridge.L1InfoTreeIndex, request.L1InfoTreeIndex)
}
