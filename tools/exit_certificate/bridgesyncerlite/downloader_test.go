package bridgesyncerlite

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// --- fake JSON-RPC server ------------------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// newRPCServer spins up an httptest server that answers eth_blockNumber and eth_getLogs from the
// supplied closures. It handles both single and batched JSON-RPC requests so it works regardless of
// how go-ethereum frames the call.
func newRPCServer(t *testing.T, blockNumber func() uint64, getLogs func() []types.Log) *httptest.Server {
	t.Helper()
	answer := func(req rpcRequest) rpcResponse {
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "eth_blockNumber":
			resp.Result = hexutil.Uint64(blockNumber())
		case "eth_getLogs":
			resp.Result = getLogs()
		default:
			resp.Result = nil
		}
		return resp
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")

		trimmed := bytes.TrimSpace(body)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			var reqs []rpcRequest
			require.NoError(t, json.Unmarshal(trimmed, &reqs))
			resps := make([]rpcResponse, len(reqs))
			for i, req := range reqs {
				resps[i] = answer(req)
			}
			require.NoError(t, json.NewEncoder(w).Encode(resps))
			return
		}
		var req rpcRequest
		require.NoError(t, json.Unmarshal(trimmed, &req))
		require.NoError(t, json.NewEncoder(w).Encode(answer(req)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// packBridgeEventLog builds a types.Log carrying an ABI-encoded BridgeEvent payload, matching what
// the bridge contract emits on chain.
func packBridgeEventLog(t *testing.T, leaf BridgeLeaf) types.Log {
	t.Helper()
	abi, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)
	data, err := abi.Events["BridgeEvent"].Inputs.Pack(
		leaf.LeafType, leaf.OriginNetwork, leaf.OriginAddress, leaf.DestinationNetwork,
		leaf.DestinationAddress, leaf.Amount, leaf.Metadata, leaf.DepositCount,
	)
	require.NoError(t, err)
	return types.Log{
		Address:     common.HexToAddress("0xbeef"),
		Topics:      []common.Hash{bridgeEventSignature},
		Data:        data,
		BlockNumber: leaf.BlockNum,
		TxHash:      leaf.TxHash,
		Index:       uint(leaf.BlockPos),
	}
}

// --- parseBridgeEvent / classifyLogs unit coverage -----------------------------------------------

func TestParseBridgeEvent(t *testing.T) {
	contract, err := agglayerbridge.NewAgglayerbridge(common.Address{}, nil)
	require.NoError(t, err)

	want := newTestLeaf(7)
	logEntry := packBridgeEventLog(t, want)
	got, err := parseBridgeEvent(contract, logEntry)
	require.NoError(t, err)

	require.Equal(t, want.LeafType, got.LeafType)
	require.Equal(t, want.OriginNetwork, got.OriginNetwork)
	require.Equal(t, want.OriginAddress, got.OriginAddress)
	require.Equal(t, want.DestinationNetwork, got.DestinationNetwork)
	require.Equal(t, want.DestinationAddress, got.DestinationAddress)
	require.Equal(t, want.Amount, got.Amount)
	require.Equal(t, want.Metadata, got.Metadata)
	require.Equal(t, want.DepositCount, got.DepositCount)
	require.Equal(t, want.TxHash, got.TxHash)
	require.Equal(t, want.BlockNum, got.BlockNum)
	require.Equal(t, want.BlockPos, got.BlockPos)
	require.Equal(t, want.Hash(), got.Hash())
}

func TestParseBridgeEventBadData(t *testing.T) {
	contract, err := agglayerbridge.NewAgglayerbridge(common.Address{}, nil)
	require.NoError(t, err)
	bad := types.Log{Topics: []common.Hash{bridgeEventSignature}, Data: []byte{0x01, 0x02}}
	_, err = parseBridgeEvent(contract, bad)
	require.Error(t, err)
}

// TestClassifyLogsParsesBridgeEvent covers the full classify→parse→append happy path with a real
// ABI-encoded BridgeEvent log.
func TestClassifyLogsParsesBridgeEvent(t *testing.T) {
	contract, err := agglayerbridge.NewAgglayerbridge(common.Address{}, nil)
	require.NoError(t, err)

	leaf := newTestLeaf(3)
	logs := []types.Log{
		{Topics: []common.Hash{common.HexToHash("0xdeadbeef")}}, // unrelated → ignored
		packBridgeEventLog(t, leaf),
	}
	out, err := classifyLogs(contract, logs, false, nil)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, leaf.Hash(), out[0].Hash())
}

func TestString(t *testing.T) {
	leaf := newTestLeaf(2)
	require.Contains(t, leaf.String(), "BridgeLeaf{")
	require.Contains(t, leaf.String(), leaf.OriginAddress.Hex())
	require.Contains(t, leaf.String(), leaf.Amount.String())

	leaf.Amount = nil
	require.Contains(t, leaf.String(), "Amount: nil")
}

func TestReportFetchProgressReturnsOnCancel(t *testing.T) {
	s := &BridgeSyncerLite{log: log.WithFields("module", "bridgesyncerlite-test")}
	ctx, cancel := context.WithCancel(context.Background())
	var completed atomic.Int64
	done := make(chan struct{})
	go func() {
		s.reportFetchProgress(ctx, time.Now(), &completed, 10, 0, 100)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reportFetchProgress did not return after cancel")
	}
}
