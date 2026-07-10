package exit_certificate

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// newBatchRPCStub starts an httptest server that handles both single and batched JSON-RPC requests,
// dispatching every call to respond. concurrentBatchRPC (used by isClaimed) sends batches, which the
// single-request newRPCStub cannot decode.
func newBatchRPCStub(t *testing.T, respond rpcResponder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")

		trimmed := strings.TrimLeft(string(raw), " \t\r\n")
		if strings.HasPrefix(trimmed, "[") {
			var reqs []jsonRPCRequest
			require.NoError(t, json.Unmarshal(raw, &reqs))
			resps := make([]jsonRPCResponse, len(reqs))
			for i, req := range reqs {
				params, _ := req.Params.([]any)
				result, rpcErr := respond(req.Method, params)
				resps[i] = jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
			}
			_ = json.NewEncoder(w).Encode(resps)
			return
		}

		var req jsonRPCRequest
		require.NoError(t, json.Unmarshal(raw, &req))
		params, _ := req.Params.([]any)
		result, rpcErr := respond(req.Method, params)
		_ = json.NewEncoder(w).Encode(jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// bridgeEventData builds the 256-byte ABI payload of a BridgeEvent log: the metadata offset is set
// past the end of the buffer so extractMetadata yields no metadata, keeping the fixture minimal.
func bridgeEventData(leafType uint8, originNetwork, destNetwork, depositCount uint32, amount *big.Int) []byte {
	data := make([]byte, 256)
	data[31] = leafType
	big.NewInt(int64(originNetwork)).FillBytes(data[32:64])
	// originAddress data[64:96] left zero (native token)
	big.NewInt(int64(destNetwork)).FillBytes(data[96:128])
	// destAddress data[128:160] left zero
	if amount != nil {
		amount.FillBytes(data[160:192])
	}
	big.NewInt(256).FillBytes(data[192:224]) // metadataOffset past end → no metadata
	big.NewInt(int64(depositCount)).FillBytes(data[224:256])
	return data
}

// bridgeLogsResult marshals a single BridgeEvent log entry as eth_getLogs returns it.
func bridgeLogsResult(t *testing.T, data []byte) json.RawMessage {
	t.Helper()
	out, err := json.Marshal([]map[string]string{{
		"data":            "0x" + common.Bytes2Hex(data),
		"blockNumber":     "0x1",
		"transactionHash": common.HexToHash("0xabc").Hex(),
	}})
	require.NoError(t, err)
	return out
}

// claimedResult encodes the isClaimed eth_call return value (non-zero = claimed).
func claimedResult(claimed bool) json.RawMessage {
	if claimed {
		return quoted("0x0000000000000000000000000000000000000000000000000000000000000001")
	}
	return quoted("0x0000000000000000000000000000000000000000000000000000000000000000")
}

// stepEConfig builds a Config wired to the given stub URL for both L1 and L2 RPC.
func stepEConfig(url string) *Config {
	return &Config{
		L1RPCURL:        url,
		L2RPCURL:        url,
		L1BridgeAddress: common.HexToAddress("0xbridge"),
		L2BridgeAddress: common.HexToAddress("0xbridge"),
		L2NetworkID:     1,
		Options: Options{
			L1StartBlock:     0,
			BlockRange:       5000,
			RPCBatchSize:     200,
			ConcurrencyLimit: 4,
		},
	}
}

func emptyCert() *agglayertypes.Certificate {
	return &agglayertypes.Certificate{NetworkID: 1}
}

func TestRunStepE_NoDeposits(t *testing.T) {
	t.Parallel()
	srv := newBatchRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthBlockNumber:
			return quoted("0x10"), nil
		case rpcMethodEthGetLogs:
			return json.RawMessage(`[]`), nil
		default:
			t.Fatalf("unexpected method %s", method)
			return nil, nil
		}
	})

	res, err := RunStepE(context.Background(), stepEConfig(srv.URL), emptyCert())
	require.NoError(t, err)
	require.Empty(t, res.UnclaimedBridges)
	require.NotNil(t, res.FinalCertificate)
}

func TestRunStepE_AllClaimed(t *testing.T) {
	t.Parallel()
	data := bridgeEventData(0, 0, 1, 7, big.NewInt(100))
	srv := newBatchRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthBlockNumber:
			return quoted("0x10"), nil
		case rpcMethodEthGetLogs:
			return bridgeLogsResult(t, data), nil
		case rpcMethodEthCall:
			return claimedResult(true), nil
		default:
			t.Fatalf("unexpected method %s", method)
			return nil, nil
		}
	})

	res, err := RunStepE(context.Background(), stepEConfig(srv.URL), emptyCert())
	require.NoError(t, err)
	require.Empty(t, res.UnclaimedBridges)
}

func TestRunStepE_UnclaimedAssetErrors(t *testing.T) {
	t.Parallel()
	data := bridgeEventData(0, 0, 1, 7, big.NewInt(100))
	srv := newBatchRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthBlockNumber:
			return quoted("0x10"), nil
		case rpcMethodEthGetLogs:
			return bridgeLogsResult(t, data), nil
		case rpcMethodEthCall:
			return claimedResult(false), nil
		default:
			t.Fatalf("unexpected method %s", method)
			return nil, nil
		}
	})

	res, err := RunStepE(context.Background(), stepEConfig(srv.URL), emptyCert())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unclaimed deposits not supported")
	require.Len(t, res.UnclaimedBridges, 1)
}

func TestRunStepE_UnclaimedAssetIgnored(t *testing.T) {
	t.Parallel()
	data := bridgeEventData(0, 0, 1, 7, big.NewInt(100))
	cfg := stepEConfig("")
	srv := newBatchRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthBlockNumber:
			return quoted("0x10"), nil
		case rpcMethodEthGetLogs:
			return bridgeLogsResult(t, data), nil
		case rpcMethodEthCall:
			return claimedResult(false), nil
		default:
			return quoted("0x"), nil
		}
	})
	cfg.L1RPCURL = srv.URL
	cfg.L2RPCURL = srv.URL
	cfg.Options.IgnoreUnclaimed = true

	res, err := RunStepE(context.Background(), cfg, emptyCert())
	require.NoError(t, err)
	require.Len(t, res.UnclaimedBridges, 1)
	require.NotNil(t, res.FinalCertificate)
}

func TestRunStepE_UnclaimedMessagesOnly(t *testing.T) {
	t.Parallel()
	// leaf_type=1 (message) → excluded from certificate, no asset error.
	data := bridgeEventData(1, 0, 1, 9, big.NewInt(0))
	srv := newBatchRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthBlockNumber:
			return quoted("0x10"), nil
		case rpcMethodEthGetLogs:
			return bridgeLogsResult(t, data), nil
		case rpcMethodEthCall:
			return claimedResult(false), nil
		default:
			return quoted("0x"), nil
		}
	})

	res, err := RunStepE(context.Background(), stepEConfig(srv.URL), emptyCert())
	require.NoError(t, err)
	require.Empty(t, res.UnclaimedBridges)
	require.Len(t, res.UnclaimedMessages, 1)
}

func TestRunStepE_BridgeServiceMatch(t *testing.T) {
	t.Parallel()
	// One unclaimed asset on L1; the aggkit bridge service reports the same deposit count, so the
	// cross-check passes and (with IgnoreUnclaimed) the step succeeds.
	data := bridgeEventData(0, 0, 1, 7, big.NewInt(100))

	bridgeSvc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.Contains(r.URL.Path, "/bridge/v1/bridges"))
		resp := aggkitBridgesResult{
			Bridges: []*aggkitBridgeEntry{{DepositCount: 7, DestinationNetwork: 1, LeafType: 0}},
			Count:   1,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer bridgeSvc.Close()

	srv := newBatchRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		switch method {
		case rpcMethodEthBlockNumber:
			return quoted("0x10"), nil
		case rpcMethodEthGetLogs:
			return bridgeLogsResult(t, data), nil
		case rpcMethodEthCall:
			return claimedResult(false), nil
		default:
			return quoted("0x"), nil
		}
	})

	cfg := stepEConfig(srv.URL)
	cfg.Options.IgnoreUnclaimed = true
	cfg.Options.BridgeServiceURL = bridgeSvc.URL
	cfg.Options.BridgeServiceType = BridgeServiceTypeAggkit

	res, err := RunStepE(context.Background(), cfg, emptyCert())
	require.NoError(t, err)
	require.Len(t, res.UnclaimedBridges, 1)
}

func TestRunStepE_L1BlockError(t *testing.T) {
	t.Parallel()
	srv := newBatchRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return nil, revertErr()
	})
	_, err := RunStepE(context.Background(), stepEConfig(srv.URL), emptyCert())
	require.Error(t, err)
}
