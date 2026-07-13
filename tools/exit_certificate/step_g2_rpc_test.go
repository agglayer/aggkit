package exit_certificate

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// rpcResponder stubs a JSON-RPC method call: it returns either a result or an RPC-level error for the
// given method/params. A nil result with a nil error responds with a JSON null result.
type rpcResponder func(method string, params []any) (json.RawMessage, *jsonRPCError)

// newRPCStub starts an httptest server that decodes each single JSON-RPC request and dispatches it to
// respond. It is closed automatically when the test ends.
func newRPCStub(t *testing.T, respond rpcResponder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		params, _ := req.Params.([]any)
		result, rpcErr := respond(req.Method, params)
		resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// hexResult wraps raw ABI-encoded bytes as the quoted hex string an eth_call result uses.
func hexResult(b []byte) json.RawMessage {
	return json.RawMessage(`"0x` + hex.EncodeToString(b) + `"`)
}

// quoted wraps s as a JSON string result (e.g. a tx hash returned by eth_sendTransaction).
func quoted(s string) json.RawMessage {
	return json.RawMessage(`"` + s + `"`)
}

// gasTokenStubURL returns the URL of an RPC stub whose eth_call always returns a zero ABI word,
// satisfying the bridge gasTokenNetwork()/gasTokenAddress() lookups with the standard-ETH values
// (fetchL2GasTokenInfo no longer falls back to them on lookup failure).
func gasTokenStubURL(t *testing.T) string {
	t.Helper()
	srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return hexResult(make([]byte, abiWordBytes)), nil
	})
	return srv.URL
}

func TestSetSenderBalance(t *testing.T) {
	t.Parallel()
	sender := common.HexToAddress("0x1111111111111111111111111111111111111111")

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, "anvil_setBalance", method)
			require.Equal(t, sender.Hex(), params[0])
			require.Equal(t, largeETHBalance, params[1])
			return quoted("0x1"), nil
		})
		require.NoError(t, setSenderBalance(context.Background(), srv.URL, sender))
	})

	t.Run("rpc error", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return nil, revertErr()
		})
		err := setSenderBalance(context.Background(), srv.URL, sender)
		require.Error(t, err)
		require.Contains(t, err.Error(), "set balance")
	})
}

func TestReadLocalExitRoot(t *testing.T) {
	t.Parallel()
	bridge := common.HexToAddress("0x2222222222222222222222222222222222222222")
	want := common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef0")

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		out, err := bridgeABI.Methods["getRoot"].Outputs.Pack([32]byte(want))
		require.NoError(t, err)
		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, rpcMethodEthCall, method)
			call, ok := params[0].(map[string]any)
			require.True(t, ok)
			require.Equal(t, bridge.Hex(), call["to"])
			return hexResult(out), nil
		})
		got, err := readLocalExitRoot(context.Background(), srv.URL, bridge, "latest")
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("rpc error", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return nil, revertErr()
		})
		_, err := readLocalExitRoot(context.Background(), srv.URL, bridge, "latest")
		require.Error(t, err)
	})

	// LocalExitRoot wrapper on the production backend exercises the same path.
	t.Run("backend wrapper", func(t *testing.T) {
		t.Parallel()
		out, err := bridgeABI.Methods["getRoot"].Outputs.Pack([32]byte(want))
		require.NoError(t, err)
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return hexResult(out), nil
		})
		backend := &anvilForkBackend{url: srv.URL, bridgeAddr: bridge}
		got, err := backend.LocalExitRoot(context.Background(), "latest")
		require.NoError(t, err)
		require.Equal(t, want, got)
	})
}

func TestCallGetTokenWrappedAddress(t *testing.T) {
	t.Parallel()
	bridge := common.HexToAddress("0x3333333333333333333333333333333333333333")
	origin := common.HexToAddress("0x4444444444444444444444444444444444444444")
	want := common.HexToAddress("0x5555555555555555555555555555555555555555")

	t.Run("success via backend wrapper", func(t *testing.T) {
		t.Parallel()
		out, err := bridgeABI.Methods["getTokenWrappedAddress"].Outputs.Pack(want)
		require.NoError(t, err)
		srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, rpcMethodEthCall, method)
			return hexResult(out), nil
		})
		backend := &anvilForkBackend{url: srv.URL, bridgeAddr: bridge}
		got, err := backend.TokenWrappedAddress(context.Background(), 0, origin)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("rpc error", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return nil, revertErr()
		})
		_, err := callGetTokenWrappedAddress(context.Background(), srv.URL, bridge, 0, origin)
		require.Error(t, err)
	})

	t.Run("invalid hex result", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return quoted("0xZZZZ"), nil
		})
		_, err := callGetTokenWrappedAddress(context.Background(), srv.URL, bridge, 0, origin)
		require.Error(t, err)
		require.Contains(t, err.Error(), "decode hex result")
	})
}

func TestSendAnvilTransaction(t *testing.T) {
	t.Parallel()
	from := common.HexToAddress("0x6666666666666666666666666666666666666666")
	to := common.HexToAddress("0x7777777777777777777777777777777777777777")
	wantHash := "0x" + strings.Repeat("ab", 32)

	srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
		require.Equal(t, "eth_sendTransaction", method)
		tx, ok := params[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, from.Hex(), tx["from"])
		require.Equal(t, to.Hex(), tx["to"])
		require.Equal(t, anvilTxGasLimit, tx["gas"])
		require.Equal(t, "0x64", tx["value"]) // 100 in hex
		return quoted(wantHash), nil
	})

	got, err := sendAnvilTransaction(context.Background(), srv.URL, from, to, big.NewInt(100), []byte{0x01})
	require.NoError(t, err)
	require.Equal(t, common.HexToHash(wantHash), got)
}

// TestAnvilForkBackendWrappers drives the remaining anvilForkBackend methods through stub servers so
// the thin delegation wrappers are exercised (the underlying functions are covered individually).
func TestAnvilForkBackendWrappers(t *testing.T) {
	t.Parallel()
	bridge := common.HexToAddress("0x1212121212121212121212121212121212121212")
	sender := common.HexToAddress("0x3434343434343434343434343434343434343434")
	token := common.HexToAddress("0x5656565656565656565656565656565656565656")
	maxHex := strings.Repeat("f", 64)
	txHash := "0x" + strings.Repeat("78", 32)

	srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthCall: // balanceOf for PrepareERC20Token (already funded)
			return quoted("0x" + maxHex), nil
		case "eth_getTransactionReceipt":
			return json.RawMessage(`{"status":"0x1","blockNumber":"0x1","logs":[]}`), nil
		default: // anvil_setBalance, eth_sendTransaction
			return quoted(txHash), nil
		}
	})
	backend := &anvilForkBackend{url: srv.URL, bridgeAddr: bridge}
	ctx := context.Background()

	require.NoError(t, backend.SetSenderBalance(ctx, sender))
	require.NoError(t, backend.PrepareERC20Token(ctx, sender, token))

	exit := &agglayertypes.BridgeExit{DestinationNetwork: 1, DestinationAddress: sender, Amount: big.NewInt(1)}
	gotHash, err := backend.SendBridgeAssetTx(ctx, exit, false, token)
	require.NoError(t, err)
	require.Equal(t, common.HexToHash(txHash), gotHash)

	logs, err := backend.WaitForReceipt(ctx, common.HexToHash(txHash))
	require.NoError(t, err)
	require.Empty(t, logs)
}

func TestSendBridgeAssetTx(t *testing.T) {
	t.Parallel()
	bridge := common.HexToAddress("0x8888888888888888888888888888888888888888")
	dest := common.HexToAddress("0x9999999999999999999999999999999999999999")
	token := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	wantHash := "0x" + strings.Repeat("cd", 32)

	exit := &agglayertypes.BridgeExit{
		DestinationNetwork: 1,
		DestinationAddress: dest,
		Amount:             big.NewInt(500),
	}

	t.Run("native sets value", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(_ string, params []any) (json.RawMessage, *jsonRPCError) {
			tx, ok := params[0].(map[string]any)
			require.True(t, ok)
			require.Equal(t, dest.Hex(), tx["from"])
			require.Equal(t, bridge.Hex(), tx["to"])
			require.Equal(t, "0x1f4", tx["value"]) // native exit forwards 500
			return quoted(wantHash), nil
		})
		got, err := sendBridgeAssetTx(context.Background(), srv.URL, bridge, exit, true, token)
		require.NoError(t, err)
		require.Equal(t, common.HexToHash(wantHash), got)
	})

	t.Run("erc20 leaves value unset", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(_ string, params []any) (json.RawMessage, *jsonRPCError) {
			tx, ok := params[0].(map[string]any)
			require.True(t, ok)
			_, hasValue := tx["value"]
			require.False(t, hasValue) // non-native: no ETH value attached
			return quoted(wantHash), nil
		})
		got, err := sendBridgeAssetTx(context.Background(), srv.URL, bridge, exit, false, token)
		require.NoError(t, err)
		require.Equal(t, common.HexToHash(wantHash), got)
	})
}

func TestWaitForReceipt(t *testing.T) {
	t.Parallel()
	txHash := common.HexToHash("0x" + strings.Repeat("ef", 32))

	t.Run("null then success returns logs", func(t *testing.T) {
		t.Parallel()
		calls := 0
		srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, "eth_getTransactionReceipt", method)
			calls++
			if calls == 1 {
				return json.RawMessage("null"), nil
			}
			return json.RawMessage(`{"status":"0x1","blockNumber":"0x5","logs":[]}`), nil
		})
		logs, err := waitForReceipt(context.Background(), srv.URL, txHash)
		require.NoError(t, err)
		require.Empty(t, logs)
		require.GreaterOrEqual(t, calls, 2)
	})

	t.Run("revert reports reason", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
			switch method {
			case "eth_getTransactionReceipt":
				return json.RawMessage(`{"status":"0x0","blockNumber":"0x5","logs":[]}`), nil
			case "eth_getTransactionByHash":
				return json.RawMessage(`{"from":"0x01","to":"0x02","input":"0x","value":"0x0"}`), nil
			case rpcMethodEthCall:
				return nil, nil // call succeeds → "no revert reason available"
			default:
				return quoted("0x1"), nil
			}
		})
		_, err := waitForReceipt(context.Background(), srv.URL, txHash)
		require.Error(t, err)
		require.Contains(t, err.Error(), "reverted")
	})

	t.Run("context cancelled while polling null", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			cancel()
			return json.RawMessage("null"), nil
		})
		_, err := waitForReceipt(ctx, srv.URL, txHash)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestFetchRevertReason(t *testing.T) {
	t.Parallel()
	txHash := common.HexToHash("0x" + strings.Repeat("12", 32))

	t.Run("call succeeds means no reason", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
			if method == "eth_getTransactionByHash" {
				return json.RawMessage(`{"from":"0x01","to":"0x02","input":"0x","value":"0x0"}`), nil
			}
			return quoted("0x"), nil
		})
		require.Equal(t, "no revert reason available",
			fetchRevertReason(context.Background(), srv.URL, txHash, "0x5"))
	})

	t.Run("tx fetch error is reported", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return nil, revertErr()
		})
		got := fetchRevertReason(context.Background(), srv.URL, txHash, "")
		require.Contains(t, got, "could not fetch tx")
	})
}

func TestEnsureERC20Balance(t *testing.T) {
	t.Parallel()
	token := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	account := common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")
	maxHex := strings.Repeat("f", 64) // == maxUint256

	t.Run("sufficient balance skips patch", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, rpcMethodEthCall, method) // never patches
			return quoted("0x" + maxHex), nil
		})
		require.NoError(t, ensureERC20Balance(context.Background(), srv.URL, token, account, maxUint256))
	})

	t.Run("patches first slot then verifies", func(t *testing.T) {
		t.Parallel()
		patched := false
		srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
			switch method {
			case rpcMethodEthCall:
				if patched {
					return quoted("0x" + maxHex), nil
				}
				return quoted("0x0"), nil
			case "hardhat_setStorageAt":
				patched = true
				return quoted("0x1"), nil
			default:
				return quoted("0x1"), nil
			}
		})
		require.NoError(t, ensureERC20Balance(context.Background(), srv.URL, token, account, maxUint256))
		require.True(t, patched)
	})

	t.Run("no layout matches errors", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
			if method == rpcMethodEthCall {
				return quoted("0x0"), nil // balance never reaches required
			}
			return quoted("0x1"), nil
		})
		err := ensureERC20Balance(context.Background(), srv.URL, token, account, maxUint256)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no storage layout matched")
	})
}

func TestPrepareERC20Token(t *testing.T) {
	t.Parallel()
	bridge := common.HexToAddress("0xdddddddddddddddddddddddddddddddddddddddd")
	sender := common.HexToAddress("0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	token := common.HexToAddress("0xffffffffffffffffffffffffffffffffffffffff")
	maxHex := strings.Repeat("f", 64)

	t.Run("zero token address errors", func(t *testing.T) {
		t.Parallel()
		err := prepareERC20Token(context.Background(), "http://unused", bridge, sender, common.Address{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid L2 token address")
	})

	t.Run("balance sufficient then approves", func(t *testing.T) {
		t.Parallel()
		sentApprove := false
		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			switch method {
			case rpcMethodEthCall:
				return quoted("0x" + maxHex), nil // already funded
			case "eth_sendTransaction":
				tx, ok := params[0].(map[string]any)
				require.True(t, ok)
				require.Equal(t, sender.Hex(), tx["from"])
				require.Equal(t, token.Hex(), tx["to"]) // approve goes to the token
				sentApprove = true
				return quoted("0x" + strings.Repeat("01", 32)), nil
			default:
				return quoted("0x1"), nil
			}
		})
		require.NoError(t, prepareERC20Token(context.Background(), srv.URL, bridge, sender, token))
		require.True(t, sentApprove)
	})
}
