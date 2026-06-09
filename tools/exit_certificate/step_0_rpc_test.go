package exit_certificate

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const rpcMethodEthBlockNumber = "eth_blockNumber"

// hexWord returns the 32-byte big-endian hex (0x-prefixed) of v, as an eth_call uint return.
func hexWord(v int64) string {
	w := make([]byte, 32)
	new(big.Int).SetInt64(v).FillBytes(w)
	return "0x" + common.Bytes2Hex(w)
}

// makeWrappedTokenData builds the 96-byte NewWrappedToken event payload.
func makeWrappedTokenData(originNet uint32, originAddr, wrappedAddr common.Address) string {
	data := make([]byte, 96)
	new(big.Int).SetUint64(uint64(originNet)).FillBytes(data[0:32])
	copy(data[44:64], originAddr.Bytes())
	copy(data[76:96], wrappedAddr.Bytes())
	return "0x" + common.Bytes2Hex(data)
}

func TestDecodeNewWrappedTokenEvent(t *testing.T) {
	t.Parallel()
	origin := common.BytesToAddress([]byte("origin"))
	wrapped := common.BytesToAddress([]byte("wrapped"))
	ev, err := decodeNewWrappedTokenEvent(makeWrappedTokenData(3, origin, wrapped))
	require.NoError(t, err)
	require.Equal(t, uint32(3), ev.OriginNetwork)
	require.Equal(t, origin, ev.OriginTokenAddress)
	require.Equal(t, wrapped, ev.WrappedTokenAddr)

	_, err = decodeNewWrappedTokenEvent("0x1234") // too short
	require.Error(t, err)
}

func TestDecodeSetSovereignTokenEvent(t *testing.T) {
	t.Parallel()
	origin := common.BytesToAddress([]byte("origin"))
	sovereign := common.BytesToAddress([]byte("sovereign"))
	ov, err := decodeSetSovereignTokenEvent(makeWrappedTokenData(2, origin, sovereign))
	require.NoError(t, err)
	require.Equal(t, uint32(2), ov.OriginNetwork)
	require.Equal(t, origin, ov.OriginTokenAddress)
	require.Equal(t, sovereign, ov.SovereignAddr)

	_, err = decodeSetSovereignTokenEvent("0xabcd") // too short
	require.Error(t, err)
}

// step0Stub serves every RPC RunStep0 makes: NewWrappedToken/SetSovereignToken log scans,
// totalSupply / WETHToken / gas-token eth_calls and the native-balance eth_getBalance pair.
func step0Stub(t *testing.T, wrappedData string) string {
	t.Helper()
	gasNetSel := "0x" + selectorHex(bridgeABI, "gasTokenNetwork")
	gasAddrSel := "0x" + selectorHex(bridgeABI, "gasTokenAddress")

	return newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		switch method {
		case rpcMethodEthBlockNumber:
			return "0x64"
		case "eth_getLogs":
			var f struct {
				Topics []string `json:"topics"`
			}
			_ = json.Unmarshal(params[0], &f)
			if len(f.Topics) > 0 && strings.EqualFold(f.Topics[0], newWrappedTokenTopic.Hex()) {
				return []map[string]string{{"data": wrappedData}}
			}
			return []map[string]string{} // SetSovereignTokenAddress: none
		case rpcMethodEthCall:
			var c struct {
				Data  string `json:"data"`
				Input string `json:"input"`
			}
			_ = json.Unmarshal(params[0], &c)
			d := c.Data
			if d == "" {
				d = c.Input
			}
			switch {
			case strings.HasPrefix(d, totalSupplySelector):
				return hexWord(1000)
			case strings.HasPrefix(d, wethTokenSelector):
				return hexWord(0) // zero WETH address → no WETH entry
			case strings.HasPrefix(d, gasNetSel):
				return hexWord(0)
			case strings.HasPrefix(d, gasAddrSel):
				return hexWord(0)
			}
			return "0x"
		case "eth_getBalance":
			var tag string
			_ = json.Unmarshal(params[1], &tag)
			if tag == "0x0" {
				return "0x64" // genesis balance 100
			}
			return "0xa" // current balance 10 → unlocked native = 90
		}
		return "0x"
	})
}

func TestRunStep0(t *testing.T) {
	t.Parallel()
	originToken := common.BytesToAddress([]byte("origin"))
	wrappedToken := common.BytesToAddress([]byte("wrapped"))
	url := step0Stub(t, makeWrappedTokenData(1, originToken, wrappedToken))

	cfg := &Config{
		L2RPCURL:        url,
		L2BridgeAddress: common.BytesToAddress([]byte("bridge")),
		TargetBlock:     *aggkittypes.NewBlockNumber(100),
		Options:         Options{BlockRange: 50, ConcurrencyLimit: 2, RPCBatchSize: 10},
	}

	res, err := RunStep0(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, uint64(100), res.TargetBlock)

	// one wrapped token (supply 1000) + the native entry (unlocked 90); no WETH.
	var wrapped, native *LBTEntry
	for i := range res.Entries {
		e := &res.Entries[i]
		switch e.WrappedTokenAddress {
		case wrappedToken:
			wrapped = e
		case common.Address{}:
			native = e
		}
	}
	require.NotNil(t, wrapped, "wrapped token entry present")
	require.Equal(t, "1000", wrapped.Balance)
	require.Equal(t, uint32(1), wrapped.OriginNetwork)
	require.NotNil(t, native, "native entry present")
	require.Equal(t, "90", native.Balance)
}

func TestResolveTargetBlockNumberConstant(t *testing.T) {
	t.Parallel()
	// a constant block number resolves with no RPC call.
	n, err := resolveTargetBlockNumber(context.Background(), "", *aggkittypes.NewBlockNumber(4242))
	require.NoError(t, err)
	require.Equal(t, uint64(4242), n)
}

func TestFetchTotalSuppliesEmpty(t *testing.T) {
	t.Parallel()
	entries, err := fetchTotalSupplies(context.Background(), "", nil, "latest", 10, 2)
	require.NoError(t, err)
	require.Nil(t, entries)
}
