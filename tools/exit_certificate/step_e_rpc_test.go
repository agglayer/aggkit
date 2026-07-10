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

func TestResolveL1EndBlock(t *testing.T) {
	t.Parallel()
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		require.Equal(t, rpcMethodEthBlockNumber, method)
		return "0x1a4" // 420
	})

	// No cutoff → latest L1 block.
	cfg := &Config{L1RPCURL: url}
	block, err := resolveL1EndBlock(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, uint64(420), block)

	// Cutoff at or below the head → returned as-is.
	cfg = &Config{L1RPCURL: url, Options: Options{L1EndBlock: 300}}
	block, err = resolveL1EndBlock(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, uint64(300), block)

	// Cutoff beyond the head → clear config error (some L1 clients reject
	// eth_getLogs ranges past the head with "invalid block range params").
	cfg = &Config{L1RPCURL: url, Options: Options{L1EndBlock: 1234}}
	_, err = resolveL1EndBlock(context.Background(), cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "l1EndBlock 1234 is beyond the current L1 latest block 420")
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
		require.Equal(t, rpcMethodEthGetLogs, method)
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
	deposits, logCount, err := fetchBridgeEventsInRange(
		context.Background(), url, common.HexToAddress("0xbridge"), 1, 0, 100)
	require.NoError(t, err)
	require.Len(t, deposits, 1)
	require.Equal(t, 2, logCount, "raw log count includes every destination network")
	require.Equal(t, uint32(5), deposits[0].DepositCount)
	require.Equal(t, big.NewInt(1000), deposits[0].Amount)
}

// depositCountWord encodes a depositCount() eth_call return value (a single uint256 word).
func depositCountWord(n uint64) string {
	word := make([]byte, 32)
	new(big.Int).SetUint64(n).FillBytes(word)
	return "0x" + common.Bytes2Hex(word)
}

func TestFetchL1BridgeEvents(t *testing.T) {
	t.Parallel()
	// blockRange=50 over blocks 0→100 → 3 getLogs ranges of 1 event each; depositCount must match 3.
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		switch method {
		case rpcMethodEthGetLogs:
			return []map[string]string{{
				"data":            makeBridgeEventData(0, 0, 1, 0, 50),
				"blockNumber":     "0x1",
				"transactionHash": common.HexToHash("0xaaa").Hex(),
			}}
		case rpcMethodEthCall:
			return depositCountWord(3)
		}
		require.Failf(t, "unexpected RPC method", "%s", method)
		return nil
	})
	cfg := &Config{L1RPCURL: url, L2NetworkID: 1, L1BridgeAddress: common.HexToAddress("0xbridge"),
		Options: Options{BlockRange: 50, ConcurrencyLimit: 2, L1StartBlock: 0}}
	deposits, err := fetchL1BridgeEvents(context.Background(), cfg, 100)
	require.NoError(t, err)
	require.Len(t, deposits, 3)
}

func TestFetchL1BridgeEventsDepositCountMismatch(t *testing.T) {
	t.Parallel()
	// The scan finds 0 events (the wrong-l1BridgeAddress symptom) but the bridge reports
	// depositCount()=5 → the scan cannot be trusted and the step must error.
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		switch method {
		case rpcMethodEthGetLogs:
			return []map[string]string{}
		case rpcMethodEthCall:
			return depositCountWord(5)
		}
		return nil
	})
	cfg := &Config{L1RPCURL: url, L2NetworkID: 1, L1BridgeAddress: common.HexToAddress("0xbridge"),
		Options: Options{BlockRange: 200, ConcurrencyLimit: 2}}
	_, err := fetchL1BridgeEvents(context.Background(), cfg, 100)
	require.Error(t, err)
	require.Contains(t, err.Error(), "depositCount")
	require.Contains(t, err.Error(), "l1BridgeAddress")
}

func TestFetchL1BridgeEventsNoContractAtL1BridgeAddress(t *testing.T) {
	t.Parallel()
	// eth_call on an address with no code returns empty data at every block → fatal error.
	url := newBatchRPCServer(t, func(method string, _ []json.RawMessage) any {
		switch method {
		case rpcMethodEthGetLogs:
			return []map[string]string{}
		case rpcMethodEthCall:
			return "0x"
		}
		return nil
	})
	cfg := &Config{L1RPCURL: url, L2NetworkID: 1, L1BridgeAddress: common.HexToAddress("0xdead"),
		Options: Options{BlockRange: 200, ConcurrencyLimit: 2}}
	_, err := fetchL1BridgeEvents(context.Background(), cfg, 100)
	require.Error(t, err)
	require.Contains(t, err.Error(), "l1BridgeAddress is probably wrong")
}

func TestVerifyL1BridgeDepositCountWithStartBlock(t *testing.T) {
	t.Parallel()
	// depositCount()=2 at block 9 (l1StartBlock-1) and 5 at block 100 → the scan over 10→100 must
	// have found exactly 3 events.
	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		require.Equal(t, rpcMethodEthCall, method)
		var blockTag string
		require.NoError(t, json.Unmarshal(params[1], &blockTag))
		if blockTag == "0x9" {
			return depositCountWord(2)
		}
		return depositCountWord(5)
	})
	cfg := &Config{L1RPCURL: url, L1BridgeAddress: common.HexToAddress("0xbridge"),
		Options: Options{L1StartBlock: 10}}

	require.NoError(t, verifyL1BridgeDepositCount(context.Background(), cfg, 100, 3))

	err := verifyL1BridgeDepositCount(context.Background(), cfg, 100, 2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mismatch")
}

func TestVerifyL1BridgeDepositCountPrunedStateFallsBackToLatest(t *testing.T) {
	t.Parallel()
	// Historical state unavailable (empty return) but latest works → upper-bound check against
	// depositCount at latest.
	url := newBatchRPCServer(t, func(method string, params []json.RawMessage) any {
		require.Equal(t, rpcMethodEthCall, method)
		var blockTag string
		require.NoError(t, json.Unmarshal(params[1], &blockTag))
		if blockTag == "latest" {
			return depositCountWord(5)
		}
		return "0x"
	})
	cfg := &Config{L1RPCURL: url, L1BridgeAddress: common.HexToAddress("0xbridge")}

	require.NoError(t, verifyL1BridgeDepositCount(context.Background(), cfg, 100, 4))

	err := verifyL1BridgeDepositCount(context.Background(), cfg, 100, 9)
	require.Error(t, err)
	require.Contains(t, err.Error(), "inconsistent scan")
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

	got, err := fetchZkevmPendingBridges(context.Background(), srv.URL, 5, leafTypeAsset, 0)
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
	err := checkBridgeServicePendingBridges(context.Background(), cfg, []L1Deposit{{DepositCount: 7}}, 0)
	require.NoError(t, err)

	// L1 scan found a different set → mismatch error
	err = checkBridgeServicePendingBridges(context.Background(), cfg, []L1Deposit{{DepositCount: 8}}, 0)
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

	got, err := fetchAggkitPendingBridges(context.Background(), cfg, svc.URL, leafTypeAsset, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	_, ok := got[20]
	require.True(t, ok)
}

func TestFetchZkevmPendingBridgesCutoff(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deposits": []map[string]any{
				{"deposit_cnt": 10, "block_num": "100"},
				{"deposit_cnt": 11, "block_num": "500"}, // past the cutoff → dropped
				{"deposit_cnt": 12, "block_num": "300"},
			},
			"total_cnt": "3",
		})
	}))
	t.Cleanup(srv.Close)

	got, err := fetchZkevmPendingBridges(context.Background(), srv.URL, 5, leafTypeAsset, 300)
	require.NoError(t, err)
	require.Len(t, got, 2)
	_, ok := got[11]
	require.False(t, ok, "deposit 11 is past the cutoff")
}

func TestFetchZkevmPendingBridgesCutoffBadBlockNum(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deposits":  []map[string]any{{"deposit_cnt": 10, "block_num": "not-a-number"}},
			"total_cnt": "1",
		})
	}))
	t.Cleanup(srv.Close)

	_, err := fetchZkevmPendingBridges(context.Background(), srv.URL, 5, leafTypeAsset, 300)
	require.Error(t, err)
	require.Contains(t, err.Error(), "block_num")
}

func TestFetchAggkitPendingBridgesCutoff(t *testing.T) {
	t.Parallel()
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"bridges": []map[string]any{
				{"deposit_count": 20, "destination_network": 1, "block_num": 100},
				{"deposit_count": 21, "destination_network": 1, "block_num": 500}, // past the cutoff → dropped
			},
			"count": 2,
		})
	}))
	t.Cleanup(svc.Close)

	// isClaimed: nothing claimed.
	rpc := newBatchRPCServer(t, func(_ string, _ []json.RawMessage) any {
		return "0x0000000000000000000000000000000000000000000000000000000000000000"
	})

	cfg := &Config{L2RPCURL: rpc, L2NetworkID: 1, L2BridgeAddress: common.HexToAddress("0xbridge"),
		Options: Options{RPCBatchSize: 10, ConcurrencyLimit: 2}}

	got, err := fetchAggkitPendingBridges(context.Background(), cfg, svc.URL, leafTypeAsset, 300)
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
	_, err := fetchZkevmPendingBridges(context.Background(), srv.URL, 1, leafTypeAsset, 0)
	require.Error(t, err)
}
