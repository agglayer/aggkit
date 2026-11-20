package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// testOtherBlockHash is a block hash different from testBlockHash: the one the contradictory
// upstream data of these tests reports instead
var testOtherBlockHash = common.HexToHash("0xbbbb567890123456789012345678901234567890123456789012345678901234")

// bridgeReceiptBlockNumber is the block testBlockHash's receipt and BridgeEvent log are mined in
const bridgeReceiptBlockNumber = uint64(12345)

// bridgeBlockHeader is the header of the receipt's block as the RPC reports it, coherent with
// bridgeEventLog and coherentBridgeReceipt unless a test alters it
func bridgeBlockHeader() *aggkittypes.BlockHeader {
	return &aggkittypes.BlockHeader{
		Number: bridgeReceiptBlockNumber, Hash: testBlockHash, Time: testBlockTimestamp,
	}
}

// expectBridgeBlockHeader stubs client's CustomHeaderByNumber for the receipt's block to report
// bridgeBlockHeader, the way FindBridge resolves the block of an accepted BridgeEvent
func expectBridgeBlockHeader(client *mocks.BaseEthereumClienter) {
	expectBridgeBlockHeaderReturns(client, bridgeBlockHeader())
}

func expectBridgeBlockHeaderReturns(client *mocks.BaseEthereumClienter, header *aggkittypes.BlockHeader) {
	client.EXPECT().
		CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(bridgeReceiptBlockNumber)).
		Return(header, nil)
}

// coherentBridgeReceipt is a successful receipt of testTxHash whose single BridgeEvent log was
// mined in the same block, by the same tx
func coherentBridgeReceipt(t *testing.T) *gethtypes.Receipt {
	t.Helper()

	return &gethtypes.Receipt{
		Status:      gethtypes.ReceiptStatusSuccessful,
		BlockNumber: new(big.Int).SetUint64(bridgeReceiptBlockNumber),
		BlockHash:   testBlockHash,
		TxHash:      testTxHash,
		Logs:        []*gethtypes.Log{bridgeEventLog(t, 1, 7)},
	}
}

// TestBridgeEventSourceRejectsInconsistentUpstreamData checks that a receipt, BridgeEvent log and
// block header that do not describe one same block are rejected as a whole: ErrInvalidUpstreamData
// (transient, not a permanent not-a-bridge), no BridgeInfo
func TestBridgeEventSourceRejectsInconsistentUpstreamData(t *testing.T) {
	testCases := []struct {
		name string
		// mutate alters the coherent receipt
		mutate func(receipt *gethtypes.Receipt)
		// header is what the RPC answers for the receipt's block, nil if FindBridge must not
		// even ask for it (the receipt is rejected before)
		header *aggkittypes.BlockHeader
	}{
		{
			name: "log block hash differs from the receipt's",
			mutate: func(receipt *gethtypes.Receipt) {
				receipt.Logs[0].BlockHash = testOtherBlockHash
			},
		},
		{
			name: "receipt block hash differs from the log's",
			mutate: func(receipt *gethtypes.Receipt) {
				receipt.BlockHash = testOtherBlockHash
			},
		},
		{
			name: "log block number differs from the receipt's",
			mutate: func(receipt *gethtypes.Receipt) {
				receipt.Logs[0].BlockNumber = bridgeReceiptBlockNumber + 1
			},
		},
		{
			name: "receipt block number differs from the log's",
			mutate: func(receipt *gethtypes.Receipt) {
				receipt.BlockNumber = new(big.Int).SetUint64(bridgeReceiptBlockNumber - 1)
			},
		},
		{
			name: "receipt block number does not fit a uint64",
			mutate: func(receipt *gethtypes.Receipt) {
				// wraps to bridgeReceiptBlockNumber, which is final and equals the log's
				receipt.BlockNumber = new(big.Int).Add(
					new(big.Int).Lsh(big.NewInt(1), 64), new(big.Int).SetUint64(bridgeReceiptBlockNumber))
			},
		},
		{
			name: "log tx hash is not the tracked tx",
			mutate: func(receipt *gethtypes.Receipt) {
				receipt.Logs[0].TxHash = common.HexToHash("0xdead")
			},
		},
		{
			name: "header number differs from the receipt's block",
			header: &aggkittypes.BlockHeader{
				Number: bridgeReceiptBlockNumber + 1, Hash: testBlockHash, Time: testBlockTimestamp,
			},
		},
		{
			name: "header hash differs from the receipt's block (not the canonical block)",
			header: &aggkittypes.BlockHeader{
				Number: bridgeReceiptBlockNumber, Hash: testOtherBlockHash, Time: testBlockTimestamp,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			receipt := coherentBridgeReceipt(t)
			if tc.mutate != nil {
				tc.mutate(receipt)
			}

			// no HeaderByHash nor header expectation unless tc.header is set: the mock fails the
			// test on any other call, so a rejected receipt never reaches the header lookup
			client := mocks.NewBaseEthereumClienter(t)
			client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(receipt, nil)
			expectFinalized(client, bridgeReceiptBlockNumber)
			if tc.header != nil {
				expectBridgeBlockHeaderReturns(client, tc.header)
			}

			source := newBridgeEventSource(t, client)
			info, err := source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
			require.ErrorIs(t, err, bridgetracker.ErrInvalidUpstreamData)
			require.Nil(t, info)
			require.False(t, domain.IsPermanent(err), "a defective upstream may recover: the engine must retry")
			require.NotErrorIs(t, err, bridgetracker.ErrBridgeTxNotABridge)
			require.NotErrorIs(t, err, bridgetracker.ErrBridgeTxNotFound)
		})
	}
}

// TestBridgeEventSourceNilHeaderIsInvalidUpstreamData checks that a header lookup answering
// neither a header nor an error is treated as inconsistent upstream data, not dereferenced
func TestBridgeEventSourceNilHeaderIsInvalidUpstreamData(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(coherentBridgeReceipt(t), nil)
	expectFinalized(client, bridgeReceiptBlockNumber)
	expectBridgeBlockHeaderReturns(client, nil)

	source := newBridgeEventSource(t, client)
	info, err := source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
	require.ErrorIs(t, err, bridgetracker.ErrInvalidUpstreamData)
	require.Nil(t, info)
}

// TestBridgeEventSourceHeaderErrorIsTransient checks that a failed header lookup is a plain
// transient error (the engine retries), not an inconsistency verdict nor a permanent rejection
func TestBridgeEventSourceHeaderErrorIsTransient(t *testing.T) {
	headerErr := errors.New("header lookup failed")
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(coherentBridgeReceipt(t), nil)
	expectFinalized(client, bridgeReceiptBlockNumber)
	client.EXPECT().
		CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(bridgeReceiptBlockNumber)).
		Return(nil, headerErr)

	source := newBridgeEventSource(t, client)
	info, err := source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
	require.ErrorIs(t, err, headerErr)
	require.Nil(t, info)
	require.False(t, domain.IsPermanent(err))
	require.NotErrorIs(t, err, bridgetracker.ErrInvalidUpstreamData)
}

// TestBridgeEventSourceCoherentUpstreamData checks the accepted path: the receipt's block hash
// and number are the BridgeInfo's, and the timestamp is the one of the header fetched for the
// consistency check (the single header lookup, no HeaderByHash)
func TestBridgeEventSourceCoherentUpstreamData(t *testing.T) {
	client := mocks.NewBaseEthereumClienter(t)
	client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(coherentBridgeReceipt(t), nil)
	expectFinalized(client, bridgeReceiptBlockNumber+10)
	expectBridgeBlockHeaderReturns(client, &aggkittypes.BlockHeader{
		Number: bridgeReceiptBlockNumber, Hash: testBlockHash, Time: 1234567,
	})

	source := newBridgeEventSource(t, client)
	info, err := source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
	require.NoError(t, err)
	expected := l1ToL2Bridge()
	expected.BlockTimestamp = 1234567
	require.Equal(t, expected, info)
}

// TestBridgeEventSourceImpostorEmitterIsSkippedBeforeConsistencyChecks checks that a
// BridgeEvent-shaped log of another emitter is ignored whatever its block fields say — only the
// canonical emitter's log is ever checked against, and accepted from, the receipt
func TestBridgeEventSourceImpostorEmitterIsSkippedBeforeConsistencyChecks(t *testing.T) {
	realBridgeAddr := common.HexToAddress("0xB41D9E")
	newImpostorLog := func(t *testing.T) *gethtypes.Log {
		t.Helper()
		impostor := bridgeEventLog(t, 2, 9)
		impostor.Address = common.HexToAddress("0xBAD")
		impostor.BlockHash = testOtherBlockHash
		impostor.BlockNumber = bridgeReceiptBlockNumber + 1
		impostor.TxHash = common.HexToHash("0xdead")
		return impostor
	}
	newSource := func(t *testing.T, client *mocks.BaseEthereumClienter) *BridgeEventSource {
		t.Helper()
		source, err := NewBridgeEventSource(
			StaticClients{0: client}, aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock, nil,
			staticBridgeAddressResolver{addrs: map[uint32]common.Address{0: realBridgeAddr}})
		require.NoError(t, err)
		return source
	}

	t.Run("only an impostor log: not a bridge, no header lookup", func(t *testing.T) {
		receipt := coherentBridgeReceipt(t)
		receipt.Logs = []*gethtypes.Log{newImpostorLog(t)}
		client := mocks.NewBaseEthereumClienter(t)
		client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(receipt, nil)
		expectFinalized(client, bridgeReceiptBlockNumber)

		_, err := newSource(t, client).FindBridge(
			t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
		require.ErrorIs(t, err, bridgetracker.ErrBridgeTxNotABridge)
		require.NotErrorIs(t, err, bridgetracker.ErrInvalidUpstreamData)
	})

	t.Run("impostor next to the canonical coherent log: the canonical one is accepted", func(t *testing.T) {
		realLog := bridgeEventLog(t, 1, 7)
		realLog.Address = realBridgeAddr
		receipt := coherentBridgeReceipt(t)
		receipt.Logs = []*gethtypes.Log{newImpostorLog(t), realLog}
		client := mocks.NewBaseEthereumClienter(t)
		client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(receipt, nil)
		expectFinalized(client, bridgeReceiptBlockNumber)
		expectBridgeBlockHeader(client)

		info, err := newSource(t, client).FindBridge(
			t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
		require.NoError(t, err)
		require.Equal(t, uint32(1), info.DestinationNetwork)
		require.Equal(t, uint32(7), info.DepositCount)
	})

	t.Run("canonical emitter with an inconsistent log is still rejected", func(t *testing.T) {
		realLog := bridgeEventLog(t, 1, 7)
		realLog.Address = realBridgeAddr
		realLog.BlockHash = testOtherBlockHash
		receipt := coherentBridgeReceipt(t)
		receipt.Logs = []*gethtypes.Log{realLog}
		client := mocks.NewBaseEthereumClienter(t)
		client.EXPECT().TransactionReceipt(mock.Anything, testTxHash).Return(receipt, nil)
		expectFinalized(client, bridgeReceiptBlockNumber)

		info, err := newSource(t, client).FindBridge(
			t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
		require.ErrorIs(t, err, bridgetracker.ErrInvalidUpstreamData)
		require.Nil(t, info)
	})
}

// jsonRPCBlock is the eth_getBlockByNumber result of a block as a node serializes it (only the
// fields the HashFromJSON header path reads)
func jsonRPCBlock(number uint64, hash common.Hash, timestamp uint64) map[string]any {
	return map[string]any{
		"number":     hexutil.EncodeUint64(number),
		"hash":       hash.Hex(),
		"parentHash": common.HexToHash("0x0123").Hex(),
		"timestamp":  hexutil.EncodeUint64(timestamp),
	}
}

// jsonRPCReceipt is the eth_getTransactionReceipt result of receipt (as coherentBridgeReceipt
// builds it) the way a node serializes it, so the tests decode real JSON instead of handing
// FindBridge an already-decoded struct
func jsonRPCReceipt(receipt *gethtypes.Receipt) map[string]any {
	logs := make([]map[string]any, 0, len(receipt.Logs))
	for _, l := range receipt.Logs {
		logs = append(logs, map[string]any{
			"address":          l.Address.Hex(),
			"topics":           l.Topics,
			"data":             hexutil.Encode(l.Data),
			"blockNumber":      hexutil.EncodeUint64(l.BlockNumber),
			"transactionHash":  l.TxHash.Hex(),
			"transactionIndex": "0x0",
			"blockHash":        l.BlockHash.Hex(),
			"logIndex":         hexutil.EncodeUint64(uint64(l.Index)),
			"removed":          false,
		})
	}
	return map[string]any{
		"type":              "0x2",
		"status":            hexutil.EncodeUint64(receipt.Status),
		"cumulativeGasUsed": "0x5208",
		"gasUsed":           "0x5208",
		"effectiveGasPrice": "0x1",
		"logsBloom":         hexutil.Encode(make([]byte, gethtypes.BloomByteLength)),
		"logs":              logs,
		"transactionHash":   receipt.TxHash.Hex(),
		"transactionIndex":  "0x0",
		"contractAddress":   nil,
		"blockHash":         receipt.BlockHash.Hex(),
		"blockNumber":       hexutil.EncodeUint64(receipt.BlockNumber.Uint64()),
	}
}

// newJSONRPCClient serves a minimal JSON-RPC endpoint (eth_getTransactionReceipt answering
// receipt, eth_getBlockByNumber answering blocks by its block tag or hex number, null for an
// unknown one) and returns a real etherman client of it, with HashFromJSON as the proxy
// configures it
func newJSONRPCClient(
	t *testing.T, receipt map[string]any, blocks map[string]map[string]any,
) aggkittypes.BaseEthereumClienter {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var result any
		switch req.Method {
		case "eth_getTransactionReceipt":
			var hash string
			if err := json.Unmarshal(req.Params[0], &hash); err == nil && strings.EqualFold(hash, testTxHash.Hex()) {
				result = receipt
			}
		case "eth_getBlockByNumber":
			var tag string
			require.NoError(t, json.Unmarshal(req.Params[0], &tag))
			if block, ok := blocks[tag]; ok {
				result = block
			}
		default:
			http.Error(w, "unexpected method "+req.Method, http.StatusBadRequest)
			return
		}
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write(body)
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	cfg := ethermanconfig.NewDefaultRPCClientConfig()
	cfg.URL = server.URL
	cfg.HashFromJSON = true
	client, err := etherman.NewRPCClient(t.Context(), log.WithFields("module", "bridge_event_test"), *cfg)
	require.NoError(t, err)
	return client
}

// TestBridgeEventSourceDecodesJSONReceipt checks FindBridge end to end over a real RPC client
// decoding JSON receipts and headers. The block hashes of these tests are arbitrary (not the RLP
// hash of any header), as on a chain with non-standard headers: the check must use the hash the
// RPC reports, the way HashFromJSON does everywhere else, or an honest such chain would be
// rejected
func TestBridgeEventSourceDecodesJSONReceipt(t *testing.T) {
	finalized := jsonRPCBlock(bridgeReceiptBlockNumber+10, common.HexToHash("0xf1"), testBlockTimestamp+100)
	receiptBlockTag := hexutil.EncodeUint64(bridgeReceiptBlockNumber)

	testCases := []struct {
		name string
		// mutate alters the coherent receipt before it is serialized
		mutate func(receipt *gethtypes.Receipt)
		// block is the answer for the receipt's block
		block       map[string]any
		expectedErr error
	}{
		{
			name:  "coherent receipt, log and block with an RPC-reported hash",
			block: jsonRPCBlock(bridgeReceiptBlockNumber, testBlockHash, testBlockTimestamp),
		},
		{
			name: "log block hash differs from the receipt's",
			mutate: func(receipt *gethtypes.Receipt) {
				receipt.Logs[0].BlockHash = testOtherBlockHash
			},
			block:       jsonRPCBlock(bridgeReceiptBlockNumber, testOtherBlockHash, testBlockTimestamp),
			expectedErr: bridgetracker.ErrInvalidUpstreamData,
		},
		{
			name: "log block number differs from the receipt's",
			mutate: func(receipt *gethtypes.Receipt) {
				receipt.Logs[0].BlockNumber = bridgeReceiptBlockNumber + 1
			},
			block:       jsonRPCBlock(bridgeReceiptBlockNumber, testBlockHash, testBlockTimestamp),
			expectedErr: bridgetracker.ErrInvalidUpstreamData,
		},
		{
			name: "log tx hash is not the tracked tx",
			mutate: func(receipt *gethtypes.Receipt) {
				receipt.Logs[0].TxHash = common.HexToHash("0xdead")
			},
			block:       jsonRPCBlock(bridgeReceiptBlockNumber, testBlockHash, testBlockTimestamp),
			expectedErr: bridgetracker.ErrInvalidUpstreamData,
		},
		{
			name:        "the canonical block at the receipt's height has another hash",
			block:       jsonRPCBlock(bridgeReceiptBlockNumber, testOtherBlockHash, testBlockTimestamp),
			expectedErr: bridgetracker.ErrInvalidUpstreamData,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			receipt := coherentBridgeReceipt(t)
			if tc.mutate != nil {
				tc.mutate(receipt)
			}
			client := newJSONRPCClient(t, jsonRPCReceipt(receipt), map[string]map[string]any{
				"finalized":     finalized,
				receiptBlockTag: tc.block,
			})
			source, err := NewBridgeEventSource(
				StaticClients{0: client}, aggkittypes.FinalizedBlock, aggkittypes.FinalizedBlock, nil,
				staticBridgeAddressResolver{addrs: map[uint32]common.Address{0: testBridgeAddr}})
			require.NoError(t, err)

			info, err := source.FindBridge(t.Context(), bridgetracker.TrackingID{NetworkID: 0, TxHash: testTxHash})
			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr, fmt.Sprintf("info: %+v", info))
				require.Nil(t, info)
				return
			}
			require.NoError(t, err)
			require.Equal(t, l1ToL2Bridge(), info)
		})
	}
}
