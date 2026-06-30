package e2e

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestBridgeL2ToL2 bridges a native asset from L2A to L2B through the aggkit bridge service
// and asserts the destination (L2B) balance increased after the claim.
//
// It requires the multi-chain env (EnvOpPP2Chains): when run against a single-chain env
// (testEnv.L2B == nil) the test is skipped. Bridge-service or on-chain failures fail loudly.
func TestBridgeL2ToL2(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.L2B == nil {
		t.Skip("L2->L2 bridge test requires EnvOpPP2Chains (L2B must be non-nil)")
	}

	// GER propagation L2A -> L1 -> L2B is the slow path; allow a generous budget.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Use fresh copies of the transactors so the helper's mutation of Value (for the native
	// ETH bridge) does not leak into the shared env transactors.
	originOpts := *testEnv.L2.Transactor
	destOpts := *testEnv.L2B.Transactor

	destinationAddress := destOpts.From
	initialDestBalance, err := testEnv.L2B.Client.BalanceAt(ctx, destinationAddress, nil)
	require.NoError(t, err, "failed to read initial L2B balance")

	// Bridge a native (ETH) amount from L2A to L2B. The helper asserts the claim succeeded and
	// that the destination balance increased.
	err = BridgeL2ToL2(ctx, testEnv, &originOpts, &destOpts, common.Address{})
	require.NoError(t, err, "L2->L2 bridge flow failed")

	finalDestBalance, err := testEnv.L2B.Client.BalanceAt(ctx, destinationAddress, nil)
	require.NoError(t, err, "failed to read final L2B balance")

	delta := new(big.Int).Sub(finalDestBalance, initialDestBalance)
	require.Positive(t, delta.Sign(),
		"expected L2B balance to increase: initial=%s final=%s", initialDestBalance.String(), finalDestBalance.String())
}
