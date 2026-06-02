package envs

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/stretchr/testify/require"
)

// TestP9_OpPP2Chains_LoadAndProbe is a temporary verification harness for plan
// step P9. It is NOT a migrated test: it only proves that LoadEnv exposes both
// OP-PP L2 networks, that CheckEnv validates both, and that each chain answers a
// minimal per-chain health probe (chainID + advancing block + non-zero balance).
func TestP9_OpPP2Chains_LoadAndProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	env, err := LoadEnv(ctx, EnvOpPP2Chains)
	require.NoError(t, err, "LoadEnv(op-pp-2chains) should succeed")
	require.NotNil(t, env)

	require.GreaterOrEqual(t, len(env.L2s), 2, "expected >= 2 L2 networks")
	t.Logf("Capabilities: NativeGas=%v Sequencer=%v", env.Capabilities.NativeGas, env.Capabilities.Sequencer)
	require.True(t, env.Capabilities.NativeGas, "op-pp-2chains should have NativeGas=true")

	for _, l2 := range env.L2s {
		t.Logf("L2 %s: ChainID=%s NetworkID=%d L2Bridge=%s MintableERC20=%s aggkit=%s aggsenderRPC=%s",
			l2.SummaryKey, l2.ChainID, l2.NetworkID,
			l2.Contracts.L2BridgeAddress.Hex(),
			l2.Contracts.MintableERC20Address.Hex(),
			l2.AggkitServiceName, l2.AggsenderRPCURL)
	}

	// Assert both expected networks present with correct chain/network ids.
	l2a, okA := env.L2ByNetworkID(1)
	require.True(t, okA, "network id 1 (001) must be present")
	require.Equal(t, "001", l2a.SummaryKey)
	require.Equal(t, int64(20201), l2a.ChainID.Int64())
	require.Equal(t, uint32(1), l2a.NetworkID)

	l2b, okB := env.L2ByNetworkID(2)
	require.True(t, okB, "network id 2 (002) must be present")
	require.Equal(t, "002", l2b.SummaryKey)
	require.Equal(t, int64(20202), l2b.ChainID.Int64())
	require.Equal(t, uint32(2), l2b.NetworkID)

	// checks.go validates BOTH networks (iterates e.L2s): chainID + advancing
	// block + non-zero balance for each.
	require.NoError(t, env.CheckEnv(ctx), "CheckEnv should validate all L2 networks")

	// Explicit per-chain health probe with real RPC output for evidence.
	probe := func(name string, l2 L2Config, primary bool) {
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

		bal, err := client.BalanceAt(ctx, l2.Transactor.From, nil)
		require.NoError(t, err, "%s: BalanceAt", name)
		require.Positive(t, bal.Sign(), "%s: balance must be > 0", name)

		t.Logf("PROBE %s GREEN: chainID=%s block=%d networkID=%d acct=%s balance=%s",
			name, chainID, blockNum, nid, l2.Transactor.From.Hex(), bal.String())
	}
	probe("chain-001", l2a, true)
	probe("chain-002", l2b, false)
}
