package exit_certificate

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// makeBridgeEventData builds the 256-byte ABI payload a BridgeEvent log carries (empty metadata).
func makeBridgeEventData(leafType uint8, originNet, destNet, depositCount uint32, amount int64) string {
	data := make([]byte, 256)
	data[31] = leafType
	new(big.Int).SetUint64(uint64(originNet)).FillBytes(data[32:64])
	new(big.Int).SetUint64(uint64(destNet)).FillBytes(data[96:128])
	new(big.Int).SetInt64(amount).FillBytes(data[160:192])
	new(big.Int).SetUint64(256).FillBytes(data[192:224]) // metadataOffset past the words → empty
	new(big.Int).SetUint64(uint64(depositCount)).FillBytes(data[224:256])
	return "0x" + common.Bytes2Hex(data)
}

func TestSplitByLeafType(t *testing.T) {
	t.Parallel()
	deposits := []L1Deposit{
		{DepositCount: 0, LeafType: 0}, // asset
		{DepositCount: 1, LeafType: 1}, // message
		{DepositCount: 2, LeafType: 0}, // asset
	}
	assets, messages := splitByLeafType(deposits)
	require.Len(t, assets, 2)
	require.Len(t, messages, 1)
	require.Equal(t, uint32(1), messages[0].DepositCount)
}

func TestResolveL1LatestBlock(t *testing.T) {
	t.Parallel()
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		require.Equal(t, rpcMethodEthBlockNumber, method)
		return "0x1a4" // 420
	})
	cfg := &Config{L1RPCURL: url}
	block, err := resolveL1LatestBlock(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, uint64(420), block)
}

func TestCheckClaimedBatch(t *testing.T) {
	t.Parallel()
	deposits := []L1Deposit{{DepositCount: 0}, {DepositCount: 1}, {DepositCount: 2}}
	// claim only deposit 1: decode the leafIndex from the isClaimed call data (bytes [4:36]).
	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		require.Equal(t, rpcMethodEthCall, method)
		var call struct {
			Data string `json:"data"`
		}
		require.NoError(t, json.Unmarshal(params[0], &call))
		raw := common.FromHex(call.Data)
		leafIndex := new(big.Int).SetBytes(raw[4:36]).Uint64()
		if leafIndex == 1 {
			return "0x0000000000000000000000000000000000000000000000000000000000000001" // claimed
		}
		return "0x0000000000000000000000000000000000000000000000000000000000000000" // not claimed
	})
	cfg := &Config{L2RPCURL: url, L2BridgeAddress: common.HexToAddress("0xbridge"),
		Options: Options{RPCBatchSize: 10, ConcurrencyLimit: 2}}

	claimed, err := checkClaimedBatch(context.Background(), cfg, deposits)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	_, ok := claimed[1]
	require.True(t, ok)
}

func TestCheckClaimedBatchEmpty(t *testing.T) {
	t.Parallel()
	claimed, err := checkClaimedBatch(context.Background(), &Config{}, nil)
	require.NoError(t, err)
	require.Empty(t, claimed)
}

func TestFetchBridgeEventsInRange(t *testing.T) {
	t.Parallel()
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		require.Equal(t, "eth_getLogs", method)
		return []map[string]string{
			{ // targets L2 (destNet=1) → kept
				"data":            makeBridgeEventData(0, 0, 1, 5, 1000),
				"blockNumber":     "0x10",
				"transactionHash": common.HexToHash("0xaaa").Hex(),
			},
			{ // targets a different network (destNet=9) → filtered out
				"data":            makeBridgeEventData(0, 0, 9, 6, 2000),
				"blockNumber":     "0x11",
				"transactionHash": common.HexToHash("0xbbb").Hex(),
			},
		}
	})
	deposits, err := fetchBridgeEventsInRange(context.Background(), url, common.HexToAddress("0xbridge"), 1, 0, 100)
	require.NoError(t, err)
	require.Len(t, deposits, 1)
	require.Equal(t, uint32(5), deposits[0].DepositCount)
	require.Equal(t, big.NewInt(1000), deposits[0].Amount)
}

func TestFetchL1BridgeEvents(t *testing.T) {
	t.Parallel()
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		require.Equal(t, "eth_getLogs", method)
		return []map[string]string{{
			"data":            makeBridgeEventData(0, 0, 1, 0, 50),
			"blockNumber":     "0x1",
			"transactionHash": common.HexToHash("0xaaa").Hex(),
		}}
	})
	cfg := &Config{L1RPCURL: url, L2NetworkID: 1,
		Options: Options{BlockRange: 50, ConcurrencyLimit: 2, L1StartBlock: 0}}
	deposits, err := fetchL1BridgeEvents(context.Background(), cfg, 100)
	require.NoError(t, err)
	require.NotEmpty(t, deposits)
}

func TestFetchL1BridgeEventsEmptyRange(t *testing.T) {
	t.Parallel()
	cfg := &Config{Options: Options{L1StartBlock: 100}}
	deposits, err := fetchL1BridgeEvents(context.Background(), cfg, 10) // latest < start
	require.NoError(t, err)
	require.Nil(t, deposits)
}

func TestFetchTokenInfoNative(t *testing.T) {
	t.Parallel()
	name, decimals := fetchTokenInfo(context.Background(), &Config{}, 0, common.Address{})
	require.Equal(t, "ETH", name)
	require.Equal(t, uint8(18), decimals)

	// non-zero origin network, zero address → native(net=N)
	name, _ = fetchTokenInfo(context.Background(), &Config{}, 5, common.Address{})
	require.Contains(t, name, "native(net=5)")
}

// abiEncodeString builds the ABI return data for a string (offset|length|utf8 padded to 32).
func abiEncodeString(s string) string {
	paddedLen := ((len(s) + 31) / 32) * 32
	out := make([]byte, 64+paddedLen)
	new(big.Int).SetUint64(32).FillBytes(out[0:32])              // offset
	new(big.Int).SetUint64(uint64(len(s))).FillBytes(out[32:64]) // length
	copy(out[64:], s)
	return "0x" + common.Bytes2Hex(out)
}

func TestFetchTokenNameAndDecimals(t *testing.T) {
	t.Parallel()
	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		require.Equal(t, rpcMethodEthCall, method)
		var call struct {
			Data string `json:"data"`
		}
		require.NoError(t, json.Unmarshal(params[0], &call))
		switch call.Data {
		case abiSelectorName:
			return abiEncodeString("MyToken")
		case abiSelectorDecimals:
			word := make([]byte, 32)
			word[31] = 6
			return "0x" + common.Bytes2Hex(word)
		}
		return "0x"
	})

	require.Equal(t, "MyToken", fetchTokenName(context.Background(), url, common.BytesToAddress([]byte("tok"))))
	require.Equal(t, uint8(6), fetchTokenDecimals(context.Background(), url, common.BytesToAddress([]byte("tok"))))

	// fetchTokenInfo ERC-20 branch (origin network 0 → uses L1 RPC)
	cfg := &Config{L1RPCURL: url, L2NetworkID: 1}
	name, decimals := fetchTokenInfo(context.Background(), cfg, 0, common.BytesToAddress([]byte("tok")))
	require.Equal(t, "MyToken", name)
	require.Equal(t, uint8(6), decimals)
}

func TestFetchTokenInfoNoRPC(t *testing.T) {
	t.Parallel()
	// non-native token but no RPC URL for its origin network → short address, 0 decimals.
	name, decimals := fetchTokenInfo(context.Background(), &Config{}, 0, common.HexToAddress("0xabcdef1234"))
	require.Contains(t, name, "0x")
	require.Equal(t, uint8(0), decimals)
}

func TestLogUnclaimedAssetSummaryNative(t *testing.T) {
	t.Parallel()
	// native assets need no RPC; just exercise the grouping/sorting/logging path.
	assets := []L1Deposit{
		{OriginNetwork: 0, OriginAddress: common.Address{}, Amount: big.NewInt(1e18)},
		{OriginNetwork: 0, OriginAddress: common.Address{}, Amount: big.NewInt(2e18)},
	}
	require.NotPanics(t, func() {
		logUnclaimedAssetSummary(context.Background(), &Config{}, assets)
		logUnclaimedAssetSummary(context.Background(), &Config{}, nil) // empty → early return
	})
}

// --- bridge service HTTP cross-check -------------------------------------------------------------

func TestFetchZkevmPendingBridges(t *testing.T) {
	t.Parallel()
	// two pages: total_cnt=3, page size capped so the loop iterates.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "5", r.URL.Query().Get("dest_net"), "dest_net must use the configured l2NetworkId")
		offset := r.URL.Query().Get("offset")
		var deposits []map[string]any
		if offset == "0" {
			deposits = []map[string]any{
				{"deposit_cnt": 10}, {"deposit_cnt": 11},
			}
		} else {
			deposits = []map[string]any{{"deposit_cnt": 12}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"deposits": deposits, "total_cnt": "3"})
	}))
	t.Cleanup(srv.Close)

	got, err := fetchZkevmPendingBridges(context.Background(), srv.URL, 5, leafTypeAsset)
	require.NoError(t, err)
	require.Len(t, got, 3)
	for _, dc := range []uint32{10, 11, 12} {
		_, ok := got[dc]
		require.True(t, ok, "deposit %d", dc)
	}
}

func TestCheckBridgeServicePendingBridgesZkevmMatch(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deposits": []map[string]any{{"deposit_cnt": 7}}, "total_cnt": "1",
		})
	}))
	t.Cleanup(srv.Close)

	cfg := &Config{L2NetworkID: 1, Options: Options{
		BridgeServiceURL: srv.URL, BridgeServiceType: BridgeServiceTypeZkevm,
	}}
	// L1 scan also found deposit 7 → match, no error
	err := checkBridgeServicePendingBridges(context.Background(), cfg, []L1Deposit{{DepositCount: 7}})
	require.NoError(t, err)

	// L1 scan found a different set → mismatch error
	err = checkBridgeServicePendingBridges(context.Background(), cfg, []L1Deposit{{DepositCount: 8}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mismatch")
}

func TestFetchAggkitPendingBridges(t *testing.T) {
	t.Parallel()
	// aggkit bridge service: one page of two bridges targeting L2 (dest network 1).
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bridge/v1/bridges", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bridges": []map[string]any{
				{"deposit_count": 20, "destination_network": 1},
				{"deposit_count": 21, "destination_network": 1},
				{"deposit_count": 99, "destination_network": 2}, // other network → ignored
			},
			"count": 3,
		})
	}))
	t.Cleanup(svc.Close)

	// isClaimed: claim deposit 21 so only 20 remains pending.
	rpc := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		var call struct {
			Data string `json:"data"`
		}
		_ = json.Unmarshal(params[0], &call)
		raw := common.FromHex(call.Data)
		if new(big.Int).SetBytes(raw[4:36]).Uint64() == 21 {
			return "0x0000000000000000000000000000000000000000000000000000000000000001"
		}
		return "0x0000000000000000000000000000000000000000000000000000000000000000"
	})

	cfg := &Config{L2RPCURL: rpc, L2NetworkID: 1, L2BridgeAddress: common.HexToAddress("0xbridge"),
		Options: Options{RPCBatchSize: 10, ConcurrencyLimit: 2}}

	got, err := fetchAggkitPendingBridges(context.Background(), cfg, svc.URL, leafTypeAsset)
	require.NoError(t, err)
	require.Len(t, got, 1)
	_, ok := got[20]
	require.True(t, ok)
}

func TestFetchZkevmPendingBridgesHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	_, err := fetchZkevmPendingBridges(context.Background(), srv.URL, 1, leafTypeAsset)
	require.Error(t, err)
}
