package multidownloader

// Transaction lifecycle regression tests for the OPTIONAL transaction
// capabilities of the EVMMultidownloader. They are deliberately kept in their
// own file so they can be run in isolation:
//
//	go test -count=1 -short -race ./multidownloader -run 'LifecycleRegression'
//
// Covered here:
//   - the shared HTTP limiter that bounds BOTH foreground (TransactionByLog)
//     and background (worker page scan) transaction fetches;
//   - the log-only FilterLogs read staying coherent while a transaction fetch
//     is stalled, plus the shared-flight cancellation semantics (one waiter can
//     give up without aborting the fetch another waiter still needs);
//   - the Start/Stop lifecycle: the run context is cancelled, every blocked RPC
//     and worker is joined before isRunning is published as false, the
//     per-request timeout is transient, and a pre-cancelled Initialize context
//     can never leave an indefinite background request.

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
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	ethrpc "github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Deterministic test doubles (no sleeps / no polling for synchronization)
// ---------------------------------------------------------------------------

// tlcDoneSignalContext signals once, the first time Done() is observed. A shared
// flight waiter calls ctx.Done() only after it has joined the in-flight request,
// so the signal is an exact, race-free "this caller is now waiting" event.
type tlcDoneSignalContext struct {
	context.Context
	once     sync.Once
	signaled chan struct{}
}

func tlcNewDoneSignalContext(parent context.Context) *tlcDoneSignalContext {
	return &tlcDoneSignalContext{Context: parent, signaled: make(chan struct{})}
}

func (c *tlcDoneSignalContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.signaled) })
	return c.Context.Done()
}

// tlcGatedRPC is an RPCClienter that records the high-water mark of concurrent
// eth_getTransactionByHash calls and blocks each call until release. It lets a
// test observe the shared concurrency bound without relying on timing.
type tlcGatedRPC struct {
	*fakeTransactionRPC
	mu        sync.Mutex
	active    int
	highWater int
	entered   chan struct{}
	release   chan struct{}
}

func tlcNewGatedRPC() *tlcGatedRPC {
	return &tlcGatedRPC{
		fakeTransactionRPC: newFakeTransactionRPC(),
		entered:            make(chan struct{}, 64),
		release:            make(chan struct{}),
	}
}

func (g *tlcGatedRPC) CallContext(ctx context.Context, result interface{},
	method string, args ...interface{}) error {
	g.mu.Lock()
	g.active++
	if g.active > g.highWater {
		g.highWater = g.active
	}
	g.mu.Unlock()

	select {
	case g.entered <- struct{}{}:
	default:
	}

	select {
	case <-g.release:
	case <-ctx.Done():
		g.mu.Lock()
		g.active--
		g.mu.Unlock()
		return ctx.Err()
	}
	g.mu.Lock()
	g.active--
	g.mu.Unlock()
	return g.fakeTransactionRPC.CallContext(ctx, result, method, args...)
}

func (g *tlcGatedRPC) highWaterMark() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.highWater
}

// tlcStallingRPC blocks every call until release is closed (or the call context
// is done) and signals entry. It is used to hold a transaction fetch in flight.
type tlcStallingRPC struct {
	mu      sync.Mutex
	calls   int
	byHash  map[common.Hash]json.RawMessage
	entered chan struct{}
	release chan struct{}
}

func tlcNewStallingRPC() *tlcStallingRPC {
	return &tlcStallingRPC{
		byHash:  make(map[common.Hash]json.RawMessage),
		entered: make(chan struct{}, 8),
		release: make(chan struct{}),
	}
}

func (s *tlcStallingRPC) setTx(h common.Hash, raw json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash[h] = raw
}

func (s *tlcStallingRPC) CallContext(ctx context.Context, result interface{},
	method string, args ...interface{}) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	select {
	case s.entered <- struct{}{}:
	default:
	}

	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}

	h, _ := args[0].(common.Hash)
	s.mu.Lock()
	raw := s.byHash[h]
	s.mu.Unlock()
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	rawResult, ok := result.(*json.RawMessage)
	if !ok {
		return fmt.Errorf("tlcStallingRPC.CallContext: unexpected result type %T", result)
	}
	*rawResult = raw
	return nil
}

func (s *tlcStallingRPC) Call(result any, method string, args ...any) error { return nil }

func (s *tlcStallingRPC) BatchCallContext(ctx context.Context, b []ethrpc.BatchElem) error {
	return nil
}

func (s *tlcStallingRPC) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// tlcBlockingRPC blocks every call until its context is done, then records the
// observed context error. It proves that a blocked RPC honors the run context.
type tlcBlockingRPC struct {
	mu      sync.Mutex
	calls   int
	lastErr error
	entered chan struct{}
}

func tlcNewBlockingRPC() *tlcBlockingRPC {
	return &tlcBlockingRPC{entered: make(chan struct{}, 8)}
}

func (b *tlcBlockingRPC) CallContext(ctx context.Context, result interface{},
	method string, args ...interface{}) error {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()

	select {
	case b.entered <- struct{}{}:
	default:
	}

	<-ctx.Done()
	err := ctx.Err()
	b.mu.Lock()
	b.lastErr = err
	b.mu.Unlock()
	return err
}

func (b *tlcBlockingRPC) Call(result any, method string, args ...any) error { return nil }

func (b *tlcBlockingRPC) BatchCallContext(ctx context.Context, elems []ethrpc.BatchElem) error {
	return nil
}

func (b *tlcBlockingRPC) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func (b *tlcBlockingRPC) lastError() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastErr
}

// tlcSeedCoveredLogs stores count canonical blocks/logs starting at fromBlock
// (one distinct covered log per block) and marks the whole range as synced, so
// the provider/worker see real covered canonical sources.
func tlcSeedCoveredLogs(t *testing.T, td *testDataEVMMultidownloader,
	fromBlock uint64, count int) []ethtypes.Log {
	t.Helper()
	headers := make(aggkittypes.ListBlockHeaders, 0, count)
	logs := make([]ethtypes.Log, 0, count)
	for i := 0; i < count; i++ {
		blockNumber := fromBlock + uint64(i)
		blockHash := common.BigToHash(big.NewInt(int64(1000 + i)))
		txHash := common.BigToHash(big.NewInt(int64(2000 + i)))
		headers = append(headers, aggkittypes.NewBlockHeader(blockNumber, blockHash, 1630000000, nil))
		logs = append(logs, txFixtureLog(blockNumber, blockHash, txHash, 0, []common.Hash{txFixtureT0}))
	}
	require.NoError(t, td.realStorage.SaveEthLogsWithHeaders(nil, headers, logs, true))
	txFixtureCoverState(td, fromBlock, fromBlock+uint64(count)-1)
	return logs
}

// ---------------------------------------------------------------------------
// 1. Shared HTTP concurrency bound (foreground provider AND worker scanning)
// ---------------------------------------------------------------------------

func TestTransactionLifecycleRegression_SharedConcurrencyBound(t *testing.T) {
	const maxParallel = 2
	const numLogs = 6

	td, _ := newTxProviderTestData(t)
	// The shared HTTP limiter is created from the config in the constructor;
	// pin both the worker pool width and the limiter to a small value so the
	// bound is easy to observe deterministically.
	td.mdr.cfg.MaxParallelBlockHeaderRetrieval = maxParallel
	td.mdr.txInflight = newTransactionInflight(context.Background(), maxParallel)

	gated := tlcNewGatedRPC()
	td.mdr.rpcClient = gated
	logs := tlcSeedCoveredLogs(t, td, 10, numLogs)
	for i := range logs {
		gated.setTx(logs[i].TxHash,
			txFixtureRPCJSON(logs[i].BlockNumber, logs[i].BlockHash, logs[i].TxHash))
	}

	// The background worker scans the same covered range for the same address.
	td.mdr.transactionDemands = []transactionDemand{{
		SyncerID:  "s1",
		Address:   txFixtureAddr,
		FromBlock: 1,
		ToBlock:   aggkittypes.LatestBlock,
		Topic0:    []common.Hash{txFixtureT0},
	}}
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(15), nil).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Drive BOTH paths at once: one worker page scan and several foreground
	// provider requests, all sharing a single limiter.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		td.mdr.scanMissingTransactions(ctx)
	}()
	foregroundErrs := make([]error, numLogs)
	for i := 0; i < numLogs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, foregroundErrs[idx] = td.mdr.TransactionByLog(ctx, logs[idx])
		}(i)
	}

	// Wait until the limiter is saturated. Every entered call is blocked inside
	// the fake and cannot leave before we release it, so the high-water mark is
	// exact (no timing assumptions).
	for i := 0; i < maxParallel; i++ {
		select {
		case <-gated.entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d/%d shared transaction RPCs started", i, maxParallel)
		}
	}
	require.LessOrEqual(t, gated.highWaterMark(), maxParallel,
		"shared HTTP concurrency must never exceed MaxParallelBlockHeaderRetrieval")
	require.Equal(t, maxParallel, gated.highWaterMark(),
		"the shared limiter must be saturated by the concurrent foreground + worker requests")

	close(gated.release)
	wg.Wait()

	for i, err := range foregroundErrs {
		require.NoErrorf(t, err, "foreground resolution of log %d failed", i)
	}
	require.Equal(t, numLogs, gated.callCount(),
		"each distinct covered log must be fetched exactly once across both paths")
	for i := range logs {
		stored, err := td.mdr.transactionStorage.GetTransaction(nil, logs[i].TxHash, logs[i].BlockHash)
		require.NoError(t, err)
		require.NotNilf(t, stored, "transaction for log %d must be persisted", i)
	}
}

// ---------------------------------------------------------------------------
// 2. Log-only read stays coherent + shared-flight cancellation semantics
// ---------------------------------------------------------------------------

func TestTransactionLifecycleRegression_LogOnlyReadAndSharedFlightCancel(t *testing.T) {
	td, _ := newTxProviderTestData(t)
	stall := tlcNewStallingRPC()
	td.mdr.rpcClient = stall

	blockHash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
	txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
	stall.setTx(txHash, txFixtureRPCJSON(10, blockHash, txHash))

	// Waiter A joins first and becomes the shared-flight leader; its RPC is
	// stalled inside the fake.
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	errA := make(chan error, 1)
	go func() {
		_, err := td.mdr.TransactionByLog(ctxA, lg)
		errA <- err
	}()
	select {
	case <-stall.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("transaction fetch did not start")
	}

	// Waiter B joins the SAME in-flight request. It blocks in its own wait.
	ctxB := tlcNewDoneSignalContext(context.Background())
	type waitResult struct {
		tx  *aggkittypes.RPCTransaction
		err error
	}
	resB := make(chan waitResult, 1)
	go func() {
		tx, err := td.mdr.TransactionByLog(ctxB, lg)
		resB <- waitResult{tx: tx, err: err}
	}()
	select {
	case <-ctxB.signaled:
	case <-time.After(5 * time.Second):
		t.Fatal("co-waiter never joined the shared transaction flight")
	}

	// The log-only read is served from storage and must stay coherent even
	// though the transaction fetch is still stalled.
	filtered, err := td.mdr.FilterLogs(context.Background(), ethereum.FilterQuery{
		Addresses: []common.Address{txFixtureAddr},
		FromBlock: big.NewInt(10),
		ToBlock:   big.NewInt(10),
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, txHash, filtered[0].TxHash)

	// Cancelling one waiter returns promptly and must NOT abort the shared
	// flight the co-waiter is still waiting on.
	cancelA()
	select {
	case err := <-errA:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled waiter did not return promptly")
	}

	// Release the stalled RPC: the co-waiter succeeds on the SAME single fetch.
	close(stall.release)
	select {
	case r := <-resB:
		require.NoError(t, r.err)
		require.NotNil(t, r.tx)
		require.Equal(t, txHash, r.tx.Hash())
	case <-time.After(5 * time.Second):
		t.Fatal("co-waiter did not complete after the RPC was released")
	}
	require.Equal(t, 1, stall.callCount(),
		"both waiters must share a single transaction RPC")
}

// ---------------------------------------------------------------------------
// 3. Start/Stop lifecycle
// ---------------------------------------------------------------------------

func TestTransactionLifecycleRegression_StartStopLifecycle(t *testing.T) {
	t.Run("Stop cancels and joins the blocked RPC and the worker", func(t *testing.T) {
		td := newEVMMultidownloaderTestData(t, true)
		td.FakeInitialized(t)

		// A real transaction storage: the plain Storager mock does not implement
		// the optional TransactionStorage extension.
		realTS, err := storage.NewMultidownloaderStorage(
			log.WithFields("test", "tx_lifecycle"),
			storage.MultidownloaderStorageConfig{DBPath: filepath.Join(t.TempDir(), "tx.sqlite")})
		require.NoError(t, err)
		ts, err := mdrtypes.AsTransactionStorage(realTS)
		require.NoError(t, err)
		td.mdr.transactionStorage = ts
		td.mdr.requiresTransactions = true
		td.mdr.cfg.PeriodToCheckReorgs = types.NewDuration(time.Hour)

		blockHash := common.HexToHash("0x10")
		txHash := common.HexToHash("0xaa")
		lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})

		state := NewEmptyState()
		state.Synced.Add(mdrtypes.SyncSegment{
			ContractAddr: txFixtureAddr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 20),
		})
		td.mdr.state = state
		td.mdr.transactionDemands = []transactionDemand{{
			SyncerID:  "s1",
			Address:   txFixtureAddr,
			FromBlock: 1,
			ToBlock:   aggkittypes.LatestBlock,
			Topic0:    []common.Hash{txFixtureT0},
		}}

		blocking := tlcNewBlockingRPC()
		td.mdr.rpcClient = blocking

		td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
			Return(uint64(100), nil).Maybe()
		td.mockStorage.EXPECT().GetEthLogs(mock.Anything, mock.Anything).
			Return([]ethtypes.Log{lg}, nil).Maybe()
		// Short-circuit the log sync loop before it needs more storage.
		td.mockStorage.EXPECT().NewTx(mock.Anything).Return(nil, fmt.Errorf("stop test")).Maybe()

		var startErr error
		var startWG sync.WaitGroup
		startWG.Add(1)
		go func() {
			defer startWG.Done()
			startErr = td.mdr.Start(context.Background())
		}()

		select {
		case <-blocking.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("transaction worker never issued a transaction RPC")
		}

		require.NoError(t, td.mdr.Stop(context.Background()))
		startWG.Wait()

		require.ErrorIs(t, startErr, context.Canceled)
		td.mdr.mutex.Lock()
		running := td.mdr.isRunning
		td.mdr.mutex.Unlock()
		require.False(t, running, "isRunning must be false once Stop returns")
		require.ErrorIs(t, blocking.lastError(), context.Canceled,
			"the blocked RPC must observe the cancelled run context")
		require.Equal(t, 1, blocking.callCount())

		// Stop is idempotent about the stopped state: a second Stop reports that
		// the run is no longer running.
		require.Error(t, td.mdr.Stop(context.Background()))
	})

	t.Run("per-request timeout is transient while the parent stays active", func(t *testing.T) {
		td, _ := newTxProviderTestData(t)
		stall := tlcNewStallingRPC()
		td.mdr.rpcClient = stall

		blockHash := common.HexToHash("0x20")
		txHash := common.HexToHash("0xbb")
		lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
		txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
		stall.setTx(txHash, txFixtureRPCJSON(10, blockHash, txHash))

		// A short private per-request timeout; the parent context is NOT
		// cancelled, so only the per-request timeout can fire.
		td.mdr.txRequestTimeout = 40 * time.Millisecond
		parent, cancelParent := context.WithCancel(context.Background())
		defer cancelParent()

		type fetchResult struct {
			tx  *aggkittypes.RPCTransaction
			err error
		}
		done := make(chan fetchResult, 1)
		go func() {
			tx, err := td.mdr.TransactionByLog(parent, lg)
			done <- fetchResult{tx: tx, err: err}
		}()

		select {
		case r := <-done:
			require.ErrorIs(t, r.err, context.DeadlineExceeded)
			require.NotErrorIs(t, r.err, aggkittypes.ErrTransactionInclusionMismatch)
			require.Nil(t, r.tx, "a timed-out fetch must not be reported as a nil-success transaction")
		case <-time.After(5 * time.Second):
			t.Fatal("per-request timeout did not bound the stalled transaction RPC")
		}
		require.NoError(t, parent.Err(), "only the per-request timeout must fire, not the parent")
		stored, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, blockHash)
		require.NoError(t, err)
		require.Nil(t, stored, "a timed-out fetch must not persist a transaction")
		require.Equal(t, 1, stall.callCount())
	})

	t.Run("Initialize with a pre-cancelled context leaves no indefinite background request",
		func(t *testing.T) {
			td, _ := newTxProviderTestData(t)
			stall := tlcNewStallingRPC()
			td.mdr.rpcClient = stall

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
			td.mockEthClient.EXPECT().ChainID(mock.Anything).Return(common.Big1, nil).Maybe()
			td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, mock.Anything).
				Return(uint64(20), nil).Maybe()

			cancelledCtx, cancel := context.WithCancel(context.Background())
			cancel()
			require.NoError(t, td.mdr.Initialize(cancelledCtx))
			require.True(t, td.mdr.requiresTransactions)

			// Seed a covered canonical log after Initialize. A fetch must still
			// be bounded by the cancelled lifetime context, never by the
			// context.Background base the constructor starts with.
			blockHash := common.HexToHash("0x30")
			txHash := common.HexToHash("0xcc")
			lg := txFixtureLog(10, blockHash, txHash, 0, []common.Hash{txFixtureT0})
			txFixtureStoreBlockAndLogs(t, td, 10, blockHash, lg)
			stall.setTx(txHash, txFixtureRPCJSON(10, blockHash, txHash))

			done := make(chan error, 1)
			go func() {
				_, err := td.mdr.TransactionByLog(context.Background(), lg)
				done <- err
			}()
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("transaction fetch did not honor the cancelled Initialize context")
			}
			require.Zero(t, stall.callCount(),
				"no RPC may be issued under a cancelled lifetime context")
		})
}
