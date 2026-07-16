package exit_certificate

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestExpandStepRange(t *testing.T) {
	t.Parallel()
	// open range stops at the last auto step (sign)
	open, err := expandStepRange("0-")
	require.NoError(t, err)
	require.Equal(t, "0", open[0])
	require.Equal(t, lastAutoStep, open[len(open)-1])

	// closed range
	closed, err := expandStepRange("a-c")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b1", "b2", "b3", "c"}, closed)

	// unknown start / end
	_, err = expandStepRange("zzz-c")
	require.Error(t, err)
	_, err = expandStepRange("0-zzz")
	require.Error(t, err)

	// reversed range
	_, err = expandStepRange("f-c")
	require.Error(t, err)
}

func TestAliasRange(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a", aliasRangeStart("a")) // passthrough — "a" has no sub-steps
	require.Equal(t, "b1", aliasRangeStart("b"))
	require.Equal(t, "g1", aliasRangeStart("g"))
	require.Equal(t, "x", aliasRangeStart("x")) // passthrough

	require.Equal(t, "a", aliasRangeEnd("a"))
	require.Equal(t, "b3", aliasRangeEnd("b"))
	require.Equal(t, "g2", aliasRangeEnd("g"))
	require.Equal(t, "x", aliasRangeEnd("x"))
}

func TestFileExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.False(t, fileExists(filepath.Join(dir, "nope.json")))
	mustSaveJSON(t, dir, "yes.json", map[string]int{"a": 1})
	require.True(t, fileExists(filepath.Join(dir, "yes.json")))
}

func TestLoadTargetBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := loadTargetBlock(dir) // missing → error
	require.Error(t, err)

	mustSaveJSON(t, dir, "step-0-l2_target_block.json", uint64(12345))
	n, err := loadTargetBlock(dir)
	require.NoError(t, err)
	require.Equal(t, uint64(12345), n)
}

func TestLoadWrappedTokensFromLBT(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := loadWrappedTokensFromLBT(dir) // missing → error
	require.Error(t, err)

	mustSaveJSON(t, dir, "step-0-lbt.json", []LBTEntry{
		{WrappedTokenAddress: common.BytesToAddress([]byte("wrap")), OriginNetwork: 1,
			OriginTokenAddress: common.BytesToAddress([]byte("orig")), Balance: "1000"},
	})
	tokens, err := loadWrappedTokensFromLBT(dir)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	require.Equal(t, uint32(1), tokens[0].OriginNetwork)
}

func TestRunSingleStepUnknown(t *testing.T) {
	t.Parallel()
	cfg := &Config{Options: Options{OutputDir: t.TempDir()}}
	err := runSingleStep(context.Background(), "bogus", cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown step")
}

func TestRunSingleCAndD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tok := common.BytesToAddress([]byte("wrap"))
	orig := common.BytesToAddress([]byte("orig"))

	// Step 0 + B fixtures: LBT supply 1000, accumulated EOA 100 → SC-locked 900.
	mustSaveJSON(t, dir, "step-0-lbt.json", []LBTEntry{
		{WrappedTokenAddress: tok, OriginNetwork: 1, OriginTokenAddress: orig, Balance: "1000"},
	})
	mustSaveJSON(t, dir, "step-b-accumulated.json", []AccumulatedBalance{
		{WrappedTokenAddress: tok, OriginNetwork: 1, OriginTokenAddress: orig, TotalBalance: "100"},
	})
	mustSaveJSON(t, dir, "step-b-eoa-balances.json", []EOABalance{
		{Address: common.BytesToAddress([]byte("eoa")), ETHBalance: "0", Tokens: []EOATokenBalance{
			{WrappedTokenAddress: tok, OriginNetwork: 1, OriginTokenAddress: orig, Balance: "100"},
		}},
	})

	// Step C: pure compute from the fixtures (nativeSCLockedFromContracts off → no RPC).
	require.NoError(t, runSingleC(context.Background(), &Config{}, dir))
	require.True(t, fileExists(filepath.Join(dir, "step-c-sc-locked-values.json")))

	// Step D: build the certificate from B + C.
	cfg := &Config{
		ExitAddress: common.BytesToAddress([]byte("exit")), DestinationNetwork: 0, L2NetworkID: 1,
		Options: Options{OutputDir: dir},
	}
	require.NoError(t, runSingleD(cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, "step-d-exit-certificate.json")))

	// dispatch through runSingleStep routes to the same handlers.
	require.NoError(t, runSingleStep(context.Background(), "c", cfg))
}

func TestRunSingleCMissingInput(t *testing.T) {
	t.Parallel()
	err := runSingleC(context.Background(), &Config{}, t.TempDir()) // no fixtures → load error
	require.Error(t, err)
}

func TestSaveStepEFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, saveStepEFiles(dir, &StepEResult{
		UnclaimedBridges:  []L1Deposit{{DepositCount: 1}},
		UnclaimedMessages: []L1Deposit{{DepositCount: 2}},
	}))
	require.True(t, fileExists(filepath.Join(dir, "step-e-unclaimed-bridges.json")))
	require.True(t, fileExists(filepath.Join(dir, "step-e-unclaimed-messages.json")))
}

func TestLogPipelineConfig(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		logPipelineConfig(&Config{
			L2RPCURL: "http://l2", L1RPCURL: "http://l1",
			ExitAddress: common.BytesToAddress([]byte("exit")),
			Options:     Options{OutputDir: "/tmp/out", BlockRange: 5000},
		})
	})
}
