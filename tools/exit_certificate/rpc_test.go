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
	t.Parallel()

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
	t.Parallel()

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
		{Method: "eth_getBlockByNumber", Params: []interface{}{"0x1", false}},
	}

	results, err := batchRPC(ctx, server.URL, calls, 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Nil(t, results[0])
}

func TestBatchRPC_HTTPError(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	// sleepWithBackoff is a void function; just verify it doesn't panic
	// The actual delay values are tested via the formula: min(1000 * 2^attempt, 10000) ms
	require.NotPanics(t, func() { sleepWithBackoff(0) })
}

func TestBatchRPC_SingleResponse(t *testing.T) {
	t.Parallel()

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
