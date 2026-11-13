package multidownloader

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"sync"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	mockethermantypes "github.com/agglayer/aggkit/etherman/types/mocks"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	mockmdrtypes "github.com/agglayer/aggkit/multidownloader/types/mocks"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocktypes "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	ethrpc "github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// fakeTransactionRPC is a deterministic RPCClienter for eth_getTransactionByHash.
type fakeTransactionRPC struct {
	mu        sync.Mutex
	calls     int
	byHash    map[common.Hash]json.RawMessage
	errByHash map[common.Hash]error
	gate      chan struct{}
}

func newFakeTransactionRPC() *fakeTransactionRPC {
	return &fakeTransactionRPC{
		byHash:    make(map[common.Hash]json.RawMessage),
		errByHash: make(map[common.Hash]error),
	}
}

func (f *fakeTransactionRPC) CallContext(ctx context.Context, result interface{},
	method string, args ...interface{}) error {
	f.mu.Lock()
	f.calls++
	gate := f.gate
	f.mu.Unlock()

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	hash, _ := args[0].(common.Hash)
	f.mu.Lock()
	err := f.errByHash[hash]
	raw, ok := f.byHash[hash]
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if !ok {
		raw = json.RawMessage("null")
	}
	rawResult, ok := result.(*json.RawMessage)
	if !ok {
		return fmt.Errorf("fakeTransactionRPC.CallContext: unexpected result type %T", result)
	}
	*rawResult = raw
	return nil
}

func (f *fakeTransactionRPC) Call(result any, method string, args ...any) error { return nil }

func (f *fakeTransactionRPC) BatchCallContext(ctx context.Context, b []ethrpc.BatchElem) error {
	return nil
}

func (f *fakeTransactionRPC) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeTransactionRPC) setTx(txHash common.Hash, raw json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byHash[txHash] = raw
}

func (f *fakeTransactionRPC) setErr(txHash common.Hash, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errByHash[txHash] = err
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

var (
	txFixtureFrom = common.HexToAddress("0x00000000000000000000000000000000000000aa")
	txFixtureTo   = common.HexToAddress("0x00000000000000000000000000000000000000bb")
	txFixtureAddr = common.HexToAddress("0x00000000000000000000000000000000000000cc")
	txFixtureT0   = common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000a1")
	txFixtureT1   = common.HexToHash("0x00000000000000000000000000000000000000000000000000000000000000b1")
)

func txFixtureLog(blockNumber uint64, blockHash, txHash common.Hash, logIndex uint,
	topics []common.Hash) ethtypes.Log {
	return ethtypes.Log{
		Address:        txFixtureAddr,
		BlockNumber:    blockNumber,
		BlockHash:      blockHash,
		BlockTimestamp: 1630000000,
		Topics:         topics,
		TxHash:         txHash,
		TxIndex:        0,
		Index:          logIndex,
	}
}

func txFixtureRPCJSON(blockNumber uint64, blockHash, txHash common.Hash) json.RawMessage {
	obj := map[string]any{
		"hash":             txHash.Hex(),
		"from":             txFixtureFrom.Hex(),
		"to":               txFixtureTo.Hex(),
		"blockHash":        blockHash.Hex(),
		"blockNumber":      fmt.Sprintf("0x%x", blockNumber),
		"transactionIndex": "0x0",
		"input":            "0x",
	}
	raw, _ := json.Marshal(obj)
	return raw
}

// txFixtureRPC builds a validated RPCTransaction for direct storage seeding.
func txFixtureRPC(blockNumber uint64, blockHash, txHash common.Hash) aggkittypes.RPCTransaction {
	blockHashHex := blockHash.Hex()
	blockNumberHex := fmt.Sprintf("0x%x", blockNumber)
	txIndexHex := "0x0"
	to := txFixtureTo.Hex()
	return aggkittypes.RPCTransaction{
		FromRaw:             txFixtureFrom.Hex(),
		To:                  &to,
		HashRaw:             txHash.Hex(),
		BlockHashRaw:        &blockHashHex,
		BlockNumberRaw:      &blockNumberHex,
		TransactionIndexRaw: &txIndexHex,
		InputRaw:            "0x",
	}
}

// txFixtureCoverState seeds the in-memory synced state so the provider considers
// the given block range/addresses as already covered. Real coverage (state AND a
// canonical stored log) is required before the provider will fetch a
// transaction, so a fixture that only writes storage would be rejected as an
// invented/uncovered log.
func txFixtureCoverState(td *testDataEVMMultidownloader, fromBlock, toBlock uint64,
	addrs ...common.Address) {
	if td.mdr.state == nil {
		td.mdr.state = NewEmptyState()
	}
	if len(addrs) == 0 {
		addrs = []common.Address{txFixtureAddr}
	}
	for _, addr := range addrs {
		td.mdr.state.Synced.Add(mdrtypes.SyncSegment{
			ContractAddr: addr,
			BlockRange:   aggkitcommon.NewBlockRange(fromBlock, toBlock),
		})
	}
}

// txFixtureStoreBlockAndLogs stores the canonical block header and logs AND marks
// the same block/addresses as covered in the in-memory synced state, so the
// provider sees a real covered canonical source (not a fabricated log).
func txFixtureStoreBlockAndLogs(t *testing.T, td *testDataEVMMultidownloader,
	blockNumber uint64, blockHash common.Hash, logs ...ethtypes.Log) {
	t.Helper()
	headers := aggkittypes.ListBlockHeaders{
		aggkittypes.NewBlockHeader(blockNumber, blockHash, 1630000000, nil),
	}
	require.NoError(t, td.realStorage.SaveEthLogsWithHeaders(nil, headers, logs, true))
	addrs := make([]common.Address, 0, len(logs)+1)
	addrs = append(addrs, txFixtureAddr)
	for i := range logs {
		addrs = append(addrs, logs[i].Address)
	}
	txFixtureCoverState(td, blockNumber, blockNumber, addrs...)
}

// newTxProviderTestData returns a test data with transaction support enabled and
// a controllable RPC client. No background worker is started. The storage is
// backed by a real file so concurrent access never lands on a different
// per-connection in-memory database.
func newTxProviderTestData(t *testing.T) (*testDataEVMMultidownloader, *fakeTransactionRPC) {
	t.Helper()
	logger := log.WithFields("test", "tx_provider")
	cfg := Config{
		BlockChunkSize:                  5000,
		MaxParallelBlockHeaderRetrieval: 50,
		BlockFinality:                   aggkittypes.FinalizedBlock,
		WaitPeriodToCheckCatchUp:        types.NewDuration(time.Second),
		StoragePath:                     filepath.Join(t.TempDir(), "tx.sqlite"),
	}
	realDB, err := storage.NewMultidownloaderStorage(logger,
		storage.MultidownloaderStorageConfig{DBPath: cfg.StoragePath})
	require.NoError(t, err)
	ethClient := mocktypes.NewBaseEthereumClienter(t)
	mockBlockNotifierManager := mockethermantypes.NewBlockNotifierManager(t)
	mockReorgProcessor := mockmdrtypes.NewReorgProcessor(t)
	fake := newFakeTransactionRPC()
	mdr, err := NewEVMMultidownloader(logger, cfg, "test", ethClient, fake,
		realDB, mockBlockNotifierManager, mockReorgProcessor)
	require.NoError(t, err)
	ts, err := mdrtypes.AsTransactionStorage(realDB)
	require.NoError(t, err)
	mdr.transactionStorage = ts
	mdr.requiresTransactions = true
	return &testDataEVMMultidownloader{
		mockEthClient:            ethClient,
		mdr:                      mdr,
		realStorage:              realDB,
		usedStorage:              realDB,
		mockBlockNotifierManager: mockBlockNotifierManager,
		mockReorgProcessor:       mockReorgProcessor,
	}, fake
}

// ---------------------------------------------------------------------------
// Provider: TransactionByLog
// ---------------------------------------------------------------------------

func TestTransactionByLog_Disabled(t *testing.T) {
	td := newEVMMultidownloaderTestData(t, false)
	fake := newFakeTransactionRPC()
	td.mdr.rpcClient = fake

	_, err := td.mdr.TransactionByLog(t.Context(), txFixtureLog(10, common.HexToHash("0x10"), common.HexToHash("0xaa"), 0, nil))
	require.ErrorIs(t, err, errTransactionProviderDisabled)
	require.Zero(t, fake.callCount())
}

func TestTransactionByLog_CacheHit(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	require.NoError(t, td.mdr.transactionStorage.SaveTransactions(nil,
		[]aggkittypes.RPCTransaction{txFixtureRPC(10, blockHash, txHash)}))

	got, err := td.mdr.TransactionByLog(t.Context(), lg)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, txHash, got.Hash())
	require.Zero(t, fake.callCount(), "cache hit must not perform any RPC")
}

func TestTransactionByLog_FetchAndSave(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	fake.setTx(txHash, txFixtureRPCJSON(10, blockHash, txHash))

	got, err := td.mdr.TransactionByLog(t.Context(), lg)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 1, fake.callCount())

	stored, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.NotNil(t, stored, "resolved transaction must be persisted")
}

func TestTransactionByLog_ConcurrentSingleRPC(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	fake.setTx(txHash, txFixtureRPCJSON(10, blockHash, txHash))
	fake.mu.Lock()
	fake.gate = make(chan struct{})
	fake.mu.Unlock()

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = td.mdr.TransactionByLog(t.Context(), lg)
		}(i)
	}
	// Give the goroutines time to all join the singleflight call.
	time.Sleep(50 * time.Millisecond)
	close(fake.gate)
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 1, fake.callCount(), "duplicate concurrent requests must share one RPC")
}

func TestTransactionByLog_NullPendingMalformed(t *testing.T) {
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")

	t.Run("null response", func(t *testing.T) {
		td, fake := newTxProviderTestData(t)
		lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
		txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
		fake.setTx(txHash, json.RawMessage("null"))

		got, err := td.mdr.TransactionByLog(t.Context(), lg)
		require.Error(t, err)
		require.NotErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
		require.Nil(t, got)
	})

	t.Run("pending transaction", func(t *testing.T) {
		td, fake := newTxProviderTestData(t)
		lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
		txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
		pending := json.RawMessage(`{"hash":"` + txHash.Hex() + `","from":"` + txFixtureFrom.Hex() +
			`","blockHash":null,"blockNumber":null,"transactionIndex":null,"input":"0x"}`)
		fake.setTx(txHash, pending)

		got, err := td.mdr.TransactionByLog(t.Context(), lg)
		require.Error(t, err)
		require.NotErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
		require.Nil(t, got)
	})

	t.Run("malformed response", func(t *testing.T) {
		td, fake := newTxProviderTestData(t)
		lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
		txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
		fake.setTx(txHash, json.RawMessage(`{"hash":`))

		got, err := td.mdr.TransactionByLog(t.Context(), lg)
		require.Error(t, err)
		require.NotErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
		require.Nil(t, got)
	})

	t.Run("rpc transport error is retryable", func(t *testing.T) {
		td, fake := newTxProviderTestData(t)
		lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
		txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
		fake.setErr(txHash, fmt.Errorf("connection reset"))

		_, err := td.mdr.TransactionByLog(t.Context(), lg)
		require.Error(t, err)
		require.NotErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
	})
}

func TestTransactionByLog_InclusionMismatch(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	otherHash := common.HexToHash("0x99")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	// RPC reports the transaction mined in a different block.
	fake.setTx(txHash, txFixtureRPCJSON(11, otherHash, txHash))

	got, err := td.mdr.TransactionByLog(t.Context(), lg)
	require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
	require.Nil(t, got)
	// No row must be written for a mismatching inclusion.
	stored, err2 := td.mdr.transactionStorage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err2)
	require.Nil(t, stored)
}

func TestTransactionByLog_StaleCoverageMismatch(t *testing.T) {
	td, _ := newTxProviderTestData(t)
	storedHash := common.HexToHash("0x10")
	staleHash := common.HexToHash("0x11")
	txHash := common.HexToHash("0xaa")
	// Storage has a canonical block with storedHash, the log claims staleHash.
	lg := txFixtureLog(10, staleHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, storedHash)

	_, err := td.mdr.TransactionByLog(t.Context(), lg)
	require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
}

func TestTransactionByLog_UncoveredLogIsNotSuccess(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	// The log's block is not stored at all.
	lg := txFixtureLog(12345, common.HexToHash("0xdead"), common.HexToHash("0xaa"), 0, nil)

	got, err := td.mdr.TransactionByLog(t.Context(), lg)
	require.Error(t, err)
	require.NotErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
	require.ErrorIs(t, err, aggkittypes.ErrTransactionUnavailable)
	require.Nil(t, got, "an uncovered log must never be reported as a nil success")
	require.Zero(t, fake.callCount())
}

func TestTransactionByLog_CanceledContext(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	fake.setTx(txHash, txFixtureRPCJSON(10, blockHash, txHash))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := td.mdr.TransactionByLog(ctx, lg)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, fake.callCount(), "a canceled request must not issue an RPC")
}

func TestTransactionByLog_ReplayBelowFrontierAllowed(t *testing.T) {
	// A foreground request below the registered transaction frontier must still
	// be resolved (historic backfill), not silently rejected.
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	fake.setTx(txHash, txFixtureRPCJSON(10, blockHash, txHash))

	got, err := td.mdr.TransactionByLog(t.Context(), lg)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 1, fake.callCount())
}

// ---------------------------------------------------------------------------
// Demand derivation
// ---------------------------------------------------------------------------

func TestBuildTransactionDemands_NonLossy(t *testing.T) {
	td := newEVMMultidownloaderTestData(t, false)
	addr := txFixtureAddr
	require.NoError(t, td.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{addr},
		FromBlock:         100,
		ToBlock:           aggkittypes.FinalizedBlock,
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 50,
			Topic0:    []common.Hash{txFixtureT0},
		},
	}))
	require.NoError(t, td.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID:          "s2",
		ContractAddresses: []common.Address{addr},
		FromBlock:         200,
		ToBlock:           aggkittypes.LatestBlock,
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 10,
			Topic0:    []common.Hash{txFixtureT1},
		},
	}))

	demands := td.mdr.buildTransactionDemands()
	require.Len(t, demands, 2, "distinct registrations must not be merged")
	bySyncer := map[string]transactionDemand{}
	for _, d := range demands {
		bySyncer[d.SyncerID] = d
	}
	require.Equal(t, uint64(100), bySyncer["s1"].FromBlock, "max(FromBlock, tr.FromBlock)")
	require.Equal(t, uint64(200), bySyncer["s2"].FromBlock)
	require.Equal(t, aggkittypes.FinalizedBlock, bySyncer["s1"].ToBlock)
	require.Equal(t, aggkittypes.LatestBlock, bySyncer["s2"].ToBlock)
	require.Equal(t, []common.Hash{txFixtureT0}, bySyncer["s1"].Topic0)
	require.Equal(t, []common.Hash{txFixtureT1}, bySyncer["s2"].Topic0)
}

func TestBuildTransactionDemands_DisabledProducesNothing(t *testing.T) {
	td := newEVMMultidownloaderTestData(t, false)
	require.NoError(t, td.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txFixtureAddr},
		FromBlock:         1,
		ToBlock:           aggkittypes.LatestBlock,
	}))
	require.Empty(t, td.mdr.buildTransactionDemands())
}

// ---------------------------------------------------------------------------
// Initialize capability gate
// ---------------------------------------------------------------------------

func TestInitialize_TransactionSupportRejectedWithoutCapability(t *testing.T) {
	td := newEVMMultidownloaderTestData(t, true) // mock storage does not implement TransactionStorage
	td.MockInitialize(t, 1)
	require.NoError(t, td.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txFixtureAddr},
		FromBlock:         1,
		ToBlock:           aggkittypes.LatestBlock,
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 1,
		},
	}))

	err := td.mdr.Initialize(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "TransactionStorage")
	require.False(t, td.mdr.requiresTransactions)
}

func TestInitialize_TransactionSupportDisabledWithMockStorage(t *testing.T) {
	// Disabled-only registrations must keep working with the plain Storager mock.
	td := newEVMMultidownloaderTestData(t, true)
	td.MockInitialize(t, 1)
	require.NoError(t, td.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txFixtureAddr},
		FromBlock:         1,
		ToBlock:           aggkittypes.LatestBlock,
	}))

	require.NoError(t, td.mdr.Initialize(t.Context()))
	require.False(t, td.mdr.requiresTransactions)
	require.Nil(t, td.mdr.transactionStorage)
}

func TestInitialize_TransactionSupportEnabledWithRealStorage(t *testing.T) {
	td := newEVMMultidownloaderTestData(t, false)
	td.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil).Maybe()
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
		Return(uint64(200), nil).Maybe()
	require.NoError(t, td.mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID:          "s1",
		ContractAddresses: []common.Address{txFixtureAddr},
		FromBlock:         1,
		ToBlock:           aggkittypes.LatestBlock,
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: 1,
		},
	}))

	require.NoError(t, td.mdr.Initialize(t.Context()))
	require.True(t, td.mdr.requiresTransactions)
	require.NotNil(t, td.mdr.transactionStorage)
	require.Len(t, td.mdr.transactionDemands, 1)
}

// ---------------------------------------------------------------------------
// Background worker
// ---------------------------------------------------------------------------

func TestScanMissingTransactions_BackfillsCoveredLogs(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	fake.setTx(txHash, txFixtureRPCJSON(10, blockHash, txHash))

	// The syncer requires transactions for this address and the range is covered.
	td.mdr.transactionDemands = []transactionDemand{{
		SyncerID:  "s1",
		Address:   txFixtureAddr,
		FromBlock: 1,
		ToBlock:   aggkittypes.LatestBlock,
		Topic0:    []common.Hash{txFixtureT0},
	}}
	state := NewEmptyState()
	state.Synced.Add(mdrtypes.SyncSegment{
		ContractAddr: txFixtureAddr,
		BlockRange:   aggkitcommon.NewBlockRange(1, 20),
	})
	td.mdr.state = state
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(20), nil).Maybe()

	td.mdr.scanMissingTransactions(t.Context())

	stored, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, 1, fake.callCount())
}

func TestScanMissingTransactions_UncoveredRangeIsNotComplete(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	fake.setTx(txHash, txFixtureRPCJSON(10, blockHash, txHash))

	td.mdr.transactionDemands = []transactionDemand{{
		SyncerID:  "s1",
		Address:   txFixtureAddr,
		FromBlock: 1,
		ToBlock:   aggkittypes.LatestBlock,
		Topic0:    []common.Hash{txFixtureT0},
	}}
	// Only block 15+ is covered; the log at block 10 must NOT be scanned.
	state := NewEmptyState()
	state.Synced.Add(mdrtypes.SyncSegment{
		ContractAddr: txFixtureAddr,
		BlockRange:   aggkitcommon.NewBlockRange(15, 20),
	})
	td.mdr.state = state
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(20), nil).Maybe()

	td.mdr.scanMissingTransactions(t.Context())

	require.Zero(t, fake.callCount(), "uncovered logs must not be treated as missing transactions")
}

func TestScanMissingTransactions_TopicFilterWidening(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHashA := common.HexToHash("0xaa")
	txHashB := common.HexToHash("0xbb")
	lgA := txFixtureLog(10, blockHash, txHashA, 0, []common.Hash{txFixtureT0})
	lgB := txFixtureLog(10, blockHash, txHashB, 1, []common.Hash{txFixtureT1})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lgA, lgB)
	fake.setTx(txHashA, txFixtureRPCJSON(10, blockHash, txHashA))
	fake.setTx(txHashB, txFixtureRPCJSON(10, blockHash, txHashB))

	// A topic0 filter for T0 must only backfill the T0 log.
	td.mdr.transactionDemands = []transactionDemand{{
		SyncerID:  "s1",
		Address:   txFixtureAddr,
		FromBlock: 1,
		ToBlock:   aggkittypes.LatestBlock,
		Topic0:    []common.Hash{txFixtureT0},
	}}
	state := NewEmptyState()
	state.Synced.Add(mdrtypes.SyncSegment{
		ContractAddr: txFixtureAddr,
		BlockRange:   aggkitcommon.NewBlockRange(1, 20),
	})
	td.mdr.state = state
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(20), nil).Maybe()

	td.mdr.scanMissingTransactions(t.Context())

	storedA, err := td.mdr.transactionStorage.GetTransaction(nil, txHashA, blockHash)
	require.NoError(t, err)
	require.NotNil(t, storedA)
	storedB, err := td.mdr.transactionStorage.GetTransaction(nil, txHashB, blockHash)
	require.NoError(t, err)
	require.Nil(t, storedB, "logs outside the topic0 filter must not be backfilled")
	require.Equal(t, 1, fake.callCount())
}

func TestTransactionWorker_StopsOnCancel(t *testing.T) {
	td, _ := newTxProviderTestData(t)
	td.mdr.transactionDemands = nil
	ctx, cancel := context.WithCancel(context.Background())
	td.mdr.startTransactionWorker(ctx)

	done := make(chan struct{})
	go func() {
		td.mdr.workerWG.Wait()
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("transaction worker did not stop after context cancellation")
	}
}

func TestStartStop_NoTransactionsZeroRPC(t *testing.T) {
	// Without a transaction-enabled syncer, Start must not perform any
	// transaction RPC nor start a worker.
	td := newEVMMultidownloaderTestData(t, true)
	td.FakeInitialized(t)
	fake := newFakeTransactionRPC()
	td.mdr.rpcClient = fake

	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
		Return(uint64(100), nil).Maybe()
	td.mockStorage.EXPECT().NewTx(mock.Anything).Return(nil, fmt.Errorf("stop test")).Maybe()
	td.mockStorage.EXPECT().GetBlockHeadersNotFinalized(mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	ctx := context.Background()
	var startErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		startErr = td.mdr.Start(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, td.mdr.Stop(context.Background()))
	wg.Wait()
	require.ErrorIs(t, startErr, context.Canceled)
	require.Zero(t, fake.callCount())
	require.False(t, td.mdr.requiresTransactions)
}

// ---------------------------------------------------------------------------
// FilterLogs coherent read
// ---------------------------------------------------------------------------

func TestFilterLogs_CoherentReadReturnsCoveredLogs(t *testing.T) {
	td := newEVMMultidownloaderTestData(t, false)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)

	state := NewEmptyState()
	state.Synced.Add(mdrtypes.SyncSegment{
		ContractAddr: txFixtureAddr,
		BlockRange:   aggkitcommon.NewBlockRange(1, 20),
	})
	td.mdr.state = state

	logs, err := td.mdr.FilterLogs(t.Context(), ethereum.FilterQuery{
		Addresses: []common.Address{txFixtureAddr},
		FromBlock: big.NewInt(1),
		ToBlock:   big.NewInt(20),
	})
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, txHash, logs[0].TxHash)
}
