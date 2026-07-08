package exit_certificate

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// --- pure helpers --------------------------------------------------------------------------------

func TestFilterEOAs(t *testing.T) {
	t.Parallel()
	a := common.HexToAddress("0xa")
	b := common.HexToAddress("0xb")
	c := common.HexToAddress("0xc")

	eoas := filterEOAs([]common.Address{a, b, c}, []common.Address{b})
	require.Equal(t, []common.Address{a, c}, eoas)

	// no contracts → all are EOAs
	require.Equal(t, []common.Address{a, b}, filterEOAs([]common.Address{a, b}, nil))
}

func TestCheckDeclaredGenesisPrefund(t *testing.T) {
	t.Parallel()
	// unset → no check
	require.NoError(t, checkDeclaredGenesisPrefund("", big.NewInt(5), 1))
	// declared == detected → ok
	require.NoError(t, checkDeclaredGenesisPrefund("5", big.NewInt(5), 1))
	require.NoError(t, checkDeclaredGenesisPrefund("0", big.NewInt(0), 0))
	// declared != detected → mismatch, including when no preload was detected at all
	require.ErrorIs(t, checkDeclaredGenesisPrefund("4", big.NewInt(5), 1), errGenesisPrefundMismatch)
	require.ErrorIs(t, checkDeclaredGenesisPrefund("4", big.NewInt(0), 0), errGenesisPrefundMismatch)
	// non-numeric (unreachable via LoadConfig) → error
	require.Error(t, checkDeclaredGenesisPrefund("10 ETH", big.NewInt(5), 1))
}

func TestPadLeft(t *testing.T) {
	t.Parallel()
	require.Equal(t, "abc", padLeft("abc", 2)) // already long enough → unchanged
	require.Len(t, padLeft("ab", 5), 5)
	require.True(t, strings.HasSuffix(padLeft("ab", 5), "ab"))
}

func TestSumBalances(t *testing.T) {
	t.Parallel()
	require.Equal(t, big.NewInt(0), sumBalances(nil))
	got := sumBalances(map[common.Address]*big.Int{
		common.HexToAddress("0x1"): big.NewInt(10),
		common.HexToAddress("0x2"): big.NewInt(32),
	})
	require.Equal(t, big.NewInt(42), got)
}

func TestIsEOAResult(t *testing.T) {
	t.Parallel()
	require.True(t, isEOAResult(nil))                          // absent → EOA
	require.True(t, isEOAResult(json.RawMessage(`123`)))       // non-string → treated as EOA
	require.True(t, isEOAResult(json.RawMessage(`""`)))        // empty code → EOA
	require.True(t, isEOAResult(json.RawMessage(`"0x"`)))      // no code → EOA
	require.False(t, isEOAResult(json.RawMessage(`"0x6080"`))) // has code → contract
}

func TestUnmarshalHexBigInt(t *testing.T) {
	t.Parallel()
	require.Nil(t, unmarshalHexBigInt(nil))
	require.Nil(t, unmarshalHexBigInt(json.RawMessage(`""`)))
	require.Nil(t, unmarshalHexBigInt(json.RawMessage(`"0x"`)))
	require.Nil(t, unmarshalHexBigInt(json.RawMessage(`123`))) // non-string → nil
	require.Equal(t, big.NewInt(255), unmarshalHexBigInt(json.RawMessage(`"0xff"`)))
}

func TestBuildSingleEOABalance(t *testing.T) {
	t.Parallel()
	addr := common.HexToAddress("0xeoa")
	tokenAddr := common.HexToAddress("0xtok")
	tokenLookup := map[common.Address]WrappedToken{
		tokenAddr: {WrappedTokenAddress: tokenAddr, OriginNetwork: 1, OriginTokenAddress: common.HexToAddress("0xorig")},
	}

	// no ETH and no tokens → not included
	_, ok := buildSingleEOABalance(addr, nil, nil, tokenLookup)
	require.False(t, ok)

	// ETH only
	entry, ok := buildSingleEOABalance(addr,
		map[common.Address]*big.Int{addr: big.NewInt(500)}, nil, tokenLookup)
	require.True(t, ok)
	require.Equal(t, "500", entry.ETHBalance)
	require.Empty(t, entry.Tokens)

	// token only (zero ETH)
	tokenBalances := map[common.Address]map[common.Address]*big.Int{
		tokenAddr: {addr: big.NewInt(7)},
	}
	entry, ok = buildSingleEOABalance(addr, nil, tokenBalances, tokenLookup)
	require.True(t, ok)
	require.Equal(t, "0", entry.ETHBalance)
	require.Len(t, entry.Tokens, 1)
	require.Equal(t, "7", entry.Tokens[0].Balance)
	require.Equal(t, uint32(1), entry.Tokens[0].OriginNetwork)
}

// TestBuildSingleEOABalanceSortedTokens covers AET-17: the token list must come out sorted by
// wrapped token address regardless of the map iteration order.
func TestBuildSingleEOABalanceSortedTokens(t *testing.T) {
	t.Parallel()
	addr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	tok1 := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	tok2 := common.HexToAddress("0xBBBB000000000000000000000000000000000002")
	tok3 := common.HexToAddress("0xCCCC000000000000000000000000000000000003")

	tokenBalances := map[common.Address]map[common.Address]*big.Int{
		tok3: {addr: big.NewInt(3)},
		tok1: {addr: big.NewInt(1)},
		tok2: {addr: big.NewInt(2)},
	}
	entry, ok := buildSingleEOABalance(addr, nil, tokenBalances, map[common.Address]WrappedToken{})
	require.True(t, ok)
	require.Len(t, entry.Tokens, 3)
	require.Equal(t, tok1, entry.Tokens[0].WrappedTokenAddress)
	require.Equal(t, tok2, entry.Tokens[1].WrappedTokenAddress)
	require.Equal(t, tok3, entry.Tokens[2].WrappedTokenAddress)
}

// TestBuildAccumulatedSorted covers AET-17: native entry first, then tokens sorted by address.
func TestBuildAccumulatedSorted(t *testing.T) {
	t.Parallel()
	holder := common.HexToAddress("0x1111111111111111111111111111111111111111")
	tok1 := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	tok2 := common.HexToAddress("0xBBBB000000000000000000000000000000000002")
	tok3 := common.HexToAddress("0xCCCC000000000000000000000000000000000003")

	tokenBalances := map[common.Address]map[common.Address]*big.Int{
		tok2: {holder: big.NewInt(2)},
		tok3: {holder: big.NewInt(3)},
		tok1: {holder: big.NewInt(1)},
	}
	got := buildAccumulated(map[common.Address]*big.Int{holder: big.NewInt(9)},
		tokenBalances, map[common.Address]WrappedToken{})
	require.Len(t, got, 4)
	require.Equal(t, common.Address{}, got[0].WrappedTokenAddress)
	require.Equal(t, tok1, got[1].WrappedTokenAddress)
	require.Equal(t, tok2, got[2].WrappedTokenAddress)
	require.Equal(t, tok3, got[3].WrappedTokenAddress)
}

func TestBuildEOABalances(t *testing.T) {
	t.Parallel()
	a := common.HexToAddress("0xa")
	b := common.HexToAddress("0xb") // no balances → dropped
	eth := map[common.Address]*big.Int{a: big.NewInt(1)}

	got := buildEOABalances([]common.Address{a, b}, eth, nil, nil)
	require.Len(t, got, 1)
	require.Equal(t, a, got[0].Address)
}

func TestBuildAccumulated(t *testing.T) {
	t.Parallel()
	tokenAddr := common.HexToAddress("0xtok")
	eth := map[common.Address]*big.Int{
		common.HexToAddress("0x1"): big.NewInt(3),
		common.HexToAddress("0x2"): big.NewInt(4),
	}
	tokenBalances := map[common.Address]map[common.Address]*big.Int{
		tokenAddr: {common.HexToAddress("0x1"): big.NewInt(10)},
	}
	tokenLookup := map[common.Address]WrappedToken{
		tokenAddr: {WrappedTokenAddress: tokenAddr, OriginNetwork: 2},
	}

	got := buildAccumulated(eth, tokenBalances, tokenLookup)
	require.Len(t, got, 2) // ETH entry + one token

	// first entry is the native ETH accumulation (zero address)
	require.Equal(t, common.Address{}, got[0].WrappedTokenAddress)
	require.Equal(t, "7", got[0].TotalBalance)

	require.Equal(t, tokenAddr, got[1].WrappedTokenAddress)
	require.Equal(t, "10", got[1].TotalBalance)
	require.Equal(t, uint32(2), got[1].OriginNetwork)
}

// --- RPC fan-out functions via a batch JSON-RPC stub ---------------------------------------------

// newBatchRPCServer answers JSON-RPC requests, batched (array) or single (object), dispatching each
// to resultFor(method, params) for the result value. This covers both concurrentBatchRPC (arrays)
// and singleRPC (single objects).
func newBatchRPCServer(t *testing.T, resultFor func(method string, params []json.RawMessage) any) string {
	t.Helper()
	type rpcReq struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	respOf := func(req rpcReq) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": resultFor(req.Method, req.Params)}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			var reqs []rpcReq
			require.NoError(t, json.Unmarshal(trimmed, &reqs))
			resps := make([]map[string]any, len(reqs))
			for i, req := range reqs {
				resps[i] = respOf(req)
			}
			require.NoError(t, json.NewEncoder(w).Encode(resps))
			return
		}
		var req rpcReq
		require.NoError(t, json.Unmarshal(trimmed, &req))
		require.NoError(t, json.NewEncoder(w).Encode(respOf(req)))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// newErrorRPCServer returns the URL of a server that fails every RPC request with HTTP 500,
// so any RPC batch sent to it errors out (after the client's retries are exhausted).
func newErrorRPCServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rpc unavailable", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// firstAddr decodes the first JSON-RPC param as an address hex string.
func firstAddr(t *testing.T, params []json.RawMessage) common.Address {
	t.Helper()
	require.NotEmpty(t, params)
	var s string
	require.NoError(t, json.Unmarshal(params[0], &s))
	return common.HexToAddress(s)
}

func TestClassifyAddresses(t *testing.T) {
	t.Parallel()
	contract := common.HexToAddress("0xcc")
	eoa1 := common.HexToAddress("0x01")
	eoa2 := common.HexToAddress("0x02")

	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		require.Equal(t, rpcMethodEthGetCode, method)
		if firstAddr(t, params) == contract {
			return "0x6080604052" // has code → contract
		}
		return "0x"
	})

	eoas, contracts, err := classifyAddresses(t.Context(), url,
		[]common.Address{eoa1, contract, eoa2}, "latest", 10, 2)
	require.NoError(t, err)
	require.ElementsMatch(t, []common.Address{eoa1, eoa2}, eoas)
	require.Equal(t, []common.Address{contract}, contracts)
}

func TestFetchETHBalances(t *testing.T) {
	t.Parallel()
	rich := common.HexToAddress("0x01")
	poor := common.HexToAddress("0x02")

	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		require.Equal(t, rpcMethodEthGetBalance, method)
		if firstAddr(t, params) == rich {
			return "0x64" // 100
		}
		return "0x0"
	})

	balances, err := fetchETHBalances(t.Context(), url,
		[]common.Address{rich, poor}, "latest", 10, 2)
	require.NoError(t, err)
	require.Len(t, balances, 1) // only non-zero kept
	require.Equal(t, big.NewInt(100), balances[rich])
}

func TestFetchAllTokenBalances(t *testing.T) {
	t.Parallel()
	token := WrappedToken{WrappedTokenAddress: common.HexToAddress("0xtok"), OriginNetwork: 1}
	holder := common.HexToAddress("0x01")
	other := common.HexToAddress("0x02")

	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		require.Equal(t, "eth_call", method)
		// the balanceOf target address is encoded in the call data; decode the call object
		var call struct {
			Data string `json:"data"`
		}
		require.NoError(t, json.Unmarshal(params[0], &call))
		if strings.HasSuffix(call.Data, strings.TrimPrefix(holder.Hex(), "0x")) {
			return "0x05"
		}
		return "0x0"
	})

	out, err := fetchAllTokenBalances(t.Context(), url,
		[]WrappedToken{token}, []common.Address{holder, other}, "latest", 10, 2)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, big.NewInt(5), out[token.WrappedTokenAddress][holder])
}

// TestFetchAllTokenBalancesFailFast guards against the silent-drop regression: a failing
// balanceOf batch for any token must abort the scan with an error rather than omitting the
// token from the map (which would make Step C misroute its whole supply to exitAddress).
func TestFetchAllTokenBalancesFailFast(t *testing.T) {
	t.Parallel()
	token := WrappedToken{WrappedTokenAddress: common.HexToAddress("0xtok"), OriginNetwork: 1}
	holder := common.HexToAddress("0x01")

	url := newErrorRPCServer(t)

	out, err := fetchAllTokenBalances(t.Context(), url,
		[]WrappedToken{token}, []common.Address{holder}, "latest", 10, 2)
	require.Error(t, err)
	require.Nil(t, out)
}

// blockTagOf decodes the second JSON-RPC param (the block tag) as a string.
func blockTagOf(t *testing.T, params []json.RawMessage) string {
	t.Helper()
	require.GreaterOrEqual(t, len(params), 2)
	var s string
	require.NoError(t, json.Unmarshal(params[1], &s))
	return s
}

func stepBConfig(url string) *Config {
	return &Config{
		L2RPCURL: url,
		Options:  Options{RPCBatchSize: 10, ConcurrencyLimit: 2},
	}
}

// genesisTag is toBlockTag(0): the block tag the genesis-preload guard queries.
const genesisTag = "0x0"

// rpcMethodEthGetCode is the eth_getCode method name (rpcMethodEthGetBalance lives in step_b2_test.go).
const rpcMethodEthGetCode = "eth_getCode"

func TestRunStepB1HappyPath(t *testing.T) {
	t.Parallel()
	rich := common.HexToAddress("0x01")
	poor := common.HexToAddress("0x02")

	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		switch method {
		case rpcMethodEthGetCode:
			return "0x" // all EOAs
		case rpcMethodEthGetBalance:
			if blockTagOf(t, params) == genesisTag {
				return "0x0" // zero at genesis → guard passes
			}
			if firstAddr(t, params) == rich {
				return "0x64" // 100
			}
			return "0x0"
		default:
			return "0x0"
		}
	})

	stepA := &StepAResult{Addresses: []common.Address{rich, poor}}
	res, err := RunStepB1(t.Context(), stepBConfig(url), 100, stepA)
	require.NoError(t, err)
	require.Empty(t, res.ContractAddresses)
	require.Len(t, res.EOABalances, 1) // only the rich EOA has a balance
	require.Equal(t, rich, res.EOABalances[0].Address)
	// accumulated always carries the native-ETH entry first
	require.NotEmpty(t, res.Accumulated)
	require.Equal(t, "100", res.Accumulated[0].TotalBalance)
}

func TestRunStepB1GenesisPreloadAborts(t *testing.T) {
	t.Parallel()
	addr := common.HexToAddress("0x01")
	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		switch method {
		case rpcMethodEthGetCode:
			return "0x"
		case rpcMethodEthGetBalance:
			return "0x64" // non-zero everywhere, including genesis → guard trips
		default:
			return "0x0"
		}
	})

	stepA := &StepAResult{Addresses: []common.Address{addr}}

	// default: a genesis preload aborts Step B1
	_, err := RunStepB1(t.Context(), stepBConfig(url), 100, stepA)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ignoreGenesisBalance")

	// ignoreGenesisBalance downgrades it to a warning and continues
	cfg := stepBConfig(url)
	cfg.Options.IgnoreGenesisBalance = true
	res, err := RunStepB1(t.Context(), cfg, 100, stepA)
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestRunStepB1GenesisPrefundDeclared(t *testing.T) {
	t.Parallel()
	addr := common.HexToAddress("0x01")
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		switch method {
		case rpcMethodEthGetCode:
			return "0x"
		case rpcMethodEthGetBalance:
			return "0x64" // 100 wei everywhere, including genesis → preload total = 100
		default:
			return "0x0"
		}
	})

	stepA := &StepAResult{Addresses: []common.Address{addr}}

	// declared prefund matches the detected preload total → the preload itself is still only
	// acceptable with ignoreGenesisBalance=true
	cfg := stepBConfig(url)
	cfg.Options.IgnoreGenesisBalance = true
	cfg.Options.GenesisPrefundETHWei = "100"
	res, err := RunStepB1(t.Context(), cfg, 100, stepA)
	require.NoError(t, err)
	require.NotNil(t, res)

	// a mismatching declaration is fatal even with ignoreGenesisBalance=true: Step F would
	// subtract the wrong amount from the native LBT entry
	cfg.Options.GenesisPrefundETHWei = "50"
	_, err = RunStepB1(t.Context(), cfg, 100, stepA)
	require.ErrorIs(t, err, errGenesisPrefundMismatch)
}

func TestRunStepB(t *testing.T) {
	t.Parallel()
	// All EOAs and no extra ERC-20s, so B2 and B3 short-circuit; this exercises the B1→B2→B3
	// orchestration in RunStepB end-to-end.
	addr := common.HexToAddress("0x01")
	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		switch method {
		case rpcMethodEthGetCode:
			return "0x"
		case rpcMethodEthGetBalance:
			if blockTagOf(t, params) == genesisTag {
				return "0x0"
			}
			return "0x2a" // 42
		default:
			return "0x0"
		}
	})

	stepA := &StepAResult{Addresses: []common.Address{addr}}
	res, err := RunStepB(t.Context(), stepBConfig(url), 100, stepA)
	require.NoError(t, err)
	require.Len(t, res.EOABalances, 1)
	require.Empty(t, res.DetectedERC20s)
	require.Empty(t, res.ERC20HolderBreakdowns)
}
