package exit_certificate

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// step0SuccessConfig wires a Config to a step0Stub so RunStep0 (and its run.go wrappers) succeed.
func step0SuccessConfig(t *testing.T, dir string) *Config {
	t.Helper()
	url := step0Stub(t, makeWrappedTokenData(1,
		common.BytesToAddress([]byte("origin")), common.BytesToAddress([]byte("wrapped"))))
	return &Config{
		L2RPCURL:        url,
		L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		TargetBlock:     *aggkittypes.NewBlockNumber(100),
		Options:         Options{OutputDir: dir, BlockRange: 50, ConcurrencyLimit: 2, RPCBatchSize: 10},
	}
}

func TestRunSingle0Success(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := step0SuccessConfig(t, dir)

	require.NoError(t, runSingle0(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStep0TargetBlock)))
	require.True(t, fileExists(filepath.Join(dir, fileStep0LBT)))

	// Dispatch through runSingleStep routes to the same handler.
	require.NoError(t, runSingleStep(context.Background(), "0", cfg))
}

func TestRunSingleFOffline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustSaveJSON(t, dir, fileStepDCertificate, map[string]any{"network_id": 1})
	cfg := &Config{Options: Options{OutputDir: dir, UseAgglayerAdminToStepFCheck: false}}

	require.NoError(t, runSingleF(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStepFChecks)))
}

func TestRunSingleISuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustSaveJSON(t, dir, fileStepGReorderedCertificate, map[string]any{"network_id": 1})
	mustSaveJSON(t, dir, fileStepGNewLocalExitRoot, StepGResult{NewLocalExitRoot: common.HexToHash("0xbeef")})
	mustSaveJSON(t, dir, fileStepHPreviousLocalExitRoot, StepHResult{PreviousLocalExitRoot: common.HexToHash("0xabcd"), Height: 2})

	topic1 := common.BytesToHash([]byte{0x0a})
	srv := newBatchRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthBlockNumber:
			return quoted("0x100"), nil
		case rpcMethodEthGetLogs:
			out, _ := json.Marshal([]map[string]any{{
				"topics": []string{updateL1InfoTreeV2Topic.Hex(), topic1.Hex()},
			}})
			return out, nil
		default:
			return quoted("0x"), nil
		}
	})
	cfg := &Config{
		L1RPCURL:                srv.URL,
		L1GlobalExitRootAddress: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Options:                 Options{OutputDir: dir, BlockRange: 5000},
	}

	require.NoError(t, runSingleI(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileFinalCertificate)))
}

func TestResolveOrGenerateLBTSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := step0SuccessConfig(t, dir)

	entries, tokens, targetBlock, err := resolveOrGenerateLBT(context.Background(), cfg, dir)
	require.NoError(t, err)
	require.Equal(t, uint64(100), targetBlock)
	require.NotEmpty(t, entries)
	require.NotEmpty(t, tokens)
	require.True(t, fileExists(filepath.Join(dir, fileStep0LBT)))
}
