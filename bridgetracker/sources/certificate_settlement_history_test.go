package sources

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeSettlement is one canned row a fakeSettlementsServer answers GET /bridge/v1/settlements
// with, paired with the deposit-count index its LER resolves to on /bridge/v1/root-by-ler (what
// Covers reads)
type fakeSettlement struct {
	ler       common.Hash
	rootIndex uint32
	txHash    *common.Hash
}

// fakeSettlementsServer serves both GET /bridge/v1/root-by-ler (used by Covers) and GET
// /bridge/v1/settlements (paginated, most-recent first: settlements[0] is the newest) on network
// l2ToL1Bridge().NetworkID, from a fixed in-order list. notFound makes /settlements always
// answer HTTP 404, simulating a bridge-service instance that predates the endpoint
type fakeSettlementsServer struct {
	settlements []fakeSettlement
	notFound    bool
}

func (f fakeSettlementsServer) start(t *testing.T) NetworkURLResolver {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/bridge/v1/root-by-ler", func(w http.ResponseWriter, r *http.Request) {
		lerHex := r.URL.Query().Get("ler")
		for _, s := range f.settlements {
			if s.ler.Hex() == lerHex {
				fmt.Fprintf(w, `{"index":%d,"block_num":0,"block_position":0}`, s.rootIndex)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"not found (not synced yet)"}`)
	})
	mux.HandleFunc("/bridge/v1/settlements", func(w http.ResponseWriter, r *http.Request) {
		if f.notFound {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		pageNumber, _ := strconv.Atoi(r.URL.Query().Get("page_number"))
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if pageNumber < 1 {
			pageNumber = 1
		}
		start := (pageNumber - 1) * pageSize
		result := bridgeservicetypes.SettlementsResult{Count: len(f.settlements)}
		for i := start; i < len(f.settlements) && i < start+pageSize; i++ {
			s := f.settlements[i]
			resp := &bridgeservicetypes.SettlementResponse{
				NewLocalExitRoot: bridgeservicetypes.Hash(s.ler.Hex()),
				BlockNumber:      uint64(1000 + i),
			}
			if s.txHash != nil {
				h := bridgeservicetypes.Hash(s.txHash.Hex())
				resp.TxHash = &h
			}
			result.Settlements = append(result.Settlements, resp)
		}
		body, err := json.Marshal(result)
		require.NoError(t, err)
		_, err = w.Write(body)
		require.NoError(t, err)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return staticURLs{l2ToL1Bridge().NetworkID: bridgeservicefinder.NetworkURLs{BridgeURL: server.URL}}
}

// TestEarliestSettlementTxCoveringViaBridgeService proves the primary path: it walks
// bridge-service's most-recent-first settlement list and returns the tx hash of the last (most
// recent) entry that still covers the bridge -- the earliest one that does, right after the
// transition from non-covering to covering
func TestEarliestSettlementTxCoveringViaBridgeService(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	txC, txB, txA := common.HexToHash("0xc"), common.HexToHash("0xb"), common.HexToHash("0xa")
	server := fakeSettlementsServer{settlements: []fakeSettlement{
		{ler: common.HexToHash("0x111"), rootIndex: 9, txHash: &txC}, // covers (9 >= 7)
		{ler: common.HexToHash("0x222"), rootIndex: 7, txHash: &txB}, // covers (7 >= 7): the exact one
		{ler: common.HexToHash("0x333"), rootIndex: 3, txHash: &txA}, // does not cover (3 < 7)
	}}
	source := NewCertificateSource(nil, server.start(t), nil, testRollupManagerAddress, testLogger)

	got, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 0)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, txB, *got)
}

// TestEarliestSettlementTxCoveringViaBridgeServicePaginates proves the walk continues across
// pages until it finds the transition, not just within the first page
func TestEarliestSettlementTxCoveringViaBridgeServicePaginates(t *testing.T) {
	orig := settlementsPageSize
	settlementsPageSize = 1
	t.Cleanup(func() { settlementsPageSize = orig })

	bridge := l2ToL1Bridge() // DepositCount: 7
	txB, txA := common.HexToHash("0xb"), common.HexToHash("0xa")
	server := fakeSettlementsServer{settlements: []fakeSettlement{
		{ler: common.HexToHash("0x222"), rootIndex: 7, txHash: &txB}, // page 1: covers
		{ler: common.HexToHash("0x333"), rootIndex: 3, txHash: &txA}, // page 2: does not cover
	}}
	source := NewCertificateSource(nil, server.start(t), nil, testRollupManagerAddress, testLogger)

	got, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 0)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, txB, *got)
}

// TestEarliestSettlementTxCoveringViaBridgeServiceOldestCoversEverything proves that when every
// settlement fetched covers the bridge (the network's very first certificate already did), the
// oldest one seen is returned as the earliest possible answer, instead of erroring or hanging
func TestEarliestSettlementTxCoveringViaBridgeServiceOldestCoversEverything(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	txB, txA := common.HexToHash("0xb"), common.HexToHash("0xa")
	server := fakeSettlementsServer{settlements: []fakeSettlement{
		{ler: common.HexToHash("0x222"), rootIndex: 9, txHash: &txB},
		{ler: common.HexToHash("0x333"), rootIndex: 7, txHash: &txA}, // oldest, still covers (7 >= 7)
	}}
	source := NewCertificateSource(nil, server.start(t), nil, testRollupManagerAddress, testLogger)

	got, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 0)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, txA, *got)
}

// TestEarliestSettlementTxCoveringLegacyRowFallsBackToLogs proves that when the exact covering
// entry bridge-service reports has no recorded tx hash (a settlement synced before #1817's
// tx_hash column existed), the search falls back to reading the same event off L1 instead of
// returning a nil/wrong answer
func TestEarliestSettlementTxCoveringLegacyRowFallsBackToLogs(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	txA := common.HexToHash("0xa")
	server := fakeSettlementsServer{settlements: []fakeSettlement{
		{ler: common.HexToHash("0x222"), rootIndex: 7, txHash: nil},  // covers, but legacy (no tx hash)
		{ler: common.HexToHash("0x333"), rootIndex: 3, txHash: &txA}, // does not cover
	}}
	ethClient := mocks.NewBaseEthereumClienter(t)
	logTxHash := common.HexToHash("0xfallback")
	expectVerifyBatchesLog(ethClient, bridge, 100, gethtypes.Log{
		Topics: []common.Hash{
			verifyBatchesTrustedAggregatorSignature, rollupIDTopicFor(bridge.NetworkID),
		},
		Data:        verifyBatchesLogData(common.HexToHash("0x222")), // same LER as the legacy row: covers
		TxHash:      logTxHash,
		BlockNumber: 100,
	})

	source := NewCertificateSource(
		nil, server.start(t), StaticClients{0: ethClient}, testRollupManagerAddress, testLogger)

	got, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 100)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, logTxHash, *got)
}

// TestEarliestSettlementTxCoveringEndpointNotFoundFallsBackToLogs proves that a bridge-service
// instance that predates GET /bridge/v1/settlements (HTTP 404) is treated the same way: fall
// back to L1 logs instead of erroring
func TestEarliestSettlementTxCoveringEndpointNotFoundFallsBackToLogs(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	// notFound only affects GET /settlements; /root-by-ler (what the fallback's Covers check
	// asks) is still served normally, off the same settlements list
	server := fakeSettlementsServer{
		notFound:    true,
		settlements: []fakeSettlement{{ler: common.HexToHash("0x222"), rootIndex: 7}},
	}
	ethClient := mocks.NewBaseEthereumClienter(t)
	logTxHash := common.HexToHash("0xfallback")
	expectVerifyBatchesLog(ethClient, bridge, 100, gethtypes.Log{
		Topics: []common.Hash{
			verifyBatchesTrustedAggregatorSignature, rollupIDTopicFor(bridge.NetworkID),
		},
		Data:        verifyBatchesLogData(common.HexToHash("0x222")),
		TxHash:      logTxHash,
		BlockNumber: 100,
	})

	source := NewCertificateSource(
		nil, server.start(t), StaticClients{0: ethClient}, testRollupManagerAddress, testLogger)

	got, err := source.EarliestSettlementTxCovering(t.Context(), bridge, 100)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, logTxHash, *got)
}

// rollupIDTopicFor mirrors earliestSettlementTxCoveringViaLogs' own topic encoding, for tests
// that need to stub FilterLogs with a matching FilterQuery
func rollupIDTopicFor(networkID uint32) common.Hash {
	return common.BigToHash(new(big.Int).SetUint64(uint64(networkID)))
}

// verifyBatchesLogData builds a VerifyBatchesTrustedAggregator log's non-indexed Data (numBatch,
// stateRoot, exitRoot, each word-padded to 32 bytes) carrying exitRoot as its last word
func verifyBatchesLogData(exitRoot common.Hash) []byte {
	data := make([]byte, verifyBatchesTrustedAggregatorDataLen)
	copy(data[64:], exitRoot[:])
	return data
}

// expectVerifyBatchesLog stubs ethClient's FilterLogs for earliestSettlementTxCoveringViaLogs'
// very first backwards chunk (fromBlock down to fromBlock minus l1InfoTreeBackwardsSearchChunkSize
// plus one, or 0), filtered to bridge's rollupID, with a single matching log
func expectVerifyBatchesLog(
	ethClient *mocks.BaseEthereumClienter, bridge *bridgetracker.BridgeInfo, fromBlock uint64, log gethtypes.Log,
) {
	fromChunk := uint64(0)
	if fromBlock >= l1InfoTreeBackwardsSearchChunkSize {
		fromChunk = fromBlock - l1InfoTreeBackwardsSearchChunkSize + 1
	}
	ethClient.EXPECT().FilterLogs(mock.Anything, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromChunk),
		ToBlock:   new(big.Int).SetUint64(fromBlock),
		Addresses: []common.Address{testRollupManagerAddress},
		Topics:    [][]common.Hash{{verifyBatchesTrustedAggregatorSignature}, {rollupIDTopicFor(bridge.NetworkID)}},
	}).Return([]gethtypes.Log{log}, nil)
}
