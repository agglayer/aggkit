package multidownloader

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Incremental worker helpers
// ---------------------------------------------------------------------------

// incrDemand is the single immutable demand shared by the incremental worker
// tests: one contract, one topic0, from block 1 up to the latest block.
func incrDemand() transactionDemand {
	return transactionDemand{
		SyncerID:  "s1",
		Address:   txFixtureAddr,
		FromBlock: 1,
		ToBlock:   aggkittypes.LatestBlock,
		Topic0:    []common.Hash{txFixtureT0},
	}
}

// incrSeedLog persists one canonical block header + log WITHOUT touching the
// in-memory synced state (coverage is set explicitly by incrSetCoverage).
func incrSeedLog(t *testing.T, td *testDataEVMMultidownloader, lg ethtypes.Log) {
	t.Helper()
	headers := aggkittypes.ListBlockHeaders{
		aggkittypes.NewBlockHeader(lg.BlockNumber, lg.BlockHash, lg.BlockTimestamp, nil),
	}
	require.NoError(t, td.realStorage.SaveEthLogsWithHeaders(nil, headers, []ethtypes.Log{lg}, true))
}

// incrSetCoverage replaces the in-memory synced state with one covered segment
// [from,to] for the fixture address.
func incrSetCoverage(td *testDataEVMMultidownloader, from, to uint64) {
	state := NewEmptyState()
	state.Synced.Add(mdrtypes.SyncSegment{
		ContractAddr: txFixtureAddr,
		BlockRange:   aggkitcommon.NewBlockRange(from, to),
	})
	td.mdr.state = state
}

// incrMoveBlockToReorged simulates a reorg of one block by moving it (and its
// logs/transactions) to the reorged tables, exactly like the real reorg
// processor does.
func incrMoveBlockToReorged(t *testing.T, td *testDataEVMMultidownloader, block uint64) {
	t.Helper()
	tx, err := td.realStorage.NewTx(context.Background())
	require.NoError(t, err)
	_, err = td.realStorage.InsertReorgAndMoveReorgedBlocksAndLogs(tx, mdrtypes.ReorgData{
		BlockRangeAffected:        aggkitcommon.NewBlockRange(block, block),
		DetectedAtBlock:           block,
		NetworkFinalizedBlockName: aggkittypes.LatestBlock,
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

// ---------------------------------------------------------------------------
// Scheduler unit tests: advance, failed-page retry, reorg rewind
// ---------------------------------------------------------------------------

func TestTransactionScheduler_AdvanceRetryRewind(t *testing.T) {
	s := newTransactionScheduler()
	d := incrDemand()
	s.syncDemands([]transactionDemand{d})

	// First plan: four brand new pages, no retries.
	plan := s.plan(d, 1, 20, 5)
	require.Empty(t, plan.retries)
	require.Equal(t,
		[]transactionPageRange{{1, 5}, {6, 10}, {11, 15}, {16, 20}},
		plan.pages)

	// Complete 1-5 and 6-10, fail 11-15.
	s.commit(d, 1, 5, false, s.generation)
	s.commit(d, 6, 10, false, s.generation)
	s.commit(d, 11, 15, true, s.generation)

	// Next plan: the failed page is retried first, then only the remaining new
	// page. The completed pages are never planned again.
	plan = s.plan(d, 1, 20, 5)
	require.Equal(t, []transactionPageRange{{11, 15}}, plan.retries)
	require.Equal(t, []transactionPageRange{{16, 20}}, plan.pages)

	// Retry succeeds, new page succeeds: nothing left, front advanced past the
	// covered range.
	s.commit(d, 11, 15, false, s.generation)
	s.commit(d, 16, 20, false, s.generation)
	plan = s.plan(d, 1, 20, 5)
	require.Empty(t, plan.retries)
	require.Empty(t, plan.pages)

	// A reorg at block 11 rewinds the cursor: the region at/after 11 is planned
	// again and the (now empty) retry queue stays empty.
	genBefore := s.generation
	s.rewindFrom(11)
	require.Greater(t, s.generation, genBefore)
	plan = s.plan(d, 1, 20, 5)
	require.Empty(t, plan.retries)
	require.Equal(t, []transactionPageRange{{11, 15}, {16, 20}}, plan.pages)
}

func TestTransactionScheduler_RetryQueueOverflowRewinds(t *testing.T) {
	s := newTransactionScheduler()
	d := incrDemand()
	s.syncDemands([]transactionDemand{d})

	// Fill the retry queue exactly to its bound: memory stays bounded and the
	// cursor advances normally while the queue still has room.
	for i := uint64(0); i < maxRetryPages; i++ {
		s.commit(d, i, i, true, s.generation)
	}
	s.mu.Lock()
	sched := s.schedules[transactionDemandSignature(d)]
	require.Equal(t, maxRetryPages, len(sched.retry))
	require.Equal(t, uint64(maxRetryPages), sched.frontier)
	s.mu.Unlock()

	// One more failed page overflows the queue. The gap is never dropped: the
	// cursor is rewound to the OLDEST queued page so the whole region is
	// re-covered, and the queue restarts bounded at a single entry.
	s.commit(d, maxRetryPages, maxRetryPages, true, s.generation)
	s.mu.Lock()
	sched = s.schedules[transactionDemandSignature(d)]
	require.Equal(t, 1, len(sched.retry))
	require.Equal(t, uint64(0), sched.frontier,
		"overflow must rewind the cursor to the oldest failed page")
	s.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Scheduler unit tests: a plan built before a reorg is never committed after it
// ---------------------------------------------------------------------------

func TestTransactionScheduler_StalePlanRejectedAfterRewind(t *testing.T) {
	s := newTransactionScheduler()
	d := incrDemand()
	s.syncDemands([]transactionDemand{d})

	// Epoch 0: complete [1..100], advancing the cursor to 101.
	first := s.plan(d, 1, 100, 50)
	require.Equal(t, []transactionPageRange{{1, 50}, {51, 100}}, first.pages)
	s.commit(d, 1, 50, false, first.generation)
	s.commit(d, 51, 100, false, first.generation)

	// A plan built in epoch 0 for the following pages, still unprocessed when
	// the reorg lands.
	stale := s.plan(d, 1, 200, 50)
	require.Equal(t, []transactionPageRange{{101, 150}, {151, 200}}, stale.pages)
	require.Equal(t, first.generation, stale.generation)

	// A reorg at block 50 rewinds the cursor to 50 and bumps the generation.
	s.rewindFrom(50)
	require.Greater(t, s.generation, stale.generation)
	s.mu.Lock()
	sched := s.schedules[transactionDemandSignature(d)]
	require.Equal(t, uint64(50), sched.frontier)
	s.mu.Unlock()

	// The stale plan's pages belong to a superseded epoch: a successful commit
	// must not advance the cursor and a failed one must not poison the queue.
	s.commit(d, 101, 150, false, stale.generation)
	s.commit(d, 151, 200, true, stale.generation)
	s.mu.Lock()
	require.Equal(t, uint64(50), sched.frontier,
		"a stale plan must not advance the cursor after a rewind")
	require.Empty(t, sched.retry,
		"a stale failed page must not poison the new retry queue")
	s.mu.Unlock()

	// A fresh plan in the new epoch resumes at the rewound frontier and commits
	// normally.
	fresh := s.plan(d, 1, 200, 50)
	require.Equal(t,
		[]transactionPageRange{{50, 99}, {100, 149}, {150, 199}, {200, 200}}, fresh.pages)
	require.Greater(t, fresh.generation, stale.generation)
	s.commit(d, 50, 99, false, fresh.generation)
	s.mu.Lock()
	require.Equal(t, uint64(100), sched.frontier)
	s.mu.Unlock()
}

func TestTransactionScheduler_ReconciledDemandInvalidatesStalePlan(t *testing.T) {
	s := newTransactionScheduler()
	d := incrDemand()
	s.syncDemands([]transactionDemand{d})

	stale := s.plan(d, 1, 100, 50)
	require.Equal(t, []transactionPageRange{{1, 50}, {51, 100}}, stale.pages)

	// Drop the registration, then re-add it before the stale plan is committed.
	// The reconciliation must invalidate the stale plan so it can never advance
	// the reconstructed schedule.
	s.syncDemands(nil)
	require.Greater(t, s.generation, stale.generation)
	s.syncDemands([]transactionDemand{d})

	s.commit(d, 51, 100, false, stale.generation)
	s.mu.Lock()
	sched := s.schedules[transactionDemandSignature(d)]
	require.Equal(t, uint64(0), sched.frontier,
		"a stale plan must not advance a reconstructed schedule")
	require.False(t, sched.initialized,
		"a stale plan must not initialize a reconstructed schedule")
	s.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Incremental worker: completed pages are never re-read
// ---------------------------------------------------------------------------

func TestIncrementalWorker_SecondScanSkipsCompletedPages(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	hash10 := common.HexToHash("0x10")
	txA := common.HexToHash("0xaa")
	hash11 := common.HexToHash("0x11")
	txB := common.HexToHash("0xbb")
	lg10 := txFixtureLog(10, hash10, txA, 0, []common.Hash{txFixtureT0})
	lg11 := txFixtureLog(11, hash11, txB, 0, []common.Hash{txFixtureT0})
	incrSeedLog(t, td, lg10)
	incrSeedLog(t, td, lg11)
	incrSetCoverage(td, 1, 40)
	fake.setTx(txA, txFixtureRPCJSON(10, hash10, txA))
	fake.setTx(txB, txFixtureRPCJSON(11, hash11, txB))

	td.mdr.transactionDemands = []transactionDemand{incrDemand()}
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(40), nil).Maybe()

	td.mdr.scanMissingTransactions(t.Context())
	require.Equal(t, 2, fake.callCount())
	d1 := td.mdr.TransactionWorkerDiagnostics()
	require.Equal(t, uint64(1), d1.PagesScanned, "the covered range is a single page")
	require.Equal(t, uint64(2), d1.LogsExamined)

	// A second scan must not touch the already completed page again.
	td.mdr.scanMissingTransactions(t.Context())
	require.Equal(t, 2, fake.callCount(), "no new RPC for an already enriched page")
	d2 := td.mdr.TransactionWorkerDiagnostics()
	require.Equal(t, d1.PagesScanned, d2.PagesScanned,
		"a completed page must not be re-read on a later tick")
	require.Equal(t, d1.LogsExamined, d2.LogsExamined)
}

func TestIncrementalWorker_AppendsOnlyNewRange(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	hash10 := common.HexToHash("0x10")
	txA := common.HexToHash("0xaa")
	hash50 := common.HexToHash("0x50")
	txB := common.HexToHash("0xbb")
	lg10 := txFixtureLog(10, hash10, txA, 0, []common.Hash{txFixtureT0})
	lg50 := txFixtureLog(50, hash50, txB, 0, []common.Hash{txFixtureT0})
	incrSeedLog(t, td, lg10)
	incrSetCoverage(td, 1, 40)
	fake.setTx(txA, txFixtureRPCJSON(10, hash10, txA))
	fake.setTx(txB, txFixtureRPCJSON(50, hash50, txB))

	td.mdr.transactionDemands = []transactionDemand{incrDemand()}
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(40), nil).Maybe()
	td.mdr.scanMissingTransactions(t.Context())
	require.Equal(t, 1, fake.callCount())
	d1 := td.mdr.TransactionWorkerDiagnostics()
	require.Equal(t, uint64(1), d1.LogsExamined)

	// New logs are committed past the old frontier: only the new page is read.
	incrSeedLog(t, td, lg50)
	incrSetCoverage(td, 1, 60)
	td.mockBlockNotifierManager.ExpectedCalls = nil
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(60), nil).Maybe()

	td.mdr.scanMissingTransactions(t.Context())
	require.Equal(t, 2, fake.callCount(), "only the new page's transaction is fetched")
	d2 := td.mdr.TransactionWorkerDiagnostics()
	require.Equal(t, uint64(2), d2.PagesScanned, "exactly one new page was scanned")
	require.Equal(t, d1.LogsExamined+1, d2.LogsExamined,
		"only the appended page's logs were examined")
}

// ---------------------------------------------------------------------------
// Incremental worker: startup with cached vs uncached data
// ---------------------------------------------------------------------------

func TestIncrementalWorker_StartupCachedVsUncached(t *testing.T) {
	t.Run("uncached is fetched once at startup", func(t *testing.T) {
		td, fake := newTxProviderTestData(t)
		hash := common.HexToHash("0x10")
		txHash := common.HexToHash("0xaa")
		incrSeedLog(t, td, txFixtureLog(10, hash, txHash, 0, []common.Hash{txFixtureT0}))
		incrSetCoverage(td, 1, 20)
		fake.setTx(txHash, txFixtureRPCJSON(10, hash, txHash))

		td.mdr.transactionDemands = []transactionDemand{incrDemand()}
		td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
			Return(uint64(20), nil).Maybe()

		// New run: discard cursors and rebuild from the persisted coverage.
		td.mdr.txScheduler.resetAll()
		td.mdr.txScheduler.syncDemands(td.mdr.transactionDemands)
		td.mdr.scanMissingTransactions(t.Context())

		require.Equal(t, 1, fake.callCount())
		stored, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, hash)
		require.NoError(t, err)
		require.NotNil(t, stored)
	})

	t.Run("cached is ready without any rpc", func(t *testing.T) {
		td, fake := newTxProviderTestData(t)
		hash := common.HexToHash("0x10")
		txHash := common.HexToHash("0xaa")
		incrSeedLog(t, td, txFixtureLog(10, hash, txHash, 0, []common.Hash{txFixtureT0}))
		incrSetCoverage(td, 1, 20)
		require.NoError(t, td.mdr.transactionStorage.SaveTransactions(nil,
			[]aggkittypes.RPCTransaction{txFixtureRPC(10, hash, txHash)}))

		td.mdr.transactionDemands = []transactionDemand{incrDemand()}
		td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
			Return(uint64(20), nil).Maybe()

		td.mdr.txScheduler.resetAll()
		td.mdr.txScheduler.syncDemands(td.mdr.transactionDemands)
		td.mdr.scanMissingTransactions(t.Context())

		require.Zero(t, fake.callCount(), "already cached data must not trigger any RPC")
	})
}

// ---------------------------------------------------------------------------
// Incremental worker: reorg re-examines the reorged region, refreshing a tx
// that was re-included under a new block hash
// ---------------------------------------------------------------------------

func TestIncrementalWorker_ReorgSameTxNewHashRefresh(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	hashOld := common.HexToHash("0x10")
	hashNew := common.HexToHash("0x99")
	txHash := common.HexToHash("0xaa")

	// Old canonical chain: block 10/hashOld with txHash, already cached.
	incrSeedLog(t, td, txFixtureLog(10, hashOld, txHash, 0, []common.Hash{txFixtureT0}))
	incrSetCoverage(td, 1, 10)
	require.NoError(t, td.mdr.transactionStorage.SaveTransactions(nil,
		[]aggkittypes.RPCTransaction{txFixtureRPC(10, hashOld, txHash)}))

	td.mdr.transactionDemands = []transactionDemand{incrDemand()}
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(10), nil).Maybe()

	td.mdr.scanMissingTransactions(t.Context())
	require.Zero(t, fake.callCount(), "the old inclusion is a cache hit")
	require.Equal(t, uint64(1), td.mdr.TransactionWorkerDiagnostics().PagesScanned)

	// Reorg: block 10 is replaced by block 10/hashNew and the SAME tx hash is
	// re-included there.
	incrMoveBlockToReorged(t, td, 10)
	incrSeedLog(t, td, txFixtureLog(10, hashNew, txHash, 0, []common.Hash{txFixtureT0}))
	fake.setTx(txHash, txFixtureRPCJSON(10, hashNew, txHash))

	// The reorg rewinds the cursor so the replaced block is re-examined.
	td.mdr.rewindTransactionSchedulerFrom(10)
	td.mdr.scanMissingTransactions(t.Context())

	require.Equal(t, 1, fake.callCount(),
		"the same tx re-included under a new block hash must be refreshed")
	stored, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, hashNew)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, uint64(2), td.mdr.TransactionWorkerDiagnostics().PagesScanned,
		"the rewound block must be scanned again")
}

// ---------------------------------------------------------------------------
// Canceled scans never advance the cursor
// ---------------------------------------------------------------------------

func TestIncrementalWorker_CanceledScanDoesNotAdvanceFrontier(t *testing.T) {
	td, _ := newTxProviderTestData(t)
	d := incrDemand()
	td.mdr.transactionDemands = []transactionDemand{d}
	td.mdr.txScheduler.syncDemands([]transactionDemand{d})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	td.mdr.processTransactionPages(ctx, d, []transactionPageRange{{from: 1, to: 10}}, td.mdr.txScheduler.generation)

	// The page must still be offered by the next plan: the canceled scan did not
	// commit a (false) completion.
	plan := td.mdr.txScheduler.plan(d, 1, 10, 5000)
	require.Equal(t, []transactionPageRange{{from: 1, to: 10}}, plan.pages)
}

// ---------------------------------------------------------------------------
// Failed pages are retried (and never forgotten)
// ---------------------------------------------------------------------------

func TestIncrementalWorker_FailedPageRetried(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	hash := common.HexToHash("0x10")
	txHash := common.HexToHash("0xaa")
	incrSeedLog(t, td, txFixtureLog(10, hash, txHash, 0, []common.Hash{txFixtureT0}))
	incrSetCoverage(td, 1, 20)

	td.mdr.transactionDemands = []transactionDemand{incrDemand()}
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(20), nil).Maybe()

	// First scan: the RPC fails, so the page is kept in the retry queue.
	fake.setErr(txHash, context.DeadlineExceeded)
	td.mdr.scanMissingTransactions(t.Context())
	require.Equal(t, 1, fake.callCount())
	require.GreaterOrEqual(t, td.mdr.TransactionWorkerDiagnostics().Failures, uint64(1))

	// Second scan: the RPC now succeeds; the failed page is retried and the tx
	// is persisted (the gap was never forgotten).
	fake.setErr(txHash, nil)
	fake.setTx(txHash, txFixtureRPCJSON(10, hash, txHash))
	td.mdr.scanMissingTransactions(t.Context())
	stored, err := td.mdr.transactionStorage.GetTransaction(nil, txHash, hash)
	require.NoError(t, err)
	require.NotNil(t, stored)
}

// ---------------------------------------------------------------------------
// Diagnostics snapshot is bounded and URI-free
// ---------------------------------------------------------------------------

func TestTransactionWorkerDiagnostics_StableJSONShape(t *testing.T) {
	td, _ := newTxProviderTestData(t)
	d := td.mdr.TransactionWorkerDiagnostics()
	require.Zero(t, d.Scans)
	require.Zero(t, d.Failures)

	// The report must serialize to a stable, bounded set of named counters and
	// must never leak a free-form field such as an RPC URL.
	raw, err := json.Marshal(d)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	require.ElementsMatch(t, []string{
		"scans", "pagesScanned", "retryPages", "logsExamined", "cacheLookups", "failures",
	}, keys)
	require.NotContains(t, fields, "url")
}

// ---------------------------------------------------------------------------
// Scheduler unit tests: a retry-queue overflow invalidates the outstanding plan
// ---------------------------------------------------------------------------

// TestTransactionScheduler_OverflowInvalidatesOutstandingPlan pins the fix for
// the retry-overflow data loss: when the bounded retry queue overflows, the
// cursor is rewound to the oldest queued page AND every outstanding plan is
// invalidated. Without the invalidation a later page of the SAME (now stale)
// plan could be accepted and advance the cursor past the rewound region,
// silently dropping every gap below it.
func TestTransactionScheduler_OverflowInvalidatesOutstandingPlan(t *testing.T) {
	s := newTransactionScheduler()
	d := incrDemand()
	s.syncDemands([]transactionDemand{d})

	// One plan's worth of pages 1..maxRetryPages fail, filling the retry queue.
	gen := s.generation
	for i := uint64(1); i <= maxRetryPages; i++ {
		s.commit(d, i, i, true, gen)
	}
	s.mu.Lock()
	sched := s.schedules[transactionDemandSignature(d)]
	require.Len(t, sched.retry, maxRetryPages)
	require.Equal(t, uint64(maxRetryPages+1), sched.frontier)
	s.mu.Unlock()

	// The next failed page overflows the queue: the cursor is rewound to the
	// oldest queued page AND the generation is bumped so the remaining pages of
	// the SAME (now stale) plan can no longer be accepted.
	require.True(t, s.commit(d, maxRetryPages+1, maxRetryPages+1, true, gen))
	require.Greater(t, s.generation, gen,
		"an overflow rewind must invalidate every outstanding plan")

	// A later SUCCESS from the stale plan must be rejected: accepting it would
	// advance the cursor past the rewound region and silently drop all the gaps
	// below it (the original data-loss bug).
	require.False(t, s.commit(d, maxRetryPages+2, maxRetryPages+2, false, gen),
		"a stale plan page must be rejected after an overflow rewind")
	s.mu.Lock()
	require.Equal(t, uint64(1), sched.frontier,
		"the overflow rewind must survive a stale success")
	require.Len(t, sched.retry, 1)
	require.Contains(t, sched.retry, uint64(maxRetryPages+1))
	s.mu.Unlock()

	// A fresh plan starts at the rewound frontier and re-covers the lost holes.
	plan := s.plan(d, 1, maxRetryPages+2, 1)
	require.Equal(t, []transactionPageRange{{maxRetryPages + 1, maxRetryPages + 1}}, plan.retries)
	require.NotEmpty(t, plan.pages)
	require.Equal(t, uint64(1), plan.pages[0].from,
		"a fresh plan must restart at the rewound frontier to recover the dropped gaps")
}

// TestTransactionScheduler_RewindRetainsSubReorgRetries pins the fix for the
// rewind gap loss: a reorg rewind drops only the retry pages it re-covers (those
// at/after the reorg). A queued page strictly below the reorg stays canonical
// and must be retained, or its unresolved transactions would never be retried
// again (the frontier is only rewound to the reorg point).
func TestTransactionScheduler_RewindRetainsSubReorgRetries(t *testing.T) {
	s := newTransactionScheduler()
	d := incrDemand()
	s.syncDemands([]transactionDemand{d})

	// Pages 1..5 fail: they stay canonical and are queued for retry.
	for i := uint64(1); i <= 5; i++ {
		s.commit(d, i, i, true, s.generation)
	}
	// Pages 6..89 complete, then page 90 fails: the cursor advances past the
	// reorg point and 90 is queued too.
	for i := uint64(6); i <= 89; i++ {
		s.commit(d, i, i, false, s.generation)
	}
	s.commit(d, 90, 90, true, s.generation)
	s.mu.Lock()
	sched := s.schedules[transactionDemandSignature(d)]
	require.Equal(t, uint64(91), sched.frontier)
	require.Len(t, sched.retry, 6)
	s.mu.Unlock()

	// A reorg at block 90 rewinds the cursor. The queued page 90 is re-covered
	// by the rewind and is dropped, but pages 1..5 start strictly below the reorg
	// and MUST be retained (they are never planned again otherwise).
	s.rewindFrom(90)
	s.mu.Lock()
	require.Equal(t, uint64(90), sched.frontier)
	require.Len(t, sched.retry, 5, "sub-reorg retry pages must survive the rewind")
	require.NotContains(t, sched.retry, uint64(90),
		"a retry page at/after the reorg is re-covered and must be dropped")
	for i := uint64(1); i <= 5; i++ {
		require.Contains(t, sched.retry, i)
	}
	s.mu.Unlock()

	// The next plan retries the retained pages first, then resumes at the
	// rewound frontier, so no still-canonical gap is lost.
	plan := s.plan(d, 1, 90, 1)
	require.Equal(t,
		[]transactionPageRange{{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}},
		plan.retries)
	require.Equal(t, []transactionPageRange{{90, 90}}, plan.pages)
}

// ---------------------------------------------------------------------------
// Incremental worker: the coverage read and the plan are atomic against a reorg
// ---------------------------------------------------------------------------

// TestIncrementalWorker_ReorgBetweenCoverageAndPlanIsAtomic pins the fix for the
// coverage/epoch race: the worker reads the synced coverage and stamps the plan
// under ONE read lock, and a reorg publishes the shrunken state and rewinds the
// scheduler under the write lock. A reorg can therefore never land between the
// coverage read and the plan, which would otherwise let an empty (removed) range
// be stamped with the NEW generation and marked complete, skipping the canonical
// logs re-downloaded there later.
func TestIncrementalWorker_ReorgBetweenCoverageAndPlanIsAtomic(t *testing.T) {
	td, fake := newTxProviderTestData(t)
	d := incrDemand()
	td.mdr.transactionDemands = []transactionDemand{d}
	// Two pages over [1,200]: [1,100] and [101,200].
	td.mdr.cfg.BlockChunkSize = 100

	// First scan: coverage [1,99] with no logs advances the cursor to 100.
	incrSetCoverage(td, 1, 99)
	td.mdr.txScheduler.syncDemands([]transactionDemand{d})
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(99), nil).Maybe()
	td.mdr.scanMissingTransactions(t.Context())

	td.mdr.txScheduler.mu.Lock()
	sched := td.mdr.txScheduler.schedules[transactionDemandSignature(d)]
	require.Equal(t, uint64(100), sched.frontier)
	td.mdr.txScheduler.mu.Unlock()

	// The lock-free wrapper must observe the same coverage (kept referenced so
	// the locking read helper stays covered too).
	synced, ok := td.mdr.snapshotSyncedSegment(d.Address)
	require.True(t, ok)
	require.Equal(t, uint64(99), synced.ToBlock)

	// New logs commit: the in-memory coverage extends to [1,200]. Arm a barrier
	// that pauses the worker AFTER it reads the coverage and BEFORE it stamps the
	// plan.
	incrSetCoverage(td, 1, 200)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	testHookAfterCoverageBeforePlan = func() {
		once.Do(func() { close(entered) })
		<-release
	}
	t.Cleanup(func() { testHookAfterCoverageBeforePlan = nil })

	td.mockBlockNotifierManager.ExpectedCalls = nil
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(200), nil).Maybe()

	scanDone := make(chan struct{})
	go func() {
		td.mdr.scanMissingTransactions(t.Context())
		close(scanDone)
	}()

	// Wait until the worker holds the read lock with the coverage already read.
	<-entered

	// A reorg publishes the shrunken state and rewinds the scheduler under the
	// write lock. It MUST block until the worker has stamped its plan.
	reorgStarted := make(chan struct{})
	reorgDone := make(chan struct{})
	go func() {
		close(reorgStarted)
		td.mdr.mutex.Lock()
		st := NewEmptyState()
		st.Synced.Add(mdrtypes.SyncSegment{
			ContractAddr: txFixtureAddr,
			BlockRange:   aggkitcommon.NewBlockRange(1, 99),
		})
		td.mdr.state = st
		td.mdr.rewindTransactionSchedulerFrom(100)
		td.mdr.mutex.Unlock()
		close(reorgDone)
	}()
	<-reorgStarted
	select {
	case <-reorgDone:
		close(release)
		t.Fatal("the reorg was published between the coverage read and the plan: " +
			"the coverage read and the plan are not atomic")
	case <-time.After(500 * time.Millisecond):
	}

	// Let the worker stamp the plan; only then can the reorg proceed.
	close(release)
	<-scanDone
	<-reorgDone
	// Disarm the hook so the verification scan below runs normally.
	testHookAfterCoverageBeforePlan = nil

	// The stale plan must not have advanced the cursor past the rewound region:
	// the removed (empty) range [100,200] was never marked complete.
	td.mdr.txScheduler.mu.Lock()
	require.LessOrEqual(t, sched.frontier, uint64(100),
		"the removed empty range must not be marked complete from the stale coverage")
	td.mdr.txScheduler.mu.Unlock()

	// A canonical log appended at block 150 after the reorg (with the coverage
	// re-extended) MUST be picked up: the range was never skipped.
	hash150 := common.HexToHash("0x150")
	tx150 := common.HexToHash("0x1500")
	incrSeedLog(t, td, txFixtureLog(150, hash150, tx150, 0, []common.Hash{txFixtureT0}))
	incrSetCoverage(td, 1, 200)
	fake.setTx(tx150, txFixtureRPCJSON(150, hash150, tx150))
	td.mockBlockNotifierManager.ExpectedCalls = nil
	td.mockBlockNotifierManager.EXPECT().GetCurrentBlockNumber(mock.Anything, aggkittypes.LatestBlock).
		Return(uint64(200), nil).Maybe()

	td.mdr.scanMissingTransactions(t.Context())
	stored, err := td.mdr.transactionStorage.GetTransaction(nil, tx150, hash150)
	require.NoError(t, err)
	require.NotNil(t, stored,
		"a canonical log appended in the removed range must still be fetched")
	require.Equal(t, 1, fake.callCount())
}
