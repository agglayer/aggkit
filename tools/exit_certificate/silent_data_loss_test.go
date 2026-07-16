package exit_certificate

// Tests for issue #1713 (AET-06 and the untracked computeNativeBalance case): scan/decode
// failures must fail the step instead of warn-and-continue, which silently omitted value
// (tokens, overrides, the native balance) from the exit certificate. The AET-20 and
// NewWrappedToken decode paths are covered by TestFetchBridgeEventsInRangeDecodeError and
// TestRunStep0MalformedNewWrappedTokenEvent.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// newJSONRPCErrorServer answers every JSON-RPC request (single or batched) with a
// JSON-RPC error of the given code. singleRPC propagates it without HTTP-level
// retries, and batchRPC treats the revert code (3) as permanent, keeping tests fast.
func newJSONRPCErrorServer(t *testing.T, code int, msg string) string {
	t.Helper()
	type rpcReq struct {
		ID json.RawMessage `json:"id"`
	}
	errOf := func(req rpcReq) map[string]any {
		return map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"error": map[string]any{"code": code, "message": msg},
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if len(body) > 0 && body[0] == '[' {
			var reqs []rpcReq
			require.NoError(t, json.Unmarshal(body, &reqs))
			resps := make([]map[string]any, len(reqs))
			for i, req := range reqs {
				resps[i] = errOf(req)
			}
			require.NoError(t, json.NewEncoder(w).Encode(resps))
			return
		}
		var req rpcReq
		require.NoError(t, json.Unmarshal(body, &req))
		require.NoError(t, json.NewEncoder(w).Encode(errOf(req)))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFetchSetSovereignTokenEventsInRangeCorruptEventAborts(t *testing.T) {
	t.Parallel()
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		require.Equal(t, "eth_getLogs", method)
		return []map[string]string{{
			"data":        "0xabcd", // corrupt: too short to decode
			"blockNumber": "0x10",
			"logIndex":    "0x0",
		}}
	})
	_, err := fetchSetSovereignTokenEventsInRange(
		context.Background(), url, common.HexToAddress("0xbridge"), 0, 100)
	require.ErrorContains(t, err, "decode SetSovereignTokenAddress event")
}

func TestFetchNewWrappedTokenEventsScanFailureAborts(t *testing.T) {
	t.Parallel()
	url := newJSONRPCErrorServer(t, eip1474RevertCode, "scan failed")
	cfg := &Config{
		L2RPCURL:        url,
		L2BridgeAddress: common.HexToAddress("0xbridge"),
		Options:         Options{BlockRange: 1000, ConcurrencyLimit: 2},
	}
	_, err := fetchNewWrappedTokenEvents(context.Background(), cfg, 100)
	require.ErrorContains(t, err, "scan failed")
}

func TestRunStep0NativeBalanceFailureAborts(t *testing.T) {
	t.Parallel()
	// Log scans (single requests) succeed with no events; the eth_getBalance pair of
	// computeNativeBalance (a batched request) reverts, so Step 0 must abort instead of
	// building an LBT without the native entry.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if len(body) > 0 && body[0] == '[' {
			var reqs []struct {
				ID json.RawMessage `json:"id"`
			}
			require.NoError(t, json.Unmarshal(body, &reqs))
			resps := make([]map[string]any, len(reqs))
			for i, req := range reqs {
				resps[i] = map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": eip1474RevertCode, "message": "balance unavailable"},
				}
			}
			require.NoError(t, json.NewEncoder(w).Encode(resps))
			return
		}
		var req struct {
			ID json.RawMessage `json:"id"`
		}
		require.NoError(t, json.Unmarshal(body, &req))
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID, "result": []any{},
		}))
	}))
	t.Cleanup(srv.Close)

	cfg := &Config{
		L2RPCURL:        srv.URL,
		L2BridgeAddress: common.HexToAddress("0xbridge"),
		TargetBlock:     *aggkittypes.NewBlockNumber(100),
		Options:         Options{BlockRange: 1000, ConcurrencyLimit: 2, RPCBatchSize: 10},
	}
	_, err := RunStep0(context.Background(), cfg)
	require.ErrorContains(t, err, "compute native balance")
}
