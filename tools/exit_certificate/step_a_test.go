package exit_certificate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// decodeBody reads the full request body so the handler can branch on batch ('[') vs single ('{').
func decodeBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	return body
}

const (
	stepAAddr1 = "0x1000000000000000000000000000000000000001"
	stepAAddr2 = "0x2000000000000000000000000000000000000002"
	stepAAddr3 = "0x3000000000000000000000000000000000000003"
	stepAToken = "0x4000000000000000000000000000000000000004"

	rpcMethodDebugAccountRange = "debug_accountRange"
	rpcMethodEthGetTxReceipt   = "eth_getTransactionReceipt"
)

// topicForAddr left-pads a 20-byte address into a 32-byte indexed event topic.
func topicForAddr(addr string) string {
	return "0x000000000000000000000000" + addr[2:]
}

func encodeRPC(t *testing.T, id int, result string) []byte {
	t.Helper()
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: json.RawMessage(result)}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	return b
}

func TestCollectAccountsViaStateDump_Paginates(t *testing.T) {
	t.Parallel()

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, rpcMethodDebugAccountRange, req.Method)
		calls++
		w.Header().Set("Content-Type", "application/json")

		var result string
		if calls == 1 {
			// Non-zero base64 next cursor → tool must request a second page.
			next := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03})
			result = `{"root":"0x0","accounts":{` +
				`"` + stepAAddr1 + `":{"address":"` + stepAAddr1 + `"},` +
				`"` + stepAAddr2 + `":{"address":"` + stepAAddr2 + `"}},"next":"` + next + `"}`
		} else {
			result = `{"root":"0x0","accounts":{` +
				`"` + stepAAddr3 + `":{"address":"` + stepAAddr3 + `"}},"next":""}`
		}
		_, _ = w.Write(encodeRPC(t, req.ID, result))
	}))
	defer server.Close()

	cfg := &Config{L2RPCURL: server.URL}
	addrs, err := collectAccountsViaStateDump(context.Background(), cfg, 100)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "must paginate until next is empty")
	require.ElementsMatch(t, []common.Address{
		common.HexToAddress(stepAAddr1),
		common.HexToAddress(stepAAddr2),
		common.HexToAddress(stepAAddr3),
	}, addrs)
}

func TestFetchTransferHoldersInRange_ExtractsFromAndTo(t *testing.T) {
	t.Parallel()

	zeroTopic := topicForAddr("0x0000000000000000000000000000000000000000")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, rpcMethodEthGetLogs, req.Method)
		w.Header().Set("Content-Type", "application/json")
		// One mint (from = zero address, filtered out) and one transfer addr1 → addr2.
		logs := `[` +
			`{"topics":["` + transferTopic.Hex() + `","` + zeroTopic + `","` + topicForAddr(stepAAddr1) + `"]},` +
			`{"topics":["` + transferTopic.Hex() + `","` + topicForAddr(stepAAddr1) + `","` + topicForAddr(stepAAddr2) + `"]}` +
			`]`
		_, _ = w.Write(encodeRPC(t, req.ID, logs))
	}))
	defer server.Close()

	addrs, err := fetchTransferHoldersInRange(
		context.Background(), server.URL, common.HexToAddress(stepAToken), 0, 100)
	require.NoError(t, err)
	require.ElementsMatch(t, []common.Address{
		common.HexToAddress(stepAAddr1),
		common.HexToAddress(stepAAddr1),
		common.HexToAddress(stepAAddr2),
	}, addrs, "from and to of each log are collected; the zero address is filtered out")
}

// RunStepA in "both" mode merges the state-dump accounts with the Transfer-log holders.
func TestRunStepA_Both_MergesSources(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case rpcMethodDebugAccountRange:
			result := `{"root":"0x0","accounts":{"` + stepAAddr1 + `":{"address":"` + stepAAddr1 + `"}},"next":""}`
			_, _ = w.Write(encodeRPC(t, req.ID, result))
		case rpcMethodEthGetLogs:
			// Holder addr2 received the wrapped token but is not an account in the dump.
			logs := `[{"topics":["` + transferTopic.Hex() + `","` +
				topicForAddr(stepAAddr1) + `","` + topicForAddr(stepAAddr2) + `"]}]`
			_, _ = w.Write(encodeRPC(t, req.ID, logs))
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()

	cfg := &Config{
		L2RPCURL: server.URL,
		Options: Options{
			AddressDiscovery: addressDiscoveryBoth,
			BlockRange:       defaultBlockRange,
			ConcurrencyLimit: 2,
			L2StartBlock:     0,
		},
	}
	wrappedTokens := []WrappedToken{{WrappedTokenAddress: common.HexToAddress(stepAToken)}}

	result, err := RunStepA(context.Background(), cfg, 10, wrappedTokens)
	require.NoError(t, err)
	require.Equal(t, []common.Address{
		common.HexToAddress(stepAAddr1),
		common.HexToAddress(stepAAddr2),
	}, result.Addresses, "addresses are merged, de-duplicated and sorted")
}

// In "auto" mode, when debug_accountRange is unavailable the step falls back to receipt
// harvesting (block bodies + receipts) and still collects Transfer-log holders.
func TestRunStepA_Auto_FallsBackToReceipts(t *testing.T) {
	t.Parallel()

	txHash := "0x0000000000000000000000000000000000000000000000000000000000001234"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		w.Header().Set("Content-Type", "application/json")

		// Batch request (block headers) — body is a JSON array.
		if len(body) > 0 && body[0] == '[' {
			var reqs []jsonRPCRequest
			require.NoError(t, json.Unmarshal(body, &reqs))
			resps := make([]jsonRPCResponse, len(reqs))
			for i, req := range reqs {
				resps[i] = jsonRPCResponse{
					JSONRPC: "2.0", ID: req.ID,
					Result: json.RawMessage(`{"transactions":["` + txHash + `"]}`),
				}
			}
			b, err := json.Marshal(resps)
			require.NoError(t, err)
			_, _ = w.Write(b)
			return
		}

		var req jsonRPCRequest
		require.NoError(t, json.Unmarshal(body, &req))
		switch req.Method {
		case rpcMethodDebugAccountRange:
			// Simulate a node without the debug_accountRange method.
			resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID,
				Error: &jsonRPCError{Code: -32601, Message: "the method " + rpcMethodDebugAccountRange + " does not exist"}}
			b, err := json.Marshal(resp)
			require.NoError(t, err)
			_, _ = w.Write(b)
		case rpcMethodEthGetTxReceipt:
			receipt := `{"from":"` + stepAAddr1 + `","to":"` + stepAAddr3 +
				`","contractAddress":null,"logs":[]}`
			_, _ = w.Write(encodeRPC(t, req.ID, receipt))
		case rpcMethodEthGetLogs:
			logs := `[{"topics":["` + transferTopic.Hex() + `","` +
				topicForAddr(stepAAddr1) + `","` + topicForAddr(stepAAddr2) + `"]}]`
			_, _ = w.Write(encodeRPC(t, req.ID, logs))
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()

	cfg := &Config{
		L2RPCURL: server.URL,
		Options: Options{
			AddressDiscovery: addressDiscoveryAuto,
			BlockRange:       defaultBlockRange,
			StepAWindowSize:  10,
			RPCBatchSize:     10,
			ConcurrencyLimit: 2,
			L2StartBlock:     0,
		},
	}
	wrappedTokens := []WrappedToken{{WrappedTokenAddress: common.HexToAddress(stepAToken)}}

	result, err := RunStepA(context.Background(), cfg, 5, wrappedTokens)
	require.NoError(t, err)
	// addr1, addr3 from the receipt; addr2 from the Transfer log.
	require.ElementsMatch(t, []common.Address{
		common.HexToAddress(stepAAddr1),
		common.HexToAddress(stepAAddr2),
		common.HexToAddress(stepAAddr3),
	}, result.Addresses)
}

func TestDecodeNextKey(t *testing.T) {
	t.Parallel()

	t.Run("empty means done", func(t *testing.T) {
		t.Parallel()
		b, err := decodeNextKey("")
		require.NoError(t, err)
		require.Empty(t, b)
	})
	t.Run("base64", func(t *testing.T) {
		t.Parallel()
		b, err := decodeNextKey(base64.StdEncoding.EncodeToString([]byte{0xde, 0xad}))
		require.NoError(t, err)
		require.Equal(t, []byte{0xde, 0xad}, b)
	})
	t.Run("hex", func(t *testing.T) {
		t.Parallel()
		b, err := decodeNextKey("0xdead")
		require.NoError(t, err)
		require.Equal(t, []byte{0xde, 0xad}, b)
	})
	t.Run("all-zero means done", func(t *testing.T) {
		t.Parallel()
		b, err := decodeNextKey(base64.StdEncoding.EncodeToString([]byte{0x00, 0x00}))
		require.NoError(t, err)
		require.Empty(t, b)
	})
}

func TestNormalizeAddressDiscovery(t *testing.T) {
	t.Parallel()

	require.Equal(t, addressDiscoveryAuto, normalizeAddressDiscovery(""))
	require.Equal(t, addressDiscoveryAuto, normalizeAddressDiscovery("nonsense"))
	require.Equal(t, addressDiscoveryStateDump, normalizeAddressDiscovery(addressDiscoveryStateDump))
	require.Equal(t, addressDiscoveryLogs, normalizeAddressDiscovery(addressDiscoveryLogs))
	require.Equal(t, addressDiscoveryBoth, normalizeAddressDiscovery(addressDiscoveryBoth))
}

func TestAccountAddress(t *testing.T) {
	t.Parallel()

	inner := stepAAddr2
	// Inner address field takes precedence.
	addr, ok := accountAddress(stepAAddr1, &inner)
	require.True(t, ok)
	require.Equal(t, common.HexToAddress(stepAAddr2), addr)

	// Falls back to the map key when the inner field is absent.
	addr, ok = accountAddress(stepAAddr1, nil)
	require.True(t, ok)
	require.Equal(t, common.HexToAddress(stepAAddr1), addr)

	// Non-address key with no inner field → not ok.
	_, ok = accountAddress("not-an-address", nil)
	require.False(t, ok)
}

func TestAccountRangeParams_DialectShapes(t *testing.T) {
	t.Parallel()

	start := []byte{0x01, 0x02}

	erigon := accountRangeParams("0x10", start, 5000, dialectErigon)
	require.Len(t, erigon, 5, "erigon takes 5 args (no incompletes)")
	require.Equal(t, base64.StdEncoding.EncodeToString(start), erigon[1], "erigon start is base64")

	geth := accountRangeParams("0x10", start, 5000, dialectGeth)
	require.Len(t, geth, 6, "geth takes 6 args (incompletes)")
	require.Equal(t, "0x0102", geth[1], "geth start is 0x-hex")
	require.Equal(t, false, geth[5], "geth incompletes=false")
}

// A node that returns an empty accounts map without an RPC error (e.g. stock geth without
// preimages) must be rejected so callers can fall back instead of trusting an empty dump.
func TestCollectAccountsViaStateDump_ZeroAccountsErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		_, _ = w.Write(encodeRPC(t, req.ID, `{"root":"0x0","accounts":{},"next":""}`))
	}))
	defer server.Close()

	cfg := &Config{L2RPCURL: server.URL}
	_, err := collectAccountsViaStateDump(context.Background(), cfg, 100)
	require.Error(t, err)
	require.ErrorContains(t, err, "0 accounts")
}

// If the node never returns an empty "next" cursor, the dump must fail loudly once the page cap is
// reached rather than silently returning a truncated set.
func TestCollectAccountsViaStateDump_TruncationErrors(t *testing.T) {
	orig := maxAccountRangePages
	maxAccountRangePages = 2
	defer func() { maxAccountRangePages = orig }()

	next := base64.StdEncoding.EncodeToString([]byte{0x01})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		// Always one account and a non-empty cursor → the dump never completes.
		body := `{"root":"0x0","accounts":{"` + stepAAddr1 + `":{"address":"` + stepAAddr1 + `"}},"next":"` + next + `"}`
		_, _ = w.Write(encodeRPC(t, req.ID, body))
	}))
	defer server.Close()

	cfg := &Config{L2RPCURL: server.URL}
	_, err := collectAccountsViaStateDump(context.Background(), cfg, 100)
	require.Error(t, err)
	require.ErrorContains(t, err, "did not complete")
}

func TestRunStepA_StateDumpOnly(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, rpcMethodDebugAccountRange, req.Method, "stateDump mode must not scan logs")
		_, _ = w.Write(encodeRPC(t, req.ID,
			`{"root":"0x0","accounts":{"`+stepAAddr1+`":{"address":"`+stepAAddr1+`"}},"next":""}`))
	}))
	defer server.Close()

	cfg := &Config{L2RPCURL: server.URL, Options: Options{AddressDiscovery: addressDiscoveryStateDump}}
	result, err := RunStepA(context.Background(), cfg, 10, nil)
	require.NoError(t, err)
	require.Equal(t, []common.Address{common.HexToAddress(stepAAddr1)}, result.Addresses)
}

func TestRunStepA_LogsOnly(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, rpcMethodEthGetLogs, req.Method, "logs mode must not dump state")
		_, _ = w.Write(encodeRPC(t, req.ID,
			`[{"topics":["`+transferTopic.Hex()+`","`+topicForAddr(stepAAddr1)+`","`+topicForAddr(stepAAddr2)+`"]}]`))
	}))
	defer server.Close()

	cfg := &Config{
		L2RPCURL: server.URL,
		Options:  Options{AddressDiscovery: addressDiscoveryLogs, BlockRange: defaultBlockRange, ConcurrencyLimit: 2},
	}
	wrappedTokens := []WrappedToken{{WrappedTokenAddress: common.HexToAddress(stepAToken)}}
	result, err := RunStepA(context.Background(), cfg, 10, wrappedTokens)
	require.NoError(t, err)
	require.Equal(t, []common.Address{
		common.HexToAddress(stepAAddr1), common.HexToAddress(stepAAddr2),
	}, result.Addresses)
}

// The Transfer-log scan must start at block 0 regardless of l2StartBlock, so token-only holders
// that received before l2StartBlock are not dropped.
func TestCollectTokenHoldersViaLogs_ScansFromGenesis(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var fromBlocks []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int              `json:"id"`
			Params []map[string]any `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		fromBlock, ok := req.Params[0]["fromBlock"].(string)
		require.True(t, ok)
		mu.Lock()
		fromBlocks = append(fromBlocks, fromBlock)
		mu.Unlock()
		_, _ = w.Write(encodeRPC(t, req.ID, `[]`))
	}))
	defer server.Close()

	cfg := &Config{
		L2RPCURL: server.URL,
		Options:  Options{BlockRange: defaultBlockRange, ConcurrencyLimit: 4, L2StartBlock: 1_000_000},
	}
	wrappedTokens := []WrappedToken{{WrappedTokenAddress: common.HexToAddress(stepAToken)}}
	_, err := collectTokenHoldersViaLogs(context.Background(), cfg, 10, wrappedTokens)
	require.NoError(t, err)
	require.Contains(t, fromBlocks, "0x0", "scan must start at genesis, not l2StartBlock")
}

func TestDebugAccountRange_Errors(t *testing.T) {
	t.Parallel()

	// RPC-level error is wrapped and propagated.
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{
			JSONRPC: "2.0", ID: req.ID, Error: &jsonRPCError{Code: -32601, Message: "method not found"},
		})
	}))
	defer errServer.Close()
	_, err := debugAccountRange(context.Background(), errServer.URL, "0x1", nil, 10, dialectErigon)
	require.ErrorContains(t, err, "debug_accountRange")

	// Non-JSON result yields an unmarshal error.
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		_, _ = w.Write(encodeRPC(t, req.ID, `"not-an-object"`))
	}))
	defer badServer.Close()
	_, err = debugAccountRange(context.Background(), badServer.URL, "0x1", nil, 10, dialectGeth)
	require.ErrorContains(t, err, "unmarshal")
}

func TestAccountRangeDialectString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "geth", dialectGeth.String())
	require.Equal(t, "erigon", dialectErigon.String())
	require.Equal(t, "undetected", dialectUnknown.String())
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
