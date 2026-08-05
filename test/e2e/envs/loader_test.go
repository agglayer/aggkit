package envs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadEnv_InvalidEnvName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env, err := LoadEnv(ctx, ENVName("non-existent-env"))
	require.Error(t, err, "LoadEnv should return an error for non-existent environment")
	require.Nil(t, env, "Env should be nil when error occurs")
}

func TestFindEnvsDir(t *testing.T) {
	// Test that FindEnvsDir works from current directory
	envsDir, err := FindEnvsDir()
	require.NoError(t, err, "FindEnvsDir should not return an error")
	require.NotEmpty(t, envsDir, "envs directory path should not be empty")

	// Verify the directory exists
	info, err := os.Stat(envsDir)
	require.NoError(t, err, "envs directory should exist")
	require.True(t, info.IsDir(), "envs path should be a directory")

	// Verify op-pp subdirectory exists
	opPPDir := filepath.Join(envsDir, string(EnvOpPP))
	info, err = os.Stat(opPPDir)
	require.NoError(t, err, "op-pp directory should exist")
	require.True(t, info.IsDir(), "op-pp path should be a directory")

	// Verify summary.json exists
	summaryPath := filepath.Join(opPPDir, "summary.json")
	_, err = os.Stat(summaryPath)
	require.NoError(t, err, "summary.json should exist")

	// Verify op-pp-2chains subdirectory and its summary.json exist
	opPP2ChainsDir := filepath.Join(envsDir, string(EnvOpPP2Chains))
	info, err = os.Stat(opPP2ChainsDir)
	require.NoError(t, err, "op-pp-2chains directory should exist")
	require.True(t, info.IsDir(), "op-pp-2chains path should be a directory")

	_, err = os.Stat(filepath.Join(opPP2ChainsDir, "summary.json"))
	require.NoError(t, err, "op-pp-2chains summary.json should exist")
}

// TestParseSummary_TwoChains asserts that the staged op-pp-2chains summary.json parses into
// two L2 networks ("001" and "002") with the expected chain IDs (20201, 20202). This is a
// pure-parse test that does not require a live enclave.
func TestParseSummary_TwoChains(t *testing.T) {
	envsDir, err := FindEnvsDir()
	require.NoError(t, err, "FindEnvsDir should not return an error")

	summaryPath := filepath.Join(envsDir, string(EnvOpPP2Chains), "summary.json")
	data, err := os.ReadFile(summaryPath)
	require.NoError(t, err, "should read op-pp-2chains summary.json")

	var summary summaryJSON
	require.NoError(t, json.Unmarshal(data, &summary), "summary.json should unmarshal")

	// L1 stays the same as the single-chain env.
	require.Equal(t, "271828", summary.Networks.L1.ChainID, "L1 chain ID should be 271828")

	// Both L2 networks must be present.
	require.Len(t, summary.Networks.L2Networks, 2, "expected exactly two L2 networks")

	l2A, okA := summary.Networks.L2Networks[l2NetworkKeyA]
	require.True(t, okA, "L2 network %q should be present", l2NetworkKeyA)
	l2B, okB := summary.Networks.L2Networks[l2NetworkKeyB]
	require.True(t, okB, "L2 network %q should be present", l2NetworkKeyB)

	// Chain IDs parse and match the expected values.
	chainIDA, err := parseChainID(l2A.ChainID)
	require.NoError(t, err, "L2A chain ID should parse")
	require.Equal(t, "20201", chainIDA.String(), "L2A chain ID should be 20201")

	chainIDB, err := parseChainID(l2B.ChainID)
	require.NoError(t, err, "L2B chain ID should parse")
	require.Equal(t, "20202", chainIDB.String(), "L2B chain ID should be 20202")

	// Each network must expose its execution-client RPC and bridge-service endpoints.
	require.NotEmpty(t, l2A.l2RPCExternal(), "L2A execution RPC should be set")
	require.NotEmpty(t, l2A.Services.Aggkit.BridgeService.External, "L2A bridge service should be set")
	require.NotEmpty(t, l2B.l2RPCExternal(), "L2B execution RPC should be set")
	require.NotEmpty(t, l2B.Services.Aggkit.BridgeService.External, "L2B bridge service should be set")

	// The two networks must use distinct external endpoints (separate per-chain services).
	require.NotEqual(t, l2A.l2RPCExternal(), l2B.l2RPCExternal(), "L2A and L2B should have distinct RPC endpoints")
	require.NotEqual(t, l2A.Services.Aggkit.BridgeService.External, l2B.Services.Aggkit.BridgeService.External,
		"L2A and L2B should have distinct bridge-service endpoints")
}

// TestParseSummary_SingleChain asserts the existing op-pp summary still parses into exactly one
// L2 network with chain ID 2151908, guarding against regressions to the single-chain path.
func TestParseSummary_SingleChain(t *testing.T) {
	envsDir, err := FindEnvsDir()
	require.NoError(t, err, "FindEnvsDir should not return an error")

	summaryPath := filepath.Join(envsDir, string(EnvOpPP), "summary.json")
	data, err := os.ReadFile(summaryPath)
	require.NoError(t, err, "should read op-pp summary.json")

	var summary summaryJSON
	require.NoError(t, json.Unmarshal(data, &summary), "summary.json should unmarshal")

	require.Len(t, summary.Networks.L2Networks, 1, "op-pp should have exactly one L2 network")
	l2A, okA := summary.Networks.L2Networks[l2NetworkKeyA]
	require.True(t, okA, "L2 network %q should be present", l2NetworkKeyA)

	chainIDA, err := parseChainID(l2A.ChainID)
	require.NoError(t, err, "L2 chain ID should parse")
	require.Equal(t, "2151908", chainIDA.String(), "L2 chain ID should be 2151908")
}
