package exit_certificate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const testAddr1 = "0x1000000000000000000000000000000000000001"

// newTraceServer returns a test server that responds to debug_traceTransaction.
// The handler receives the tx hash (from params[0]) and returns the result/error
// provided by the given responder function.
func newTraceServer(t *testing.T, responder func(txHex string) jsonRPCResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params []json.RawMessage `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		var txHex string
		require.NoError(t, json.Unmarshal(req.Params[0], &txHex))

		resp := responder(txHex)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
}

func TestTraceOneTransaction_Success(t *testing.T) {
	t.Parallel()

	addr1 := testAddr1
	addr2 := "0x2000000000000000000000000000000000000002"
	addr3 := "0x3000000000000000000000000000000000000003"

	server := newTraceServer(t, func(_ string) jsonRPCResponse {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`{"pre":{"` + addr1 + `":{},"` + addr2 + `":{}},"post":{"` + addr3 + `":{}}}`),
		}
	})
	defer server.Close()

	addrs, err := traceOneTransaction(context.Background(), server.URL, common.HexToHash("0xabc"))
	require.NoError(t, err)
	require.Len(t, addrs, 3)

	addrSet := make(map[common.Address]struct{}, len(addrs))
	for _, a := range addrs {
		addrSet[a] = struct{}{}
	}
	require.Contains(t, addrSet, common.HexToAddress(addr1))
	require.Contains(t, addrSet, common.HexToAddress(addr2))
	require.Contains(t, addrSet, common.HexToAddress(addr3))
}

// An address that appears in both pre and post must be deduplicated.
func TestTraceOneTransaction_DeduplicatesPreAndPost(t *testing.T) {
	t.Parallel()

	addr := testAddr1

	server := newTraceServer(t, func(_ string) jsonRPCResponse {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`{"pre":{"` + addr + `":{}},"post":{"` + addr + `":{}}}`),
		}
	})
	defer server.Close()

	addrs, err := traceOneTransaction(context.Background(), server.URL, common.HexToHash("0xabc"))
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	require.Equal(t, common.HexToAddress(addr), addrs[0])
}

// An RPC-level error must be wrapped with the tx hash and propagated.
func TestTraceOneTransaction_RPCError(t *testing.T) {
	t.Parallel()

	server := newTraceServer(t, func(_ string) jsonRPCResponse {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &jsonRPCError{Code: -32000, Message: "transaction not found"},
		}
	})
	defer server.Close()

	txHash := common.HexToHash("0xdeadbeef")
	_, err := traceOneTransaction(context.Background(), server.URL, txHash)
	require.Error(t, err)
	require.ErrorContains(t, err, "transaction not found")
	require.ErrorContains(t, err, txHash.Hex())
}

// A valid RPC response whose result can't be decoded as a trace must return an unmarshal error.
func TestTraceOneTransaction_BadJSONResult(t *testing.T) {
	t.Parallel()

	server := newTraceServer(t, func(_ string) jsonRPCResponse {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`"not-an-object"`),
		}
	})
	defer server.Close()

	txHash := common.HexToHash("0xbadf00d")
	_, err := traceOneTransaction(context.Background(), server.URL, txHash)
	require.Error(t, err)
	require.ErrorContains(t, err, "unmarshal trace")
}

// A response with result:null alongside an error field must still propagate the error
// (some nodes return both fields simultaneously when the handler crashes).
func TestTraceOneTransaction_NullResultWithError(t *testing.T) {
	t.Parallel()

	server := newTraceServer(t, func(_ string) jsonRPCResponse {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage("null"),
			Error:   &jsonRPCError{Code: -32000, Message: "method handler crashed"},
		}
	})
	defer server.Close()

	txHash := common.HexToHash("0xcafe")
	_, err := traceOneTransaction(context.Background(), server.URL, txHash)
	require.Error(t, err)
	require.ErrorContains(t, err, "method handler crashed")
	require.ErrorContains(t, err, txHash.Hex())
}

// When continueOnError=true, failed traces are collected in failedTraces and
// do not abort the run; successful traces still return their addresses.
func TestTraceTransactions_ContinueOnError_CollectsFailed(t *testing.T) {
	t.Parallel()

	goodHash := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000001111")
	badHash := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000002222")
	addrGood := testAddr1

	server := newTraceServer(t, func(txHex string) jsonRPCResponse {
		if txHex == goodHash.Hex() {
			return jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      1,
				Result:  json.RawMessage(`{"pre":{"` + addrGood + `":{}},"post":{}}`),
			}
		}
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &jsonRPCError{Code: -32000, Message: "trace failed"},
		}
	})
	defer server.Close()

	addrs, failed, err := traceTransactions(
		context.Background(), server.URL,
		[]common.Hash{goodHash, badHash}, 1, true,
	)
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	require.Equal(t, common.HexToAddress(addrGood), addrs[0])
	require.Len(t, failed, 1)
	require.Equal(t, badHash, failed[0].Hash)
}

// When continueOnError=false, the first trace failure aborts the run.
func TestTraceTransactions_AbortOnError(t *testing.T) {
	t.Parallel()

	server := newTraceServer(t, func(_ string) jsonRPCResponse {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &jsonRPCError{Code: -32000, Message: "archive node required"},
		}
	})
	defer server.Close()

	_, _, err := traceTransactions(
		context.Background(), server.URL,
		[]common.Hash{common.HexToHash("0x9999")}, 1, false,
	)
	require.Error(t, err)
	require.ErrorContains(t, err, "trace failures")
}

// TestRunStepA_AbortOnTraceError verifies that RunStepA returns an error (and does not
// silently continue) when a debug_traceTransaction call fails and ContinueOnTraceError=false.
//
// Before the fix, collectResults drained every result from the worker pool before
// returning the error — i.e. all remaining transactions in the window were still traced.
// The fix adds context cancellation so in-flight workers abort as soon as the first
// failure is detected.
func TestRunStepA_AbortOnTraceError(t *testing.T) {
	t.Parallel()

	const txHex = "0x0000000000000000000000000000000000000000000000000000000000001234"

	// The server must handle two call shapes:
	//   • batch  (body starts with '[') — eth_getBlockByNumber from scanBlockHeaders
	//   • single (body starts with '{') — debug_traceTransaction from traceOneTransaction
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")

		if len(body) > 0 && body[0] == '[' {
			var reqs []jsonRPCRequest
			require.NoError(t, json.Unmarshal(body, &reqs))
			resps := make([]jsonRPCResponse, len(reqs))
			for i, req := range reqs {
				resps[i] = jsonRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  json.RawMessage(`{"transactions":["` + txHex + `"]}`),
				}
			}
			require.NoError(t, json.NewEncoder(w).Encode(resps))
			return
		}

		// Single request: always fail the trace.
		require.NoError(t, json.NewEncoder(w).Encode(jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &jsonRPCError{Code: -32000, Message: "trace not available"},
		}))
	}))
	defer server.Close()

	cfg := &Config{
		L2RPCURL: server.URL,
		Options: Options{
			L2StartBlock:         0,
			StepAWindowSize:      1,
			RPCBatchSize:         1,
			ConcurrencyLimit:     1,
			ContinueOnTraceError: false,
		},
	}

	_, err := RunStepA(context.Background(), cfg, 0)
	require.Error(t, err)
	require.ErrorContains(t, err, "trace transactions")
	require.ErrorContains(t, err, "trace not available")
}

func TestHexToUint64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected uint64
	}{
		{"zero", "0x0", 0},
		{"simple", "0x1", 1},
		{"hex value", "0xff", 255},
		{"no prefix", "ff", 255},
		{"block number", "0x1a2b3c", 1715004},
		{"large", "0xFFFFFFFF", 4294967295},
		{"mixed case", "0xAbCdEf", 11259375},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := hexToUint64(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
