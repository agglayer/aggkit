package remove_ger

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

type stubL2Bridge struct {
	emergency      bool
	emergencyErr   error
	activateCalled bool
	claimed        map[string]bool
}

func (s *stubL2Bridge) IsEmergencyState(_ *bind.CallOpts) (bool, error) {
	return s.emergency, s.emergencyErr
}

func (s *stubL2Bridge) ActivateEmergencyState(_ *bind.TransactOpts) (*types.Transaction, error) {
	s.activateCalled = true
	return nil, nil
}

func (*stubL2Bridge) DeactivateEmergencyState(_ *bind.TransactOpts) (*types.Transaction, error) {
	return nil, nil
}

func (*stubL2Bridge) UnsetMultipleClaims(_ *bind.TransactOpts, _ []*big.Int) (*types.Transaction, error) {
	return nil, nil
}

func (*stubL2Bridge) SetMultipleClaims(_ *bind.TransactOpts, _ []*big.Int) (*types.Transaction, error) {
	return nil, nil
}

func (*stubL2Bridge) ForceEmitDetailedClaimEvent(
	_ *bind.TransactOpts,
	_ []agglayerbridgel2.AgglayerBridgeL2ClaimData,
) (*types.Transaction, error) {
	return nil, nil
}

func (s *stubL2Bridge) IsClaimed(_ *bind.CallOpts, depositCount uint32, originNetwork uint32) (bool, error) {
	if s == nil || s.claimed == nil {
		return false, nil
	}
	return s.claimed[claimStateKey(depositCount, originNetwork)], nil
}

func (*stubL2Bridge) ParseDetailedClaimEvent(types.Log) (*agglayerbridgel2.Agglayerbridgel2DetailedClaimEvent, error) {
	return nil, nil
}

func (*stubL2Bridge) ParseClaimEvent(types.Log) (*agglayerbridgel2.Agglayerbridgel2ClaimEvent, error) {
	return nil, nil
}

func TestStepFreezeBridge_AlreadyInEmergencyState(t *testing.T) {
	bridge := &stubL2Bridge{emergency: true}
	env := &Env{
		L2Bridge: bridge,
		waitReceiptFn: func(context.Context, *types.Transaction) (*types.Receipt, error) {
			return nil, errors.New("unexpected waitReceipt call")
		},
	}

	err := stepFreezeBridge(context.Background(), env, &bind.TransactOpts{}, &bind.CallOpts{})

	require.Error(t, err)
	require.Contains(t, err.Error(), "already in emergency state")
	require.False(t, bridge.activateCalled)
}
