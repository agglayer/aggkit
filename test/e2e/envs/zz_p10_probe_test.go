package envs

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestP10_CDKErigon3Chains_LoadAndProbe is a temporary verification harness for
// plan step P10. It is NOT a migrated bridge test: it only proves that LoadEnv
// exposes all three cdk-erigon L2 networks, that CheckEnv validates them, and
// that each chain answers a minimal per-chain health probe (chainID + advancing
// block). It also asserts the per-network gas model: networks 001/002 are
// custom-gas (gas-token address surfaced, MintableERC20 deploy skipped) and 003
// is native (MintableERC20 deployed). The cdk-erigon EL RPC wiring is exercised
// implicitly via clientForNetwork / the L2 client dial.
func TestP10_CDKErigon3Chains_LoadAndProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	env, err := LoadEnv(ctx, EnvCDKErigon3Chains)
	require.NoError(t, err, "LoadEnv(cdk-erigon-3chains) should succeed")
	require.NotNil(t, env)

	require.GreaterOrEqual(t, len(env.L2s), 3, "expected >= 3 L2 networks")
	t.Logf("Capabilities: NativeGas=%v Sequencer=%v MultiNetwork=%v MultiAggkit=%v",
		env.Capabilities.NativeGas, env.Capabilities.Sequencer,
		env.Capabilities.MultiNetwork, env.Capabilities.MultiAggkit)
	require.Equal(t, SequencerCDKErigon, env.Capabilities.Sequencer,
		"cdk-erigon-3chains should be a cdk-erigon env")
	// This env is mixed-gas: the env-level NativeGas flag means "native deploys
	// permitted" (true) while the per-network gas model still skips the deploy on
	// the custom-gas chains 001/002 (asserted via MintableERC20 below).

	for _, l2 := range env.L2s {
		t.Logf("L2 %s: ChainID=%s NetworkID=%d L2Bridge=%s GasToken=%s MintableERC20=%s rpc=%s",
			l2.SummaryKey, l2.ChainID, l2.NetworkID,
			l2.Contracts.L2BridgeAddress.Hex(),
			l2.Contracts.GasTokenAddress.Hex(),
			l2.Contracts.MintableERC20Address.Hex(),
			l2.OpGethRPCURL)
	}

	// Assert the three expected networks with correct chain/network ids.
	l2a, okA := env.L2ByNetworkID(1)
	require.True(t, okA, "network id 1 (001) must be present")
	require.Equal(t, "001", l2a.SummaryKey)
	require.Equal(t, uint32(1), l2a.NetworkID)

	l2b, okB := env.L2ByNetworkID(2)
	require.True(t, okB, "network id 2 (002) must be present")
	require.Equal(t, "002", l2b.SummaryKey)
	require.Equal(t, int64(20202), l2b.ChainID.Int64())
	require.Equal(t, uint32(2), l2b.NetworkID)

	l2c, okC := env.L2ByNetworkID(3)
	require.True(t, okC, "network id 3 (003) must be present")
	require.Equal(t, "003", l2c.SummaryKey)
	require.Equal(t, int64(20203), l2c.ChainID.Int64())
	require.Equal(t, uint32(3), l2c.NetworkID)

	// Per-network gas model:
	//  - 001 & 002 are custom-gas: a gas-token address is surfaced and the
	//    MintableERC20 auto-deploy is skipped (zero address).
	//  - 003 is native: no gas token, MintableERC20 deployed (non-zero address).
	zero := common.Address{}
	require.NotEqual(t, zero, l2a.Contracts.GasTokenAddress, "001 must surface a custom gas token")
	require.NotEqual(t, zero, l2b.Contracts.GasTokenAddress, "002 must surface a custom gas token")
	require.Equal(t, zero, l2a.Contracts.MintableERC20Address, "001 (custom-gas) must skip MintableERC20")
	require.Equal(t, zero, l2b.Contracts.MintableERC20Address, "002 (custom-gas) must skip MintableERC20")

	require.Equal(t, zero, l2c.Contracts.GasTokenAddress, "003 must be native (no gas token)")
	require.NotEqual(t, zero, l2c.Contracts.MintableERC20Address, "003 (native) must deploy MintableERC20")

	// checks.go validates all three networks (iterates e.L2s).
	require.NoError(t, env.CheckEnv(ctx), "CheckEnv should validate all L2 networks")

	// Explicit per-chain health probe with real RPC output for evidence.
	probe := func(name string, l2 L2Config, primary, expectGasToken bool) {
		client, cleanup, err := env.clientForNetwork(ctx, l2, primary)
		require.NoError(t, err, "%s: dial client", name)
		defer cleanup()

		chainID, err := client.ChainID(ctx)
		require.NoError(t, err, "%s: ChainID", name)
		require.Equal(t, l2.ChainID.Int64(), chainID.Int64(), "%s: chainID mismatch", name)

		blockNum, err := client.BlockNumber(ctx)
		require.NoError(t, err, "%s: BlockNumber", name)
		require.Greater(t, blockNum, uint64(0), "%s: block must advance", name)

		nid, err := l2.Contracts.L2Bridge.NetworkID(&bind.CallOpts{Context: ctx})
		require.NoError(t, err, "%s: live NetworkID", name)
		require.Equal(t, l2.NetworkID, nid, "%s: NetworkID mismatch", name)

		if expectGasToken {
			require.NotEqual(t, zero, l2.Contracts.GasTokenAddress, "%s: gas token must be surfaced", name)
		}

		t.Logf("PROBE %s GREEN: chainID=%s block=%d networkID=%d gasToken=%s",
			name, chainID, blockNum, nid, l2.Contracts.GasTokenAddress.Hex())
	}
	probe("chain-001", l2a, true, true)
	probe("chain-002", l2b, false, true)
	probe("chain-003", l2c, false, false)
}
