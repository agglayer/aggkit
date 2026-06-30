package exit_certificate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	aaltAddr1 = "0x1000000000000000000000000000000000000001"
	aaltAddr2 = "0x2000000000000000000000000000000000000002"
	aaltAddr3 = "0x3000000000000000000000000000000000000003"
	aaltToken = "0x4000000000000000000000000000000000000004"

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
				`"` + aaltAddr1 + `":{"address":"` + aaltAddr1 + `"},` +
				`"` + aaltAddr2 + `":{"address":"` + aaltAddr2 + `"}},"next":"` + next + `"}`
		} else {
			result = `{"root":"0x0","accounts":{` +
				`"` + aaltAddr3 + `":{"address":"` + aaltAddr3 + `"}},"next":""}`
		}
		_, _ = w.Write(encodeRPC(t, req.ID, result))
	}))
	defer server.Close()

	cfg := &Config{L2RPCURL: server.URL}
	addrs, err := collectAccountsViaStateDump(context.Background(), cfg, 100)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "must paginate until next is empty")
	require.ElementsMatch(t, []common.Address{
		common.HexToAddress(aaltAddr1),
		common.HexToAddress(aaltAddr2),
		common.HexToAddress(aaltAddr3),
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
			`{"topics":["` + transferTopic.Hex() + `","` + zeroTopic + `","` + topicForAddr(aaltAddr1) + `"]},` +
			`{"topics":["` + transferTopic.Hex() + `","` + topicForAddr(aaltAddr1) + `","` + topicForAddr(aaltAddr2) + `"]}` +
			`]`
		_, _ = w.Write(encodeRPC(t, req.ID, logs))
	}))
	defer server.Close()

	addrs, err := fetchTransferHoldersInRange(
		context.Background(), server.URL, common.HexToAddress(aaltToken), 0, 100)
	require.NoError(t, err)
	require.ElementsMatch(t, []common.Address{
		common.HexToAddress(aaltAddr1),
		common.HexToAddress(aaltAddr1),
		common.HexToAddress(aaltAddr2),
	}, addrs, "from and to of each log are collected; the zero address is filtered out")
}

// RunStepAAlt in "both" mode merges the state-dump accounts with the Transfer-log holders.
func TestRunStepAAlt_Both_MergesSources(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case rpcMethodDebugAccountRange:
			result := `{"root":"0x0","accounts":{"` + aaltAddr1 + `":{"address":"` + aaltAddr1 + `"}},"next":""}`
			_, _ = w.Write(encodeRPC(t, req.ID, result))
		case rpcMethodEthGetLogs:
			// Holder addr2 received the wrapped token but is not an account in the dump.
			logs := `[{"topics":["` + transferTopic.Hex() + `","` +
				topicForAddr(aaltAddr1) + `","` + topicForAddr(aaltAddr2) + `"]}]`
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
	wrappedTokens := []WrappedToken{{WrappedTokenAddress: common.HexToAddress(aaltToken)}}

	result, err := RunStepAAlt(context.Background(), cfg, 10, wrappedTokens)
	require.NoError(t, err)
	require.Equal(t, []common.Address{
		common.HexToAddress(aaltAddr1),
		common.HexToAddress(aaltAddr2),
	}, result.Addresses, "addresses are merged, de-duplicated and sorted")
}

// In "auto" mode, when debug_accountRange is unavailable the step falls back to receipt
// harvesting (block bodies + receipts) and still collects Transfer-log holders.
func TestRunStepAAlt_Auto_FallsBackToReceipts(t *testing.T) {
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
			receipt := `{"from":"` + aaltAddr1 + `","to":"` + aaltAddr3 +
				`","contractAddress":null,"logs":[]}`
			_, _ = w.Write(encodeRPC(t, req.ID, receipt))
		case rpcMethodEthGetLogs:
			logs := `[{"topics":["` + transferTopic.Hex() + `","` +
				topicForAddr(aaltAddr1) + `","` + topicForAddr(aaltAddr2) + `"]}]`
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
	wrappedTokens := []WrappedToken{{WrappedTokenAddress: common.HexToAddress(aaltToken)}}

	result, err := RunStepAAlt(context.Background(), cfg, 5, wrappedTokens)
	require.NoError(t, err)
	// addr1, addr3 from the receipt; addr2 from the Transfer log.
	require.ElementsMatch(t, []common.Address{
		common.HexToAddress(aaltAddr1),
		common.HexToAddress(aaltAddr2),
		common.HexToAddress(aaltAddr3),
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

	inner := aaltAddr2
	// Inner address field takes precedence.
	addr, ok := accountAddress(aaltAddr1, &inner)
	require.True(t, ok)
	require.Equal(t, common.HexToAddress(aaltAddr2), addr)

	// Falls back to the map key when the inner field is absent.
	addr, ok = accountAddress(aaltAddr1, nil)
	require.True(t, ok)
	require.Equal(t, common.HexToAddress(aaltAddr1), addr)

	// Non-address key with no inner field → not ok.
	_, ok = accountAddress("not-an-address", nil)
	require.False(t, ok)
}
