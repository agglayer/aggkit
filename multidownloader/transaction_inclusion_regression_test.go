package multidownloader

// This file holds the transaction-inclusion regression tests that need to pin
// the concurrency and reorg semantics of the optional transaction path:
//
//   - the whole-flight dedup window (a waiter arriving after the RPC response
//     but before the save completes must share the same request), and
//   - an actual mid-flight reorg (a stale HTTP result must never be cached and
//     must never poison the re-resolution of the same tx hash in a new block),
//     and
//   - fabricated / archived source logs (a log that was never downloaded, or
//     one whose block was reorged away, must be rejected without any RPC).
//
// All synchronization is channel based (no sleeps): the join-detecting context
// and the blocking storage wrapper make every ordering deterministic.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	dbtypes "github.com/agglayer/aggkit/db/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	ethrpc "github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
)

// txRegressionOtherAddr is a covered-adjacent address used to build a log whose
// address was never downloaded/covered.
var txRegressionOtherAddr = common.HexToAddress("0x00000000000000000000000000000000000000dd")

// ---------------------------------------------------------------------------
// Test doubles / instrumentation
// ---------------------------------------------------------------------------

// joinDetectingContext wraps a context and closes joined the first time Done()
// is called. The transaction flight evaluates ctx.Done() exactly when a caller
// enters transactionCall.wait, i.e. after the shared-flight map lookup has
// already found an existing call. Closing the channel therefore proves the
// caller joined an existing flight instead of starting its own, with no timing
// dependence.
type joinDetectingContext struct {
	context.Context
	once   sync.Once
	joined chan struct{}
}

func newJoinDetectingContext(parent context.Context) *joinDetectingContext {
	return &joinDetectingContext{Context: parent, joined: make(chan struct{})}
}

func (c *joinDetectingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

// hasCall reports whether a shared transaction flight is currently registered
// for key. It lets a test observe the whole-flight window: the call must stay
// registered until the save completes (it is only deleted afterwards).
func (g *transactionInflight) hasCall(key txFlightKey) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.calls[key]
	return ok
}

// blockingSaveTransactionStorage wraps a TransactionStorage and holds
// SaveTransactions open until the test releases it, so the second half of the
// shared flight can be observed deterministically.
type blockingSaveTransactionStorage struct {
	mdrtypes.TransactionStorage
	saveEntered chan struct{}
	releaseSave chan struct{}
}

func (b *blockingSaveTransactionStorage) SaveTransactions(tx dbtypes.Querier,
	txs []aggkittypes.RPCTransaction) error {
	select {
	case b.saveEntered <- struct{}{}:
	default:
	}
	<-b.releaseSave
	return b.TransactionStorage.SaveTransactions(tx, txs)
}

// gatedTxRPC is a deterministic RPCClienter for eth_getTransactionByHash. It
// signals every entry on entered and, when a gate is installed, blocks the call
// until the gate is closed (or the context is canceled).
type gatedTxRPC struct {
	mu      sync.Mutex
	calls   int
	byHash  map[common.Hash]json.RawMessage
	entered chan common.Hash
	gate    chan struct{}
}

func newGatedTxRPC() *gatedTxRPC {
	return &gatedTxRPC{
		byHash:  make(map[common.Hash]json.RawMessage),
		entered: make(chan common.Hash, 16),
	}
}

func (g *gatedTxRPC) CallContext(ctx context.Context, result interface{},
	method string, args ...interface{}) error {
	hash, _ := args[0].(common.Hash)
	g.mu.Lock()
	g.calls++
	gate := g.gate
	g.mu.Unlock()

	select {
	case g.entered <- hash:
	default:
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	g.mu.Lock()
	raw, ok := g.byHash[hash]
	g.mu.Unlock()
	if !ok {
		raw = json.RawMessage("null")
	}
	rawResult, ok := result.(*json.RawMessage)
	if !ok {
		return fmt.Errorf("gatedTxRPC.CallContext: unexpected result type %T", result)
	}
	*rawResult = raw
	return nil
}

func (g *gatedTxRPC) Call(result any, method string, args ...any) error { return nil }

func (g *gatedTxRPC) BatchCallContext(ctx context.Context, b []ethrpc.BatchElem) error {
	return nil
}

func (g *gatedTxRPC) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func (g *gatedTxRPC) setTx(txHash common.Hash, raw json.RawMessage) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.byHash[txHash] = raw
}

func (g *gatedTxRPC) setGate(gate chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gate = gate
}

// ---------------------------------------------------------------------------
// Reorg / state helpers
// ---------------------------------------------------------------------------

// txRegressionCommitReorg commits an actual reorg through the real storage,
// moving the given single block (and its logs/transactions) to the reorged
// tables and shrinking the persisted synced coverage.
func txRegressionCommitReorg(t *testing.T, td *testDataEVMMultidownloader, blockNumber uint64) {
	t.Helper()
	tx, err := td.realStorage.NewTx(t.Context())
	require.NoError(t, err)
	reorgData := mdrtypes.ReorgData{
		BlockRangeAffected:        aggkitcommon.NewBlockRange(blockNumber, blockNumber),
		DetectedAtBlock:           blockNumber + 100,
		DetectedTimestamp:         1630003000,
		NetworkLatestBlock:        blockNumber + 200,
		NetworkFinalizedBlock:     1,
		NetworkFinalizedBlockName: aggkittypes.FinalizedBlock,
		Description:               "transaction inclusion regression test",
	}
	_, err = td.realStorage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, reorgData)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

// txRegressionShrinkState keeps the in-memory synced state coherent with a
// committed reorg by subtracting the reorged block range from the synced
// coverage of the given address.
func txRegressionShrinkState(t *testing.T, td *testDataEVMMultidownloader,
	addr common.Address, fromBlock, toBlock uint64) {
	t.Helper()
	require.NotNil(t, td.mdr.state, "state must be initialized")
	set := mdrtypes.NewSetSyncSegment()
	set.Add(mdrtypes.SyncSegment{
		ContractAddr: addr,
		BlockRange:   aggkitcommon.NewBlockRange(fromBlock, toBlock),
	})
	require.NoError(t, td.mdr.state.Synced.SubtractSegments(&set))
}

func txRegressionRequireHeaderAbsent(t *testing.T, td *testDataEVMMultidownloader, blockNumber uint64) {
	t.Helper()
	header, _, err := td.realStorage.GetBlockHeaderByNumber(nil, blockNumber)
	require.NoError(t, err)
	require.Nil(t, header, "the reorged block must be gone from the canonical blocks table")
}

// ---------------------------------------------------------------------------
// 1. Whole-flight dedup race window
// ---------------------------------------------------------------------------

// TestTransactionByLog_WholeFlightDedupRaceWindow pins that a caller arriving
// after the RPC response but before the save completes joins the SAME flight:
// the shared call stays registered through the save and the second caller must
// not issue a second RPC.
func TestTransactionByLog_WholeFlightDedupRaceWindow(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	fake.setTx(txHash, txFixtureRPCJSON(10, blockHash, txHash))

	blocking := &blockingSaveTransactionStorage{
		TransactionStorage: td.mdr.transactionStorage,
		saveEntered:        make(chan struct{}, 1),
		releaseSave:        make(chan struct{}),
	}
	td.mdr.transactionStorage = blocking

	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(blocking.releaseSave) }) }
	defer release()

	key := txFlightKey{txHash: txHash, blockHash: blockHash}
	type result struct {
		tx  *aggkittypes.RPCTransaction
		err error
	}
	providerA := make(chan result, 1)
	go func() {
		tx, err := td.mdr.TransactionByLog(t.Context(), lg)
		providerA <- result{tx, err}
	}()

	// A must have completed its RPC and be blocked inside SaveTransactions.
	select {
	case <-blocking.saveEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider A never reached SaveTransactions")
	}
	require.Equal(t, 1, fake.callCount(), "A's RPC must have completed exactly once")
	require.True(t, td.mdr.txInflight.hasCall(key),
		"the shared flight must stay registered until the save completes")

	// B arrives inside the save window. Its join-detecting context only fires
	// once it has entered transactionCall.wait, i.e. after joining A's flight.
	ctxB := newJoinDetectingContext(t.Context())
	providerB := make(chan result, 1)
	go func() {
		tx, err := td.mdr.TransactionByLog(ctxB, lg)
		providerB <- result{tx, err}
	}()

	select {
	case <-ctxB.joined:
	case <-time.After(5 * time.Second):
		t.Fatal("provider B never joined the shared flight")
	}
	require.Equal(t, 1, fake.callCount(),
		"a waiter inside the save window must not issue a second RPC")
	require.True(t, td.mdr.txInflight.hasCall(key))

	// Release the save: both waiters must observe the same valid inclusion.
	release()
	resA := <-providerA
	resB := <-providerB
	require.NoError(t, resA.err)
	require.NoError(t, resB.err)
	require.NotNil(t, resA.tx)
	require.NotNil(t, resB.tx)
	require.Same(t, resA.tx, resB.tx, "both waiters must share the same resolved transaction")
	require.Equal(t, txHash, resA.tx.Hash())
	require.Equal(t, txHash, resB.tx.Hash())
	require.Equal(t, 1, fake.callCount(), "the whole flight must perform exactly one RPC")

	stored, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, blockHash)
	require.NoError(t, err)
	require.NotNil(t, stored, "the shared flight must persist the resolved transaction")
}

// ---------------------------------------------------------------------------
// 2. Actual mid-flight reorg
// ---------------------------------------------------------------------------

// TestTransactionByLog_MidFlightReorgRegression pins that a transaction resolved
// while its source block is reorged away is never cached, and that a later
// re-inclusion of the same tx hash in a new block is resolved correctly without
// being poisoned by the stale flight.
func TestTransactionByLog_MidFlightReorgRegression(t *testing.T) {
	td, _ := newTxProviderTestData(t)
	blockNumber := uint64(10)
	oldBlockHash := common.HexToHash("0x10")
	newBlockHash := common.HexToHash("0x11")
	txHash := common.HexToHash("0xaa")
	oldLog := txFixtureLog(blockNumber, oldBlockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, blockNumber, oldBlockHash, oldLog)

	rpc := newGatedTxRPC()
	rpc.setTx(txHash, txFixtureRPCJSON(blockNumber, oldBlockHash, txHash))
	gate := make(chan struct{})
	rpc.setGate(gate)
	td.mdr.rpcClient = rpc

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type result struct {
		tx  *aggkittypes.RPCTransaction
		err error
	}
	providerA := make(chan result, 1)
	go func() {
		tx, err := td.mdr.TransactionByLog(ctx, oldLog)
		providerA <- result{tx, err}
	}()

	// A's stale RPC must be in flight before the reorg is committed.
	select {
	case <-rpc.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider A never entered the RPC")
	}

	// Commit an actual reorg that removes the covered block/log while A waits.
	txRegressionCommitReorg(t, td, blockNumber)
	// Keep the in-memory state coherent with the rolled-back storage.
	txRegressionShrinkState(t, td, txFixtureAddr, blockNumber, blockNumber)
	txRegressionRequireHeaderAbsent(t, td, blockNumber)

	// Release the stale HTTP result: it must not be cached.
	close(gate)

	resA := <-providerA
	require.ErrorIs(t, resA.err, aggkittypes.ErrTransactionInclusionMismatch)
	require.Nil(t, resA.tx)

	orphan, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, oldBlockHash)
	require.NoError(t, err)
	require.Nil(t, orphan, "a stale in-flight result must never be cached")

	// A new canonical block at the same height with the same tx hash but a
	// different block hash must be resolved from scratch.
	newLog := txFixtureLog(blockNumber, newBlockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, blockNumber, newBlockHash, newLog)
	rpc.setTx(txHash, txFixtureRPCJSON(blockNumber, newBlockHash, txHash))

	resolved, err := td.mdr.TransactionByLog(ctx, newLog)
	require.NoError(t, err)
	require.NotNil(t, resolved)
	require.Equal(t, txHash, resolved.Hash())
	minedHash, ok := resolved.BlockHash()
	require.True(t, ok)
	require.Equal(t, newBlockHash, minedHash, "the new inclusion must not reuse the old (poisoned) flight")
	txIndex, ok := resolved.TransactionIndex()
	require.True(t, ok)
	require.Equal(t, uint64(0), txIndex)
	require.Equal(t, 2, rpc.callCount(), "the stale flight must not force a third RPC")

	// The new canonical inclusion is persisted under the new block hash only.
	fresh, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, newBlockHash)
	require.NoError(t, err)
	require.NotNil(t, fresh)
	stale, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, oldBlockHash)
	require.NoError(t, err)
	require.Nil(t, stale, "the stale inclusion must never be resurrected")
}

// ---------------------------------------------------------------------------
// 3. Fabricated / archived source logs
// ---------------------------------------------------------------------------

// TestTransactionByLog_FabricatedSourceLogRejected pins that a log that was
// never downloaded (absent, wrong address, wrong log index or wrong transaction
// index) can never force a transaction RPC, and that a log whose block was
// reorged away is reported as a mismatch instead of an "unavailable" that would
// be retried forever.
func TestTransactionByLog_FabricatedSourceLogRejected(t *testing.T) {
	blockNumber := uint64(10)
	blockHash := common.HexToHash("0x10")
	storedTxHash := common.HexToHash("0xaa")

	newFixture := func(t *testing.T) (*testDataEVMMultidownloader, *fakeTransactionRPC, ethtypes.Log) {
		t.Helper()
		td, fake := newTxProviderTestData(t)
		stored := txFixtureLog(blockNumber, blockHash, storedTxHash, 0, []common.Hash{txFixtureT0})
		txFixtureStoreBlockAndLogs(t, td, blockNumber, blockHash, stored)
		return td, fake, stored
	}

	t.Run("absent log", func(t *testing.T) {
		td, fake, _ := newFixture(t)
		fabricated := txFixtureLog(blockNumber, blockHash, common.HexToHash("0xdead"), 0,
			[]common.Hash{txFixtureT0})

		got, err := td.mdr.TransactionByLog(t.Context(), fabricated)
		require.ErrorIs(t, err, aggkittypes.ErrTransactionUnavailable)
		require.NotErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
		require.Nil(t, got)
		require.Zero(t, fake.callCount(), "a fabricated log must never trigger an RPC")
	})

	t.Run("wrong address", func(t *testing.T) {
		td, fake, _ := newFixture(t)
		fabricated := txFixtureLog(blockNumber, blockHash, storedTxHash, 0, []common.Hash{txFixtureT0})
		fabricated.Address = txRegressionOtherAddr

		got, err := td.mdr.TransactionByLog(t.Context(), fabricated)
		require.ErrorIs(t, err, aggkittypes.ErrTransactionUnavailable)
		require.NotErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
		require.Nil(t, got)
		require.Zero(t, fake.callCount(), "an uncovered address must never trigger an RPC")
	})

	t.Run("wrong log index", func(t *testing.T) {
		td, fake, _ := newFixture(t)
		fabricated := txFixtureLog(blockNumber, blockHash, storedTxHash, 7, []common.Hash{txFixtureT0})

		got, err := td.mdr.TransactionByLog(t.Context(), fabricated)
		require.ErrorIs(t, err, aggkittypes.ErrTransactionUnavailable)
		require.NotErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
		require.Nil(t, got)
		require.Zero(t, fake.callCount(), "a mismatching log index must never trigger an RPC")
	})

	t.Run("wrong transaction index", func(t *testing.T) {
		td, fake, _ := newFixture(t)
		fabricated := txFixtureLog(blockNumber, blockHash, storedTxHash, 0, []common.Hash{txFixtureT0})
		fabricated.TxIndex = 3

		got, err := td.mdr.TransactionByLog(t.Context(), fabricated)
		require.ErrorIs(t, err, aggkittypes.ErrTransactionUnavailable)
		require.NotErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
		require.Nil(t, got)
		require.Zero(t, fake.callCount(), "a mismatching transaction index must never trigger an RPC")
	})

	t.Run("archived old block is a mismatch, not forever unavailable", func(t *testing.T) {
		td, fake, stored := newFixture(t)
		txRegressionCommitReorg(t, td, blockNumber)
		txRegressionShrinkState(t, td, txFixtureAddr, blockNumber, blockNumber)
		txRegressionRequireHeaderAbsent(t, td, blockNumber)

		got, err := td.mdr.TransactionByLog(t.Context(), stored)
		require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
		require.NotErrorIs(t, err, aggkittypes.ErrTransactionUnavailable,
			"an archived source block must not be reported as retryable-unavailable forever")
		require.Nil(t, got)
		require.Zero(t, fake.callCount(), "an archived source block must never trigger an RPC")
	})
}
