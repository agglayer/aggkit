package exit_certificate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestRunSingleMissingInputs covers the load-from-disk guard of each single-step orchestrator that
// reads a prerequisite file before doing any RPC: with an empty output dir they fail fast.
func TestRunSingleMissingInputs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := map[string]func(dir string) error{
		"submit": func(dir string) error { return runSingleSubmit(ctx, &Config{}, dir) },
		"wait":   func(dir string) error { return runSingleWait(ctx, &Config{}, dir) },
		"f":      func(dir string) error { return runSingleF(ctx, &Config{}, dir) },
		"h":      func(dir string) error { return runSingleH(ctx, &Config{}, dir) },
		"i":      func(dir string) error { return runSingleI(ctx, &Config{}, dir) },
		"g1":     func(dir string) error { return runSingleG1(ctx, &Config{}, dir) },
		"g2":     func(dir string) error { return runSingleG2(ctx, &Config{}, dir) },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, fn(t.TempDir()))
		})
	}
}

func TestRunSingleE_RequiresL1RPC(t *testing.T) {
	t.Parallel()
	err := runSingleE(context.Background(), &Config{}, t.TempDir())
	require.Error(t, err)
	require.Contains(t, err.Error(), "l1RpcUrl")
}

func TestRunSingleSign(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("missing certificate", func(t *testing.T) {
		t.Parallel()
		err := runSingleSign(ctx, &Config{}, t.TempDir())
		require.Error(t, err)
		require.Contains(t, err.Error(), "load final certificate")
	})

	t.Run("requires signer method", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		mustSaveJSON(t, dir, fileFinalCertificate, map[string]any{}) // valid empty cert
		err := runSingleSign(ctx, &Config{Options: Options{OutputDir: dir}}, dir)
		require.Error(t, err)
		require.Contains(t, err.Error(), "signerConfig.Method")
	})
}

func TestResolveLatestBlock(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, "eth_blockNumber", method)
			return quoted("0x1a4"), nil
		})
		n, err := resolveLatestBlock(context.Background(), srv.URL)
		require.NoError(t, err)
		require.Equal(t, uint64(420), n)
	})

	t.Run("rpc error", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return nil, revertErr()
		})
		_, err := resolveLatestBlock(context.Background(), srv.URL)
		require.Error(t, err)
	})
}

// TestRunSingleG2_EmptyCertificate drives runSingleG2 end-to-end without Anvil: a certificate with no
// bridge exits short-circuits RunStepG2 to the canonical EmptyLER (it only reads the initial LER from
// the L2 RPC, which the stub serves), so both output files are written.
func TestRunSingleG2_EmptyCertificate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bridge := common.HexToAddress("0xabcabcabcabcabcabcabcabcabcabcabcabcabca")

	// Stub L2 RPC: getRoot() returns a zero root so readLocalExitRoot succeeds (no retries/backoff).
	rootOut, err := bridgeABI.Methods["getRoot"].Outputs.Pack([32]byte{})
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: hexResult(rootOut)}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	mustSaveJSON(t, dir, fileStepG1ShadowForkBlock, StepG1Result{ShadowForkBlock: 100})
	mustSaveJSON(t, dir, fileStepECertificate, map[string]any{}) // empty cert → no bridge exits

	cfg := &Config{
		L2RPCURL:        srv.URL,
		L2BridgeAddress: bridge,
		Options:         Options{OutputDir: dir, VerifyNewLocalExitRootUsingShadowFork: false},
	}
	require.NoError(t, runSingleG2(context.Background(), cfg, dir))
	require.True(t, fileExists(filepath.Join(dir, fileStepGNewLocalExitRoot)))
	require.True(t, fileExists(filepath.Join(dir, fileStepGReorderedCertificate)))
}
