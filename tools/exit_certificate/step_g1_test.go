package exit_certificate

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// newEmptyLogsRPCServer returns a JSON-RPC server that answers every call with an empty array, so the
// lite syncer's eth_getLogs windows resolve to zero BridgeEvents and Sync completes without persisting
// anything. It handles both single and batched requests.
func newEmptyLogsRPCServer(t *testing.T) string {
	t.Helper()
	reply := func(id json.RawMessage) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": id, "result": []any{}}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			var reqs []struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(trimmed, &reqs)
			resps := make([]map[string]any, len(reqs))
			for i, req := range reqs {
				resps[i] = reply(req.ID)
			}
			_ = json.NewEncoder(w).Encode(resps)
			return
		}
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(trimmed, &req)
		_ = json.NewEncoder(w).Encode(reply(req.ID))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestRunStepG1 drives the happy path against a fake L2 that emits no BridgeEvents: the step
// lite-syncs the [0..targetBlock] range, returns the target block as the shadow-fork block, and
// leaves the G1 lite DB on disk for Step G2.
func TestRunStepG1(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Options.BlockRange = 100
	cfg.Options.ConcurrencyLimit = 2
	cfg.L2RPCURL = newEmptyLogsRPCServer(t)

	const targetBlock = uint64(250)
	res, err := RunStepG1(context.Background(), cfg, targetBlock)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, targetBlock, res.ShadowForkBlock)
	require.FileExists(t, g1LiteDBPath(cfg))

	// A pre-existing DB is wiped and re-synced on a second run, still resolving the same block.
	res2, err := RunStepG1(context.Background(), cfg, targetBlock)
	require.NoError(t, err)
	require.Equal(t, targetBlock, res2.ShadowForkBlock)
}

// TestRunStepG1SyncError covers the error path: an unreachable RPC makes the lite sync fail, and the
// failure is surfaced wrapped with the target block context.
func TestRunStepG1SyncError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cfg := testConfig(t)
	cfg.Options.BlockRange = 100
	cfg.Options.ConcurrencyLimit = 2
	cfg.L2RPCURL = srv.URL

	_, err := RunStepG1(context.Background(), cfg, 250)
	require.Error(t, err)
	require.Contains(t, err.Error(), "lite-sync L2 bridges up to block 250")
}

// TestRunStepG1DialError covers the New/dial failure path: an unparsable RPC URL makes
// bridgesyncerlite.New fail before any sync, and RunStepG1 wraps the error.
func TestRunStepG1DialError(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Options.BlockRange = 100
	cfg.L2RPCURL = "://not-a-valid-url"

	_, err := RunStepG1(context.Background(), cfg, 100)
	require.Error(t, err)
	require.Contains(t, err.Error(), "lite-sync L2 bridges up to block 100")
}

// TestSyncLiteToBlockRemovesStaleDB verifies syncLiteToBlock deletes a pre-existing lite DB (so a
// re-run reflects current chain state) before syncing afresh.
func TestSyncLiteToBlockRemovesStaleDB(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Options.BlockRange = 100
	cfg.L2RPCURL = newEmptyLogsRPCServer(t)

	// Drop a stale file where the lite DB will live; syncLiteToBlock must remove and replace it.
	require.NoError(t, os.MkdirAll(cfg.Options.OutputDir, 0o755))
	require.NoError(t, os.WriteFile(g1LiteDBPath(cfg), []byte("stale"), 0o644))

	require.NoError(t, syncLiteToBlock(context.Background(), cfg, 100))
	require.FileExists(t, g1LiteDBPath(cfg))
}
