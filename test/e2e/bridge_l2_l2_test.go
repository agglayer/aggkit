package e2e

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/stretchr/testify/require"
)

// TestBridgeL2ToL2 bridges a MintableERC20 token from L2A to L2B through the aggkit bridge
// service and asserts the claim completed successfully on L2B.
//
// MintableERC20 is an L2A-native token, so it bypasses the Local Balance Tree underflow check
// that would block bridging native ETH before any L1->L2 bridge has been performed.
//
// It requires a multi-chain env: when run against a single-chain env
// (testEnv.L2B == nil) the test is skipped. Bridge-service or on-chain failures fail loudly.
func TestBridgeL2ToL2(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.L2B == nil {
		t.Skip("L2->L2 bridge test requires a multi-chain env (L2B must be non-nil)")
	}

	// GER propagation L2A -> L1 -> L2B is the slow path; allow a generous budget.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Use fresh copies of the transactors so the helper's mutation of Value does not
	// leak into the shared env transactors.
	originOpts := *testEnv.L2.Transactor
	destOpts := *testEnv.L2B.Transactor

	// Mint MintableERC20 tokens on L2A and approve the bridge contract.
	// MintableERC20 is an L2A-native token, which avoids the Local Balance Tree underflow
	// check that prevents bridging native ETH when no L1->L2 bridge has yet been performed.
	mintAmount := new(big.Int).SetInt64(1e14)
	mintTx, err := testEnv.L2.Contracts.MintableERC20.Mint(&originOpts, originOpts.From, mintAmount)
	require.NoError(t, err, "failed to mint MintableERC20 on L2A")
	_, err = bind.WaitMined(ctx, testEnv.L2.Client, mintTx)
	require.NoError(t, err, "failed to wait for MintableERC20 mint tx on L2A")

	approveTx, err := testEnv.L2.Contracts.MintableERC20.Approve(
		&originOpts, testEnv.L2.Contracts.L2BridgeAddress, mintAmount,
	)
	require.NoError(t, err, "failed to approve MintableERC20 for L2A bridge")
	_, err = bind.WaitMined(ctx, testEnv.L2.Client, approveTx)
	require.NoError(t, err, "failed to wait for MintableERC20 approve tx on L2A")

	// Bridge the MintableERC20 token from L2A to L2B.
	err = BridgeL2ToL2(ctx, testEnv, &originOpts, &destOpts, testEnv.L2.Contracts.MintableERC20Address)
	require.NoError(t, err, "L2->L2 bridge flow failed")
}
