package types

import (
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
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

func TestDeriveGlobalIndex(t *testing.T) {
	const originNetwork uint32 = 5
	const depositCount uint32 = 99

	expected := bridgesync.GenerateGlobalIndexForNetworkID(originNetwork, depositCount)
	actual := DeriveGlobalIndex(originNetwork, depositCount)

	require.Equal(t, 0, expected.Cmp(actual))
}

func TestNewBridgeExitFromSyncWrapsWithEtrogZero(t *testing.T) {
	bridge := bridgesync.Bridge{
		BlockNum:           50,
		TxHash:             common.HexToHash("0xdef"),
		OriginNetwork:      L1OriginNetwork,
		DestinationNetwork: 10,
		DepositCount:       7,
		Amount:             big.NewInt(500),
		Metadata:           []byte{0x01},
	}

	exit := NewBridgeExitFromSync(bridge)

	require.False(t, exit.PreEtrog)
	require.Equal(t, bridge.DepositCount, exit.DepositCount)
	require.Equal(t, bridge.DestinationNetwork, exit.DestinationNetwork)
	require.Equal(t, 0, DeriveL1GlobalIndex(bridge.DepositCount).Cmp(exit.GlobalIndex))
}

func TestNewRequestFromBridgeExitNilGlobalIndex(t *testing.T) {
	bridge := BridgeExit{
		OriginNetwork:      L1OriginNetwork,
		DestinationNetwork: 1101,
		DepositCount:       42,
		GlobalIndex:        nil,
	}

	request := NewRequestFromBridgeExit(bridge, time.Unix(1, 0))

	require.NotNil(t, request.GlobalIndex)
	require.Equal(t, 0, DeriveL1GlobalIndex(bridge.DepositCount).Cmp(request.GlobalIndex))
}

func TestProofToABIProof(t *testing.T) {
	var proof treetypes.Proof
	proof[0] = common.HexToHash("0x01")
	proof[1] = common.HexToHash("0x02")

	abiProof := ProofToABIProof(proof)

	require.Len(t, abiProof, int(treetypes.DefaultHeight))
	require.Equal(t, proof[0], common.Hash(abiProof[0]))
	require.Equal(t, proof[1], common.Hash(abiProof[1]))
	require.Equal(t, common.Hash{}, common.Hash(abiProof[2]))
}

func TestCopyBigIntNilInput(t *testing.T) {
	result := copyBigInt(nil)
	require.Nil(t, result)

	value := big.NewInt(12345)
	copied := copyBigInt(value)
	require.NotNil(t, copied)
	require.Equal(t, 0, value.Cmp(copied))
	require.NotSame(t, value, copied)
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
