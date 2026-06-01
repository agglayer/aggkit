package exit_certificate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const (
	rpcMethodEthCall       = "eth_call"
	rpcMethodEthGetBalance = "eth_getBalance"
)

// rpcTestCall holds the decoded parts of a single JSON-RPC request
// received by a test server.
type rpcTestCall struct {
	Method   string // "eth_call", "eth_getBalance", …
	To       string // lowercase hex addr
	Selector string // first 10 chars of data ("0x" + 8 hex)
	FullData string // lowercase data without "0x"
}

// newEthCallServer creates a test server that handles both single and batch
// JSON-RPC requests. respond is called once per sub-request; returning a
// non-nil *jsonRPCError sends an RPC error in the response.
func newEthCallServer(t *testing.T, respond func(rpcTestCall) (json.RawMessage, *jsonRPCError)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")

		decode := func(raw json.RawMessage) jsonRPCResponse {
			var req struct {
				Method string            `json:"method"`
				Params []json.RawMessage `json:"params"`
				ID     int               `json:"id"`
			}
			_ = json.Unmarshal(raw, &req)
			tc := rpcTestCall{Method: req.Method}
			if len(req.Params) > 0 {
				switch req.Method {
				case rpcMethodEthCall:
					var obj struct {
						To   string `json:"to"`
						Data string `json:"data"`
					}
					_ = json.Unmarshal(req.Params[0], &obj)
					tc.To = strings.ToLower(obj.To)
					tc.FullData = strings.ToLower(strings.TrimPrefix(obj.Data, "0x"))
					if len(obj.Data) >= 10 {
						tc.Selector = strings.ToLower(obj.Data[:10])
					}
				case rpcMethodEthGetBalance:
					var addr string
					_ = json.Unmarshal(req.Params[0], &addr)
					tc.To = strings.ToLower(addr)
				}
			}
			result, rpcErr := respond(tc)
			if rpcErr != nil {
				return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
			}
			return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
		}

		trimmed := bytes.TrimSpace(body)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			var rawReqs []json.RawMessage
			require.NoError(t, json.Unmarshal(body, &rawReqs))
			resps := make([]jsonRPCResponse, len(rawReqs))
			for i, raw := range rawReqs {
				resps[i] = decode(raw)
			}
			require.NoError(t, json.NewEncoder(w).Encode(resps))
		} else {
			resp := decode(json.RawMessage(body))
			require.NoError(t, json.NewEncoder(w).Encode(resp))
		}
	}))
}

// abiUint256 ABI-encodes n as a 32-byte hex JSON string.
func abiUint256(n *big.Int) json.RawMessage {
	b := common.LeftPadBytes(n.Bytes(), 32)
	return json.RawMessage(`"0x` + common.Bytes2Hex(b) + `"`)
}

// abiZero returns an ABI-encoded zero uint256.
func abiZero() json.RawMessage { return abiUint256(new(big.Int)) }

// abiString ABI-encodes s as a dynamic string return value (offset | length | data).
func abiString(s string) json.RawMessage {
	offset := "0000000000000000000000000000000000000000000000000000000000000020"
	length := fmt.Sprintf("%064x", len(s))
	data := common.Bytes2Hex([]byte(s))
	for len(data)%64 != 0 {
		data += "00"
	}
	return json.RawMessage(`"0x` + offset + length + data + `"`)
}

// revertErr returns an RPC error representing a contract revert (not retried by batchRPC).
func revertErr() *jsonRPCError {
	return &jsonRPCError{Code: 3, Message: "execution reverted"}
}

// addrLow returns the lowercase hex of addr, matching tc.To comparisons.
func addrLow(addr common.Address) string { return strings.ToLower(addr.Hex()) }

// eoaFromData extracts the queried address from a balanceOf call's FullData.
// The parameter starts at offset 8 (after 8-char selector) and the last 40
// chars encode the 20-byte address.
func eoaFromData(fullData string) string {
	if len(fullData) < 72 {
		return ""
	}
	return "0x" + fullData[32:]
}

// --- checkWrappedTokenBalances ---

func TestCheckWrappedTokenBalances_AllZeroReturnEmpty(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xC001000000000000000000000000000000000001")
	server := newEthCallServer(t, func(_ rpcTestCall) (json.RawMessage, *jsonRPCError) {
		return abiZero(), nil
	})
	defer server.Close()

	balances, err := checkWrappedTokenBalances(
		context.Background(), server.URL, contractAddr, nil, "latest", 200, 5,
	)
	require.NoError(t, err)
	require.Empty(t, balances)
}

func TestCheckWrappedTokenBalances_ETHBalanceOnly(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xC001000000000000000000000000000000000001")
	ethBal := big.NewInt(5_000_000)

	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		if tc.Method == rpcMethodEthGetBalance {
			return abiUint256(ethBal), nil
		}
		return abiZero(), nil
	})
	defer server.Close()

	balances, err := checkWrappedTokenBalances(
		context.Background(), server.URL, contractAddr, nil, "latest", 200, 5,
	)
	require.NoError(t, err)
	require.Len(t, balances, 1)
	require.Equal(t, common.Address{}, balances[0].Token.WrappedTokenAddress) // zero addr = native ETH
	require.Equal(t, ethBal.String(), balances[0].Balance)
}

func TestCheckWrappedTokenBalances_WrappedTokenHeld(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xC001000000000000000000000000000000000001")
	tokenAddr := common.HexToAddress("0xABCD000000000000000000000000000000000001")
	tokenBal := big.NewInt(999_000)

	wrappedTokens := []WrappedToken{{
		WrappedTokenAddress: tokenAddr,
		OriginNetwork:       1,
		OriginTokenAddress:  common.HexToAddress("0x0101010101010101010101010101010101010101"),
	}}

	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		if tc.Method == rpcMethodEthCall && tc.To == addrLow(tokenAddr) {
			return abiUint256(tokenBal), nil
		}
		return abiZero(), nil
	})
	defer server.Close()

	balances, err := checkWrappedTokenBalances(
		context.Background(), server.URL, contractAddr, wrappedTokens, "latest", 200, 5,
	)
	require.NoError(t, err)
	require.Len(t, balances, 1)
	require.Equal(t, tokenAddr, balances[0].Token.WrappedTokenAddress)
	require.Equal(t, uint32(1), balances[0].Token.OriginNetwork)
	require.Equal(t, tokenBal.String(), balances[0].Balance)
}

func TestCheckWrappedTokenBalances_ETHAndTokenBothNonZero(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xC001000000000000000000000000000000000001")
	tokenAddr := common.HexToAddress("0xABCD000000000000000000000000000000000002")

	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		if tc.Method == rpcMethodEthGetBalance {
			return abiUint256(big.NewInt(1_000_000)), nil
		}
		if tc.Method == rpcMethodEthCall && tc.To == addrLow(tokenAddr) {
			return abiUint256(big.NewInt(500)), nil
		}
		return abiZero(), nil
	})
	defer server.Close()

	balances, err := checkWrappedTokenBalances(
		context.Background(), server.URL, contractAddr,
		[]WrappedToken{{WrappedTokenAddress: tokenAddr}}, "latest", 200, 5,
	)
	require.NoError(t, err)
	require.Len(t, balances, 2)
}

// --- detectERC20Contracts ---

func TestDetectERC20Contracts_Empty(t *testing.T) {
	t.Parallel()

	result := detectERC20Contracts(context.Background(), "http://unused", nil, "latest", 5)
	require.Empty(t, result)
}

func TestDetectERC20Contracts_ZeroTotalSupply(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xC001000000000000000000000000000000000001")
	server := newEthCallServer(t, func(_ rpcTestCall) (json.RawMessage, *jsonRPCError) {
		return abiZero(), nil // totalSupply = 0 → not ERC-20
	})
	defer server.Close()

	result := detectERC20Contracts(context.Background(), server.URL, []common.Address{contractAddr}, "latest", 5)
	require.Empty(t, result)
}

func TestDetectERC20Contracts_BalanceOfZeroReverts(t *testing.T) {
	t.Parallel()

	// Contracts that have a totalSupply-like selector but revert on balanceOf(address(0))
	// should not be classified as ERC-20.
	contractAddr := common.HexToAddress("0xC001000000000000000000000000000000000001")
	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		if tc.Selector == totalSupplySelector {
			return abiUint256(big.NewInt(1000)), nil
		}
		return nil, revertErr()
	})
	defer server.Close()

	result := detectERC20Contracts(context.Background(), server.URL, []common.Address{contractAddr}, "latest", 5)
	require.Empty(t, result)
}

func TestDetectERC20Contracts_ValidERC20WithNameAndSymbol(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xC001000000000000000000000000000000000001")
	supply := big.NewInt(1_000_000)

	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		switch tc.Selector {
		case totalSupplySelector:
			return abiUint256(supply), nil
		case nameSelector:
			return abiString("MyToken"), nil
		case symbolSelector:
			return abiString("MTK"), nil
		default:
			return abiZero(), nil // balanceOf(address(0)) succeeds → confirms ERC-20
		}
	})
	defer server.Close()

	result := detectERC20Contracts(
		context.Background(), server.URL, []common.Address{contractAddr}, "latest", 5,
	)
	require.Len(t, result, 1)
	info, ok := result[contractAddr]
	require.True(t, ok)
	require.Equal(t, supply, info.supply)
	require.Equal(t, "MyToken", info.name)
	require.Equal(t, "MTK", info.symbol)
}

func TestDetectERC20Contracts_MultipleContracts(t *testing.T) {
	t.Parallel()

	erc20Addr := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	nonERC20Addr := common.HexToAddress("0xBBBB000000000000000000000000000000000002")

	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		if tc.To == addrLow(erc20Addr) && tc.Selector == totalSupplySelector {
			return abiUint256(big.NewInt(500)), nil
		}
		if tc.To == addrLow(nonERC20Addr) && tc.Selector == totalSupplySelector {
			return abiZero(), nil // zero supply → filtered out
		}
		return abiZero(), nil // balanceOf succeeds for erc20Addr
	})
	defer server.Close()

	contracts := []common.Address{erc20Addr, nonERC20Addr}
	result := detectERC20Contracts(context.Background(), server.URL, contracts, "latest", 5)
	require.Len(t, result, 1)
	_, ok := result[erc20Addr]
	require.True(t, ok)
}

// --- RunStepB2 ---

func TestRunStepB2_EmptyContractAddresses(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		L2RPCURL: "http://unused",
		Options:  Options{RPCBatchSize: 200, ConcurrencyLimit: 5},
	}
	result, err := RunStepB2(context.Background(), cfg, 0, nil, nil, nil)
	require.NoError(t, err)
	require.Empty(t, result.DetectedERC20s)
	require.Empty(t, result.DiscardedERC20s)
}

func TestRunStepB2_NoERC20sDetected(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xC001000000000000000000000000000000000001")
	server := newEthCallServer(t, func(_ rpcTestCall) (json.RawMessage, *jsonRPCError) {
		return abiZero(), nil // totalSupply = 0 → no ERC-20s
	})
	defer server.Close()

	cfg := &Config{
		L2RPCURL: server.URL,
		Options:  Options{RPCBatchSize: 200, ConcurrencyLimit: 5},
	}
	result, err := RunStepB2(context.Background(), cfg, 0, []common.Address{contractAddr}, nil, nil)
	require.NoError(t, err)
	require.Empty(t, result.DetectedERC20s)
	require.Empty(t, result.DiscardedERC20s)
}

func TestRunStepB2_DiscardedERC20_HoldsNoTrackedTokens(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xC001000000000000000000000000000000000001")

	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		switch tc.Selector {
		case totalSupplySelector:
			return abiUint256(big.NewInt(1000)), nil
		case nameSelector:
			return abiString("VaultToken"), nil
		case symbolSelector:
			return abiString("VT"), nil
		default:
			return abiZero(), nil // balanceOf(0x0) ok; no tracked token held
		}
	})
	defer server.Close()

	wrappedTokens := []WrappedToken{{
		WrappedTokenAddress: common.HexToAddress("0xDDDD000000000000000000000000000000000001"),
	}}

	cfg := &Config{
		L2RPCURL: server.URL,
		Options:  Options{RPCBatchSize: 200, ConcurrencyLimit: 5},
	}
	result, err := RunStepB2(context.Background(), cfg, 0,
		[]common.Address{contractAddr}, nil, wrappedTokens)
	require.NoError(t, err)
	require.Empty(t, result.DetectedERC20s)
	require.Len(t, result.DiscardedERC20s, 1)
	require.Equal(t, contractAddr, result.DiscardedERC20s[0].Address)
	require.Equal(t, "VaultToken", result.DiscardedERC20s[0].Name)
	require.Equal(t, "VT", result.DiscardedERC20s[0].Symbol)
}

func TestRunStepB2_DetectedERC20_HoldsTrackedToken(t *testing.T) {
	t.Parallel()

	contractAddr := common.HexToAddress("0xC001000000000000000000000000000000000001")
	tokenAddr := common.HexToAddress("0xABCD000000000000000000000000000000000001")
	tokenBal := big.NewInt(800_000)

	server := newEthCallServer(t, func(tc rpcTestCall) (json.RawMessage, *jsonRPCError) {
		switch {
		case tc.To == addrLow(contractAddr) && tc.Selector == totalSupplySelector:
			return abiUint256(big.NewInt(1000)), nil
		case tc.To == addrLow(contractAddr) && tc.Selector == nameSelector:
			return abiString("StakingPool"), nil
		case tc.To == addrLow(contractAddr) && tc.Selector == symbolSelector:
			return abiString("SP"), nil
		case tc.To == addrLow(tokenAddr) && tc.Selector == balanceOfSelector:
			// balanceOf(contractAddr) on the wrapped token: contract holds tokenBal
			return abiUint256(tokenBal), nil
		default:
			return abiZero(), nil
		}
	})
	defer server.Close()

	wrappedTokens := []WrappedToken{{
		WrappedTokenAddress: tokenAddr,
		OriginNetwork:       0,
		OriginTokenAddress:  common.HexToAddress("0x0202020202020202020202020202020202020202"),
	}}

	cfg := &Config{
		L2RPCURL: server.URL,
		Options:  Options{RPCBatchSize: 200, ConcurrencyLimit: 5},
	}
	result, err := RunStepB2(context.Background(), cfg, 0,
		[]common.Address{contractAddr}, nil, wrappedTokens)
	require.NoError(t, err)
	require.Empty(t, result.DiscardedERC20s)
	require.Len(t, result.DetectedERC20s, 1)

	d := result.DetectedERC20s[0]
	require.Equal(t, contractAddr, d.Address)
	require.Equal(t, "StakingPool", d.Name)
	require.Equal(t, "SP", d.Symbol)
	require.Len(t, d.WrappedTokenBalances, 1)
	require.Equal(t, tokenAddr, d.WrappedTokenBalances[0].Token.WrappedTokenAddress)
	require.Equal(t, tokenBal.String(), d.WrappedTokenBalances[0].Balance)
}
