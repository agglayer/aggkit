package exit_certificate

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// newRunContext builds a urfave/cli context exposing the flags Run reads (config, step, verbose).
func newRunContext(t *testing.T, args []string) *cli.Context {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("config", "", "")
	fs.String("step", "", "")
	fs.Bool("verbose", false, "")
	require.NoError(t, fs.Parse(args))
	return cli.NewContext(nil, fs, nil)
}

// writeRunnableConfig writes a minimal-but-valid exit_certificate config whose output dir is dir and
// whose RPC endpoints are unreachable, so pipeline steps fail fast.
func writeRunnableConfig(t *testing.T, dir string) string {
	t.Helper()
	cfg := `{
  "l2RpcUrl": "http://127.0.0.1:1",
  "l1RpcUrl": "http://127.0.0.1:1",
  "l2BridgeAddress": "0x1111111111111111111111111111111111111111",
  "exitAddress": "0x2222222222222222222222222222222222222222",
  "targetBlock": "100",
  "options": {
    "useAgglayerAdminToStepFCheck": false,
    "outputDir": "` + dir + `"
  }
}`
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))
	return path
}

func TestRunConfigLoadError(t *testing.T) {
	t.Parallel()
	c := newRunContext(t, []string{"--config", filepath.Join(t.TempDir(), "missing.json")})
	err := Run(c)
	require.ErrorContains(t, err, "load config")
}

func TestRunSingleStepViaRun(t *testing.T) {
	t.Parallel()
	// step "c" needs a prerequisite file that is absent → Run executes its full body (config load,
	// output dir, migrate, parseStepList, runSingleStep) and returns the step's load error.
	dir := t.TempDir()
	c := newRunContext(t, []string{"--config", writeRunnableConfig(t, dir), "--step", "c"})
	require.Error(t, Run(c))
}

func TestRunAllViaRunFailsAtCheck(t *testing.T) {
	t.Parallel()
	// No --step → runAll, which fails fast at Step CHECK because the RPC endpoints are unreachable.
	dir := t.TempDir()
	c := newRunContext(t, []string{"--config", writeRunnableConfig(t, dir)})
	require.Error(t, Run(c))
}

// pipelineFixtures returns LBT entries and a Step B result that together yield a non-empty Step C/D.
func pipelineFixtures() ([]LBTEntry, *StepBResult) {
	tok := common.BytesToAddress([]byte("wrap"))
	orig := common.BytesToAddress([]byte("orig"))
	lbt := []LBTEntry{
		{WrappedTokenAddress: tok, OriginNetwork: 1, OriginTokenAddress: orig, Balance: "1000"},
	}
	b := &StepBResult{
		EOABalances: []EOABalance{
			{Address: common.BytesToAddress([]byte("eoa")), ETHBalance: "0", Tokens: []EOATokenBalance{
				{WrappedTokenAddress: tok, OriginNetwork: 1, OriginTokenAddress: orig, Balance: "100"},
			}},
		},
		Accumulated: []AccumulatedBalance{
			{WrappedTokenAddress: tok, OriginNetwork: 1, OriginTokenAddress: orig, TotalBalance: "100"},
		},
	}
	return lbt, b
}

func TestRunAllStepCAndD(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lbt, bResult := pipelineFixtures()

	cResult, err := runAllStepC(context.Background(), &Config{}, dir, lbt, bResult)
	require.NoError(t, err)
	require.NotEmpty(t, cResult.SCLockedValues)
	require.True(t, fileExists(filepath.Join(dir, fileStepCSCLockedValues)))

	cfg := &Config{
		ExitAddress: common.BytesToAddress([]byte("exit")), DestinationNetwork: 0, L2NetworkID: 1,
		Options: Options{OutputDir: dir},
	}
	dResult, err := runAllStepD(cfg, dir, bResult, cResult)
	require.NoError(t, err)
	require.NotNil(t, dResult.Certificate)
	require.True(t, fileExists(filepath.Join(dir, fileStepDCertificate)))
}

// TestRunAllSkipSCLockedPipeline chains Step C → D → F with skipSCLockedValue=true: Step C still
// runs and persists its output, the certificate omits the SC-locked exit, and the offline Step F
// check still matches thanks to the omitted-amount discount.
func TestRunAllSkipSCLockedPipeline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lbt, bResult := pipelineFixtures()

	cfg := &Config{
		DestinationNetwork: 0, L2NetworkID: 1,
		Options: Options{OutputDir: dir, UseAgglayerAdminToStepFCheck: false, SkipSCLockedValue: true},
	}

	cResult, err := runAllStepC(context.Background(), cfg, dir, lbt, bResult)
	require.NoError(t, err)
	require.NotEmpty(t, cResult.SCLockedValues)
	require.True(t, fileExists(filepath.Join(dir, fileStepCSCLockedValues)))

	dResult, err := runAllStepD(cfg, dir, bResult, cResult)
	require.NoError(t, err)
	// Only the EOA exit remains: the SC-locked exit (LBT 1000 − EOA 100 = 900) is omitted.
	require.Len(t, dResult.Certificate.BridgeExits, 1)

	out, err := runAllStepF(context.Background(), cfg, dir, lbt, cResult.SCLockedValues,
		dResult.Certificate, dResult.Certificate)
	require.NoError(t, err)
	require.Same(t, dResult.Certificate, out)
	require.True(t, fileExists(filepath.Join(dir, fileStepFChecks)))
}

func TestRunAllStepCSkippedNoLBT(t *testing.T) {
	t.Parallel()
	_, bResult := pipelineFixtures()
	cResult, err := runAllStepC(context.Background(), &Config{}, t.TempDir(), nil, bResult)
	require.NoError(t, err)
	require.Empty(t, cResult.SCLockedValues)
}

func TestRunAllStepESkippedNoL1(t *testing.T) {
	t.Parallel()
	cert := emptyCert()
	out, err := runAllStepE(context.Background(), &Config{}, t.TempDir(), cert)
	require.NoError(t, err)
	require.Same(t, cert, out)
}

// TestRunSingleStepDispatchAllSteps drives every step name through runSingleStep with an empty output
// dir and unreachable RPC endpoints: each handler fails fast (missing prerequisite file, or a refused
// RPC connection for the steps that hit the network first). This exercises the full dispatch switch
// and each runSingleX entry/error path without needing a live node.
func TestRunSingleStepDispatchAllSteps(t *testing.T) {
	t.Parallel()
	steps := []string{
		"check", "0", "a", "b", "b1", "b2", "b3", "c", "d",
		"e", "f", "g", "g1", "g2", "h", "i", "sign", "submit", "wait",
	}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{
				// Unreachable endpoints so the network-first steps (check, 0) fail fast.
				L1RPCURL:        "http://127.0.0.1:1",
				L2RPCURL:        "http://127.0.0.1:1",
				L2BridgeAddress: common.HexToAddress("0x1"),
				Options: Options{
					OutputDir: t.TempDir(), BlockRange: 5000, RPCBatchSize: 200, ConcurrencyLimit: 4,
				},
			}
			require.Error(t, runSingleStep(context.Background(), step, cfg))
		})
	}
}

// TestRunAllStepErrorPaths covers the entry + error-return of the pipeline-step wrappers whose steps
// require a reachable node (or agglayer): with unreachable endpoints each returns its wrapped error.
func TestRunAllStepErrorPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := &Config{
		L1RPCURL: "http://127.0.0.1:1", L2RPCURL: "http://127.0.0.1:1",
		L2BridgeAddress: common.HexToAddress("0x1"),
		Options:         Options{OutputDir: t.TempDir(), BlockRange: 5000, RPCBatchSize: 200, ConcurrencyLimit: 4},
	}

	entries, tokens, block, err := resolveOrGenerateLBT(ctx, cfg, cfg.Options.OutputDir)
	require.Error(t, err)
	require.Nil(t, entries)
	require.Nil(t, tokens)
	require.Zero(t, block)

	_, err = runAllStepA(ctx, cfg, cfg.Options.OutputDir, 100, nil)
	require.Error(t, err)

	// Step B with no addresses to scan completes without touching the node (covers the save path).
	_, err = runAllStepB(ctx, cfg, cfg.Options.OutputDir, 100, &StepAResult{})
	require.NoError(t, err)

	_, err = runAllStepG(ctx, cfg, cfg.Options.OutputDir, 100, emptyCert(), nil)
	require.Error(t, err)

	// Step H has no agglayer gRPC URL configured → wrapper returns the "required" error.
	_, err = runAllStepH(ctx, cfg, cfg.Options.OutputDir, &StepGResult{})
	require.Error(t, err)
}

func TestRunAllStepFOffline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Offline Step F with no LBT data is a benign no-op: it returns the final certificate unchanged
	// and contacts no agglayer admin endpoint.
	cfg := &Config{Options: Options{OutputDir: dir, UseAgglayerAdminToStepFCheck: false}}
	stepD := emptyCert()
	final := emptyCert()

	out, err := runAllStepF(context.Background(), cfg, dir, nil, nil, stepD, final)
	require.NoError(t, err)
	require.Same(t, final, out)
	require.True(t, fileExists(filepath.Join(dir, fileStepFChecks)))
}

func TestRunAllStepIAndRunStepI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	leafCount := uint32(10)

	// topics[1] is the indexed leafCount as a 32-byte big-endian value.
	topic1 := common.BytesToHash([]byte{byte(leafCount)})
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
	cert := emptyCert()
	gResult := &StepGResult{NewLocalExitRoot: common.HexToHash("0xbeef")}
	hResult := &StepHResult{PreviousLocalExitRoot: common.HexToHash("0xabcd"), Height: 3}

	require.NoError(t, runAllStepI(context.Background(), cfg, dir, cert, gResult, hResult))
	require.Equal(t, common.HexToHash("0xbeef"), cert.NewLocalExitRoot)
	require.Equal(t, common.HexToHash("0xabcd"), cert.PrevLocalExitRoot)
	require.Equal(t, leafCount, cert.L1InfoTreeLeafCount)
	require.True(t, fileExists(filepath.Join(dir, fileFinalCertificate)))
}

func TestRunStepIGuards(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t,
		RunStepI(context.Background(), &Config{}, nil, &StepGResult{}, nil), "certificate is nil")
	require.ErrorContains(t,
		RunStepI(context.Background(), &Config{}, emptyCert(), nil, nil), "step G result is nil")
}

func TestRunAllStepEFull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srv := newBatchRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthBlockNumber:
			return quoted("0x10"), nil
		case rpcMethodEthGetLogs:
			return json.RawMessage(`[]`), nil
		default:
			return quoted("0x"), nil
		}
	})
	cfg := stepEConfig(srv.URL)
	cfg.Options.OutputDir = dir

	out, err := runAllStepE(context.Background(), cfg, dir, emptyCert())
	require.NoError(t, err)
	require.NotNil(t, out)
	require.True(t, fileExists(filepath.Join(dir, fileStepEUnclaimedBridges)))
}
