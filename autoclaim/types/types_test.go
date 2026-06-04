package types

import (
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
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
