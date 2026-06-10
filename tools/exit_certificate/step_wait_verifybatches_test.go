package exit_certificate

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// verifyBatchesData builds the ABI-encoded data of a VerifyBatchesTrustedAggregator log:
// numBatch (uint64) + stateRoot (bytes32) + exitRoot (bytes32), each padded to 32 bytes.
func verifyBatchesData(numBatch uint64, stateRoot, exitRoot common.Hash) []byte {
	data := make([]byte, verifyBatchesDataLen)
	big.NewInt(0).SetUint64(numBatch).FillBytes(data[0:32])
	copy(data[32:64], stateRoot.Bytes())
	copy(data[64:96], exitRoot.Bytes())
	return data
}

// logsResult marshals a single eth_getLogs entry as the JSON array an RPC node returns.
func logsResult(t *testing.T, blockNumber uint64, txHash common.Hash, data []byte) json.RawMessage {
	t.Helper()
	out, err := json.Marshal([]map[string]string{{
		"blockNumber":     toBlockTag(blockNumber),
		"transactionHash": txHash.Hex(),
		"data":            "0x" + common.Bytes2Hex(data),
	}})
	require.NoError(t, err)
	return out
}

// topicLogsResult marshals an eth_getLogs array with explicit topics and data (for the GER events).
func topicLogsResult(t *testing.T, txHash common.Hash, topics []common.Hash, data []byte) json.RawMessage {
	t.Helper()
	hexTopics := make([]string, len(topics))
	for i, tp := range topics {
		hexTopics[i] = tp.Hex()
	}
	out, err := json.Marshal([]map[string]any{{
		"transactionHash": txHash.Hex(),
		"topics":          hexTopics,
		"data":            "0x" + common.Bytes2Hex(data),
	}})
	require.NoError(t, err)
	return out
}

// getLogsTopic0 returns the topics[0] filter of an eth_getLogs request.
func getLogsTopic0(params []any) string {
	filter := params[0].(map[string]any)
	return filter["topics"].([]any)[0].(string)
}

// v2Data builds the data of an UpdateL1InfoTreeV2 log: currentL1InfoRoot ++ blockhash ++ minTimestamp.
func v2Data(currentL1InfoRoot, blockhash common.Hash, minTimestamp uint64) []byte {
	data := make([]byte, 96)
	copy(data[0:32], currentL1InfoRoot.Bytes())
	copy(data[32:64], blockhash.Bytes())
	big.NewInt(0).SetUint64(minTimestamp).FillBytes(data[64:96])
	return data
}

func TestResolveRollupManagerAddress(t *testing.T) {
	t.Parallel()
	rollupManager := common.HexToAddress("0x5132A183E9F3CB7C848b0AAC5Ae0c4f0491B7aB2")
	sovereign := common.HexToAddress("0xA13Ddb14437A8F34897131367ad3ca78416d6bCa")

	t.Run("configured address short-circuits without RPC", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			t.Fatal("no RPC call expected when rollupManagerAddress is set")
			return nil, nil
		})
		cfg := &Config{L1RPCURL: srv.URL, RollupManagerAddress: rollupManager, SovereignRollupAddr: sovereign}
		got, err := resolveRollupManagerAddress(context.Background(), cfg)
		require.NoError(t, err)
		require.Equal(t, rollupManager, got)
	})

	t.Run("resolves from sovereignRollupAddr", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, "eth_call", method)
			call := params[0].(map[string]any)
			require.Equal(t, sovereign.Hex(), call["to"])
			require.Equal(t, rollupManagerSelector, call["data"])
			return hexResult(common.LeftPadBytes(rollupManager.Bytes(), 32)), nil
		})
		cfg := &Config{L1RPCURL: srv.URL, SovereignRollupAddr: sovereign}
		got, err := resolveRollupManagerAddress(context.Background(), cfg)
		require.NoError(t, err)
		require.Equal(t, rollupManager, got)
	})

	t.Run("neither address set returns zero without error", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{L1RPCURL: "http://unused"}
		got, err := resolveRollupManagerAddress(context.Background(), cfg)
		require.NoError(t, err)
		require.Equal(t, common.Address{}, got)
	})

	t.Run("rollupManager() returning zero is an error", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return hexResult(make([]byte, 32)), nil
		})
		cfg := &Config{L1RPCURL: srv.URL, SovereignRollupAddr: sovereign}
		_, err := resolveRollupManagerAddress(context.Background(), cfg)
		require.Error(t, err)
	})
}

func TestResolveFinalizedBlock(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, "eth_getBlockByNumber", method)
			require.Equal(t, "finalized", params[0])
			return json.RawMessage(`{"number":"0x10"}`), nil
		})
		n, err := resolveFinalizedBlock(context.Background(), srv.URL)
		require.NoError(t, err)
		require.Equal(t, uint64(16), n)
	})

	t.Run("null block is an error", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return json.RawMessage(`{}`), nil
		})
		_, err := resolveFinalizedBlock(context.Background(), srv.URL)
		require.Error(t, err)
	})
}

func TestQueryVerifyBatches(t *testing.T) {
	t.Parallel()
	contract := common.HexToAddress("0x5132A183E9F3CB7C848b0AAC5Ae0c4f0491B7aB2")
	exitRoot := common.HexToHash("0xabc123")
	txHash := common.HexToHash("0xdead")

	t.Run("matching exit root is found", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, "eth_getLogs", method)
			filter := params[0].(map[string]any)
			require.Equal(t, contract.Hex(), filter["address"])
			topics := filter["topics"].([]any)
			require.Equal(t, verifyBatchesTrustedAggregatorTopic.Hex(), topics[0])
			// topics[1] is the indexed rollupID (5) as a 32-byte value.
			require.Equal(t, common.BigToHash(big.NewInt(5)).Hex(), topics[1])
			return logsResult(t, 42, txHash, verifyBatchesData(7, common.Hash{}, exitRoot)), nil
		})
		block, tx, found, err := queryVerifyBatches(
			context.Background(), srv.URL, contract, 5, exitRoot, 0, 100)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, uint64(42), block)
		require.Equal(t, txHash, tx)
	})

	t.Run("non-matching exit root is not found", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return logsResult(t, 42, txHash, verifyBatchesData(7, common.Hash{}, common.HexToHash("0x999"))), nil
		})
		_, _, found, err := queryVerifyBatches(
			context.Background(), srv.URL, contract, 5, exitRoot, 0, 100)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("no logs is not found", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
			return json.RawMessage(`[]`), nil
		})
		_, _, found, err := queryVerifyBatches(
			context.Background(), srv.URL, contract, 5, exitRoot, 0, 100)
		require.NoError(t, err)
		require.False(t, found)
	})
}

func TestConfirmVerifyBatchesOnL1(t *testing.T) {
	t.Parallel()
	sovereign := common.HexToAddress("0xA13Ddb14437A8F34897131367ad3ca78416d6bCa")
	rollupManager := common.HexToAddress("0x5132A183E9F3CB7C848b0AAC5Ae0c4f0491B7aB2")
	exitRoot := common.HexToHash("0xabc123")
	txHash := common.HexToHash("0xbeef")

	t.Run("errors when l1RpcUrl is unset", func(t *testing.T) {
		t.Parallel()
		result := &StepWaitResult{}
		err := confirmVerifyBatchesOnL1(context.Background(), &Config{}, &StepSubmitResult{}, exitRoot, result)
		require.Error(t, err)
		require.Contains(t, err.Error(), "l1RpcUrl")
	})

	t.Run("errors when no rollup manager can be resolved", func(t *testing.T) {
		t.Parallel()
		result := &StepWaitResult{}
		cfg := &Config{L1RPCURL: "http://unused"} // no rollupManagerAddress, no sovereignRollupAddr
		err := confirmVerifyBatchesOnL1(context.Background(), cfg, &StepSubmitResult{}, exitRoot, result)
		require.Error(t, err)
		require.Nil(t, result.VerifyBatchesTxHash)
	})

	t.Run("resolves manager from sovereign, finds the event and the GER updates", func(t *testing.T) {
		t.Parallel()
		ger := common.HexToAddress("0xDDDdDddddddddDddDDddDDDDdDdDDdDDdDDDDddddD")
		mainnetExitRoot := common.HexToHash("0x1111")
		rollupExitRoot := common.HexToHash("0x2222")
		currentL1InfoRoot := common.HexToHash("0x3333")
		gerTxHash := common.HexToHash("0xfeed")

		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			switch method {
			case "eth_call":
				return hexResult(common.LeftPadBytes(rollupManager.Bytes(), 32)), nil
			case "eth_getBlockByNumber":
				return json.RawMessage(`{"number":"0x64"}`), nil // finalized = 100
			case "eth_getLogs":
				switch getLogsTopic0(params) {
				case verifyBatchesTrustedAggregatorTopic.Hex():
					return logsResult(t, 50, txHash, verifyBatchesData(1, common.Hash{}, exitRoot)), nil
				case updateL1InfoTreeTopic.Hex():
					return topicLogsResult(t, gerTxHash,
						[]common.Hash{updateL1InfoTreeTopic, mainnetExitRoot, rollupExitRoot}, nil), nil
				case updateL1InfoTreeV2TopicWait.Hex():
					return topicLogsResult(t, gerTxHash,
						[]common.Hash{updateL1InfoTreeV2TopicWait, common.BigToHash(big.NewInt(9))},
						v2Data(currentL1InfoRoot, common.Hash{}, 1700)), nil
				}
			}
			t.Fatalf("unexpected call %s %v", method, params)
			return nil, nil
		})
		cfg := &Config{
			L1RPCURL:                srv.URL,
			SovereignRollupAddr:     sovereign,
			L1GlobalExitRootAddress: ger,
			L2NetworkID:             5,
			Options:                 Options{BlockRange: 50},
		}
		submit := &StepSubmitResult{L1LatestBlockBeforeSubmittingCertificate: 10}
		result := &StepWaitResult{}
		err := confirmVerifyBatchesOnL1(context.Background(), cfg, submit, exitRoot, result)
		require.NoError(t, err)
		require.Equal(t, uint64(50), result.VerifyBatchesL1Block)
		require.NotNil(t, result.VerifyBatchesTxHash)
		require.Equal(t, txHash, *result.VerifyBatchesTxHash)

		require.NotNil(t, result.UpdateL1InfoTree)
		require.Equal(t, mainnetExitRoot, result.UpdateL1InfoTree.MainnetExitRoot)
		require.Equal(t, rollupExitRoot, result.UpdateL1InfoTree.RollupExitRoot)

		require.NotNil(t, result.UpdateL1InfoTreeV2)
		require.Equal(t, currentL1InfoRoot, result.UpdateL1InfoTreeV2.CurrentL1InfoRoot)
		require.Equal(t, uint32(9), result.UpdateL1InfoTreeV2.LeafCount)
		require.Equal(t, uint64(1700), result.UpdateL1InfoTreeV2.MinTimestamp)
	})

	t.Run("errors when l1GlobalExitRootAddress is unset", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			switch method {
			case "eth_call":
				return hexResult(common.LeftPadBytes(rollupManager.Bytes(), 32)), nil
			case "eth_getBlockByNumber":
				return json.RawMessage(`{"number":"0x64"}`), nil
			case "eth_getLogs":
				return logsResult(t, 50, txHash, verifyBatchesData(1, common.Hash{}, exitRoot)), nil
			}
			return nil, nil
		})
		cfg := &Config{
			L1RPCURL:            srv.URL,
			SovereignRollupAddr: sovereign,
			L2NetworkID:         5,
			Options:             Options{BlockRange: 50},
		}
		submit := &StepSubmitResult{L1LatestBlockBeforeSubmittingCertificate: 10}
		err := confirmVerifyBatchesOnL1(context.Background(), cfg, submit, exitRoot, &StepWaitResult{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "l1GlobalExitRootAddress")
	})
}

func TestFetchGERUpdatesInBlock(t *testing.T) {
	t.Parallel()
	ger := common.HexToAddress("0xDDDdDddddddddDddDDddDDDDdDdDDdDDdDDDDddddD")
	gerTxHash := common.HexToHash("0xfeed")

	t.Run("takes the last event of each type", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			require.Equal(t, "eth_getLogs", method)
			switch getLogsTopic0(params) {
			case updateL1InfoTreeTopic.Hex():
				// Two events; the last one must win.
				out, err := json.Marshal([]map[string]any{
					{"transactionHash": gerTxHash.Hex(), "topics": []string{
						updateL1InfoTreeTopic.Hex(), common.HexToHash("0xaa").Hex(), common.HexToHash("0xbb").Hex()}, "data": "0x"},
					{"transactionHash": gerTxHash.Hex(), "topics": []string{
						updateL1InfoTreeTopic.Hex(), common.HexToHash("0xcc").Hex(), common.HexToHash("0xdd").Hex()}, "data": "0x"},
				})
				require.NoError(t, err)
				return out, nil
			case updateL1InfoTreeV2TopicWait.Hex():
				return topicLogsResult(t, gerTxHash,
					[]common.Hash{updateL1InfoTreeV2TopicWait, common.BigToHash(big.NewInt(42))},
					v2Data(common.HexToHash("0xee"), common.HexToHash("0xff"), 12345)), nil
			}
			t.Fatalf("unexpected topic")
			return nil, nil
		})
		cfg := &Config{L1RPCURL: srv.URL, L1GlobalExitRootAddress: ger}
		result := &StepWaitResult{}
		require.NoError(t, fetchGERUpdatesInBlock(context.Background(), cfg, 50, result))

		require.Equal(t, common.HexToHash("0xcc"), result.UpdateL1InfoTree.MainnetExitRoot)
		require.Equal(t, common.HexToHash("0xdd"), result.UpdateL1InfoTree.RollupExitRoot)
		require.Equal(t, uint32(42), result.UpdateL1InfoTreeV2.LeafCount)
		require.Equal(t, common.HexToHash("0xee"), result.UpdateL1InfoTreeV2.CurrentL1InfoRoot)
		require.Equal(t, common.HexToHash("0xff"), result.UpdateL1InfoTreeV2.Blockhash)
		require.Equal(t, uint64(12345), result.UpdateL1InfoTreeV2.MinTimestamp)
	})

	t.Run("missing UpdateL1InfoTree is an error", func(t *testing.T) {
		t.Parallel()
		srv := newRPCStub(t, func(method string, params []any) (json.RawMessage, *jsonRPCError) {
			return json.RawMessage(`[]`), nil // no logs of any kind
		})
		cfg := &Config{L1RPCURL: srv.URL, L1GlobalExitRootAddress: ger}
		err := fetchGERUpdatesInBlock(context.Background(), cfg, 50, &StepWaitResult{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "UpdateL1InfoTree")
	})
}
