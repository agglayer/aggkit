package exit_certificate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBatchRPC_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requests []jsonRPCRequest
		err := json.NewDecoder(r.Body).Decode(&requests)
		require.NoError(t, err)
		require.Len(t, requests, 2)

		responses := []jsonRPCResponse{
			{JSONRPC: "2.0", ID: 1, Result: json.RawMessage(`"0x64"`)},
			{JSONRPC: "2.0", ID: 2, Result: json.RawMessage(`"0xc8"`)},
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(responses))
	}))
	defer server.Close()

	ctx := context.Background()
	calls := []RPCCall{
		{Method: "eth_blockNumber", Params: nil},
		{Method: "eth_blockNumber", Params: nil},
	}

	results, err := batchRPC(ctx, server.URL, calls, 1)
	require.NoError(t, err)
	require.Len(t, results, 2)

	var val1, val2 string
	require.NoError(t, json.Unmarshal(results[0], &val1))
	require.NoError(t, json.Unmarshal(results[1], &val2))
	require.Equal(t, "0x64", val1)
	require.Equal(t, "0xc8", val2)
}

func TestBatchRPC_RPCError(t *testing.T) {
	// Single-call batch where the node always returns a per-item RPC error.
	// batchRPC exhausts retries and returns an error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		responses := []jsonRPCResponse{
			{JSONRPC: "2.0", ID: 1, Error: &jsonRPCError{Code: -32000, Message: "not found"}},
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(responses))
	}))
	defer server.Close()

	ctx := context.Background()
	calls := []RPCCall{
		{Method: "eth_getBlockByNumber", Params: []any{"0x1", false}},
	}

	_, err := batchRPC(ctx, server.URL, calls, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "1/1 calls still failing")
}

func TestBatchRPC_MultipleCallsOneError(t *testing.T) {
	// Two-call batch where the second call always returns a per-item RPC error.
	// batchRPC exhausts retries and returns an error — no nil slots are silently accepted.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requests []jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requests))
		responses := make([]jsonRPCResponse, len(requests))
		for i, req := range requests {
			if req.Method == "eth_getBlockByNumber" {
				responses[i] = jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &jsonRPCError{Code: -32000, Message: "not found"}}
			} else {
				responses[i] = jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`"0x1"`)}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(responses))
	}))
	defer server.Close()

	ctx := context.Background()
	calls := []RPCCall{
		{Method: "eth_blockNumber", Params: nil},
		{Method: "eth_getBlockByNumber", Params: []any{"0x999", false}},
	}

	_, err := batchRPC(ctx, server.URL, calls, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "1/2 calls still failing")
}

func TestBatchRPC_RetriesFailedItems(t *testing.T) {
	// Two-call batch: both fail on attempt 1, both succeed on attempt 2.
	// batchRPC must retry only the failed items and return complete results with no error.
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requests []jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requests))
		callCount++
		responses := make([]jsonRPCResponse, len(requests))
		for i, req := range requests {
			if callCount == 1 {
				responses[i] = jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: &jsonRPCError{Code: -32000, Message: "overloaded"}}
			} else {
				responses[i] = jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`"0x1"`)}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(responses))
	}))
	defer server.Close()

	ctx := context.Background()
	calls := []RPCCall{
		{Method: "eth_getBalance", Params: nil},
		{Method: "eth_getBalance", Params: nil},
	}

	results, err := batchRPC(ctx, server.URL, calls, 2)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.NotNil(t, results[0])
	require.NotNil(t, results[1])
	require.Equal(t, 2, callCount)
}

func TestBatchRPC_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	ctx := context.Background()
	calls := []RPCCall{
		{Method: "eth_blockNumber", Params: nil},
	}

	_, err := batchRPC(ctx, server.URL, calls, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestBatchRPC_ContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := []RPCCall{
		{Method: "eth_blockNumber", Params: nil},
	}

	_, err := batchRPC(ctx, server.URL, calls, 1)
	require.Error(t, err)
}

func TestSingleRPC_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`"0x100"`),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := context.Background()
	result, err := singleRPC(ctx, server.URL, "eth_blockNumber", nil, 1)
	require.NoError(t, err)

	var val string
	require.NoError(t, json.Unmarshal(result, &val))
	require.Equal(t, "0x100", val)
}

func TestSingleRPC_RPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Error:   &jsonRPCError{Code: -32600, Message: "invalid request"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := context.Background()
	_, err := singleRPC(ctx, server.URL, "eth_blockNumber", nil, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid request")
}

func TestSleepWithBackoff(t *testing.T) {
	require.NotPanics(t, func() { sleepWithBackoff(context.Background(), 0) })
}

func TestSleepWithBackoff_ContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call

	start := time.Now()
	sleepWithBackoff(ctx, 1) // attempt 1 → 2000 ms without context awareness
	require.Less(t, time.Since(start), 100*time.Millisecond, "sleepWithBackoff must return immediately when context is cancelled")
}

func TestSingleRPCAuth_SendsBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer my-iap-token", r.Header.Get("Authorization"))
		resp := jsonRPCResponse{JSONRPC: "2.0", ID: 1, Result: json.RawMessage(`"ok"`)}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := context.Background()
	result, err := singleRPCAuth(ctx, server.URL, "test_method", nil, 1, "my-iap-token")
	require.NoError(t, err)
	var val string
	require.NoError(t, json.Unmarshal(result, &val))
	require.Equal(t, "ok", val)
}

func TestSingleRPCAuth_NoTokenSendsNoHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		resp := jsonRPCResponse{JSONRPC: "2.0", ID: 1, Result: json.RawMessage(`"ok"`)}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := context.Background()
	_, err := singleRPCAuth(ctx, server.URL, "test_method", nil, 1, "")
	require.NoError(t, err)
}

func TestHttpGetJSON_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"value"}`))
	}))
	defer server.Close()

	ctx := context.Background()
	body, err := httpGetJSON(ctx, server.URL)
	require.NoError(t, err)
	var result map[string]string
	require.NoError(t, json.Unmarshal(body, &result))
	require.Equal(t, "value", result["key"])
}

func TestHttpGetJSON_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer server.Close()

	ctx := context.Background()
	_, err := httpGetJSON(ctx, server.URL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "404")
}

func TestMaskRPCURL(t *testing.T) {
	require.Equal(t, "https://node.example.com", maskRPCURL("https://node.example.com/api/v1?key=secret"))
	require.Equal(t, "http://localhost:8545", maskRPCURL("http://localhost:8545/"))
	require.Equal(t, "bad url", maskRPCURL("bad url"))
}

func TestRPCExecutionError_WithData(t *testing.T) {
	e := &RPCExecutionError{Code: -32000, Message: "execution reverted", Data: "0xdeadbeef"}
	require.Contains(t, e.Error(), "execution reverted")
	require.Contains(t, e.Error(), "0xdeadbeef")
}

func TestRPCExecutionError_WithoutData(t *testing.T) {
	e := &RPCExecutionError{Code: -32000, Message: "execution reverted"}
	require.Equal(t, "RPC error: execution reverted", e.Error())
}

func TestConcurrentBatchRPC_Basic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var requests []jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requests))
		responses := make([]jsonRPCResponse, len(requests))
		for i, req := range requests {
			responses[i] = jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`"0x1"`)}
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(responses))
	}))
	defer server.Close()

	ctx := context.Background()
	calls := make([]RPCCall, 5)
	for i := range calls {
		calls[i] = RPCCall{Method: "eth_blockNumber", Params: nil}
	}

	results, err := concurrentBatchRPC(ctx, server.URL, calls, 2, 2, "test")
	require.NoError(t, err)
	require.Len(t, results, 5)
	for _, r := range results {
		require.NotNil(t, r)
	}
}

func TestDoRPCWithRetry_ExhaustsRetries(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx := context.Background()
	body, _ := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", Method: "eth_blockNumber", ID: 1})
	_, err := doRPCWithRetry(ctx, server.URL, body, 2, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "RPC failed after 2 attempts")
	require.Equal(t, 2, attempts)
}

func TestConcurrentBatchRPC_Empty(t *testing.T) {
	results, err := concurrentBatchRPC(context.Background(), "http://unused", nil, 10, 2, "test")
	require.NoError(t, err)
	require.Nil(t, results)
}

func TestBatchRPC_SingleResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`"0x42"`),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx := context.Background()
	calls := []RPCCall{
		{Method: "eth_blockNumber", Params: nil},
	}

	results, err := batchRPC(ctx, server.URL, calls, 1)
	require.NoError(t, err)
	require.Len(t, results, 1)

	var val string
	require.NoError(t, json.Unmarshal(results[0], &val))
	require.Equal(t, "0x42", val)
}
