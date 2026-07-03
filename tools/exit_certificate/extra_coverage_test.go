package exit_certificate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// RunStepA in "auto" mode with an unusable state dump falls back to receipt harvesting; with
// transaction-free blocks and no wrapped tokens the result is an empty address set.
func TestRunStepAEmptyBlocks(t *testing.T) {
	t.Parallel()
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		if method == rpcMethodEthGetBlockByNumber {
			return map[string]any{"transactions": []string{}}
		}
		return "0x" // debug_accountRange probe fails → receipt-harvest fallback
	})
	cfg := &Config{
		L2RPCURL: url,
		Options:  Options{RPCBatchSize: 10, ConcurrencyLimit: 2, StepAWindowSize: 100},
	}
	res, err := RunStepA(context.Background(), cfg, 2, nil)
	require.NoError(t, err)
	require.Empty(t, res.Addresses)
}

func TestFetchWETHBalance(t *testing.T) {
	t.Parallel()
	weth := common.HexToAddress("0x000000000000000000000000000000000000abcd")
	srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
		require.Equal(t, rpcMethodEthCall, method)
		call, _ := params[0].(map[string]any)
		data, _ := call["data"].(string)
		switch {
		case strings.HasPrefix(data, wethTokenSelector):
			return quoted(hexWord(0xabcd)), nil // WETH token address
		case strings.HasPrefix(data, totalSupplySelector):
			return quoted(hexWord(5000)), nil
		}
		return quoted("0x"), nil
	})
	entry, err := fetchWETHBalance(context.Background(), srv.URL, common.HexToAddress("0xbridge"), "latest")
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, weth, entry.WrappedTokenAddress)
	require.Equal(t, "5000", entry.Balance)
}

func TestFetchWETHBalanceZeroAddress(t *testing.T) {
	t.Parallel()
	srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return quoted(hexWord(0)), nil // zero WETH address → no entry
	})
	entry, err := fetchWETHBalance(context.Background(), srv.URL, common.HexToAddress("0xbridge"), "latest")
	require.NoError(t, err)
	require.Nil(t, entry)
}

func TestFetchTokenNameAndDecimalsExtra(t *testing.T) {
	t.Parallel()
	addr := common.HexToAddress("0xtoken")

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		// ABI-encoded string "USDC": [offset=32][len=4]["USDC"...]
		nameData := make([]byte, 96)
		nameData[31] = 0x20
		nameData[63] = 4
		copy(nameData[64:], []byte("USDC"))
		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, rpcMethodEthCall, method)
			call, _ := params[0].(map[string]any)
			data, _ := call["data"].(string)
			if strings.HasPrefix(data, abiSelectorName) {
				return quoted("0x" + common.Bytes2Hex(nameData)), nil
			}
			return quoted(hexWord(18)), nil // decimals()
		})
		require.Equal(t, "USDC", fetchTokenName(context.Background(), srv.URL, addr))
		require.Equal(t, uint8(18), fetchTokenDecimals(context.Background(), srv.URL, addr))
	})

	t.Run("rpc error returns zero values", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return nil, revertErr()
		})
		require.Empty(t, fetchTokenName(context.Background(), srv.URL, addr))
		require.Equal(t, uint8(0), fetchTokenDecimals(context.Background(), srv.URL, addr))
	})
}

func TestIsRevertError(t *testing.T) {
	t.Parallel()
	require.True(t, isRevertError(&jsonRPCError{Code: 3}))
	require.True(t, isRevertError(&jsonRPCError{Message: "execution reverted"}))
	require.False(t, isRevertError(&jsonRPCError{Code: -32000, Message: "server error"}))
}

func TestComputeNativeBalance(t *testing.T) {
	t.Parallel()
	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		switch method {
		case rpcMethodEthGetBalance:
			var tag string
			_ = json.Unmarshal(params[1], &tag)
			if tag == "0x0" {
				return "0x64" // genesis balance 100
			}
			return "0xa" // current balance 10 → unlocked native = 90
		default:
			return "0x" // gasTokenNetwork/Address fail → defaults
		}
	})
	entry, err := computeNativeBalance(context.Background(), url, common.HexToAddress("0xbridge"), "latest")
	require.NoError(t, err)
	require.Equal(t, "90", entry.Balance)
	require.Equal(t, common.Address{}, entry.WrappedTokenAddress)
}

func TestSaveJSONErrorBranches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Marshal failure: a channel cannot be JSON-encoded → logged, no panic, nothing written.
	require.NotPanics(t, func() { saveJSON(dir, "bad.json", make(chan int)) })
	require.False(t, fileExists(filepath.Join(dir, "bad.json")))

	// Write failure: using a regular file as the "directory" makes WriteFile fail.
	notADir := filepath.Join(dir, "afile")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))
	require.NotPanics(t, func() { saveJSON(notADir, "out.json", map[string]int{"a": 1}) })
}

func TestReceiptAddresses(t *testing.T) {
	t.Parallel()
	from := "0x1000000000000000000000000000000000000001"
	to := "0x1000000000000000000000000000000000000002"
	logAddr := "0x1000000000000000000000000000000000000003"

	srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		require.Equal(t, "eth_getTransactionReceipt", method)
		receipt := map[string]any{
			"from": from, "to": to,
			"logs": []map[string]string{{"address": logAddr}},
		}
		out, _ := json.Marshal(receipt)
		return out, nil
	})

	addrs, err := receiptAddresses(context.Background(), srv.URL, common.HexToHash("0xabc"))
	require.NoError(t, err)
	require.ElementsMatch(t,
		[]common.Address{common.HexToAddress(from), common.HexToAddress(to), common.HexToAddress(logAddr)}, addrs)
}

func TestReceiptAddressesNull(t *testing.T) {
	t.Parallel()
	srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return json.RawMessage(`null`), nil
	})
	_, err := receiptAddresses(context.Background(), srv.URL, common.HexToHash("0xabc"))
	require.ErrorContains(t, err, "is null")
}

func TestFetchL2ChainID(t *testing.T) {
	t.Parallel()
	srv := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		require.Equal(t, "eth_chainId", method)
		return quoted("0x1a4"), nil
	})
	id, err := fetchL2ChainID(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, uint64(420), id)
}

func TestBuildHolderBridgeExits(t *testing.T) {
	t.Parallel()
	stepC := &StepCResult{HolderBridges: []HolderBridge{
		{OriginNetwork: 1, OriginTokenAddress: common.HexToAddress("0xaa"),
			HolderAddress: common.HexToAddress("0xbb"), Amount: "100"},
		{OriginNetwork: 1, OriginTokenAddress: common.HexToAddress("0xcc"),
			HolderAddress: common.HexToAddress("0xdd"), Amount: "0"}, // zero → skipped
	}}
	exits := buildHolderBridgeExits(stepC, 0)
	require.Len(t, exits, 1)
	require.Equal(t, common.HexToAddress("0xbb"), exits[0].DestinationAddress)
}

func TestAnvilForkBackendWrappersExtra(t *testing.T) {
	t.Parallel()
	bridge := common.HexToAddress("0xbridge")
	token := common.HexToAddress("0xtoken")

	rootOut, err := bridgeABI.Methods["getRoot"].Outputs.Pack([32]byte{})
	require.NoError(t, err)
	metaOut, err := bridgeABI.Methods["getTokenMetadata"].Outputs.Pack([]byte{0x07})
	require.NoError(t, err)
	gasMetaOut, err := bridgeABI.Methods["gasTokenMetadata"].Outputs.Pack([]byte{})
	require.NoError(t, err)
	wrappedOut, err := bridgeABI.Methods["getTokenWrappedAddress"].Outputs.Pack(common.HexToAddress("0xbeef"))
	require.NoError(t, err)

	getRootSel := selectorHex(bridgeABI, "getRoot")
	tokenMetaSel := selectorHex(bridgeABI, "getTokenMetadata")
	gasMetaSel := selectorHex(bridgeABI, "gasTokenMetadata")
	wrappedSel := selectorHex(bridgeABI, "getTokenWrappedAddress")

	srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
		if method == "anvil_setBalance" {
			return quoted("0x1"), nil
		}
		call, _ := params[0].(map[string]any)
		data, _ := call["data"].(string)
		data = strings.TrimPrefix(data, "0x")
		switch {
		case strings.HasPrefix(data, getRootSel):
			return hexResult(rootOut), nil
		case strings.HasPrefix(data, tokenMetaSel):
			return hexResult(metaOut), nil
		case strings.HasPrefix(data, gasMetaSel):
			return hexResult(gasMetaOut), nil
		case strings.HasPrefix(data, wrappedSel):
			return hexResult(wrappedOut), nil
		}
		return quoted("0x"), nil
	})

	b := &anvilForkBackend{url: srv.URL, bridgeAddr: bridge}
	ctx := context.Background()

	root, err := b.LocalExitRoot(ctx, "latest")
	require.NoError(t, err)
	require.Equal(t, common.Hash{}, root)

	meta, err := b.TokenMetadata(ctx, token)
	require.NoError(t, err)
	require.Equal(t, []byte{0x07}, meta)

	gasMeta, err := b.GasTokenMetadata(ctx)
	require.NoError(t, err)
	require.Empty(t, gasMeta)

	wrapped, err := b.TokenWrappedAddress(ctx, 1, token)
	require.NoError(t, err)
	require.Equal(t, common.HexToAddress("0xbeef"), wrapped)

	require.NoError(t, b.SetSenderBalance(ctx, common.HexToAddress("0xsender")))
}
