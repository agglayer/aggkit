package multidownloader

// This file implements the OPTIONAL transaction capabilities of the
// EVMMultidownloader:
//
//   - aggkittypes.TransactionProvider (TransactionByLog), the foreground
//     demand-driven resolver used by sync consumers (e.g. bridgesync) to
//     resolve the full transaction that produced a log.
//   - an independent background worker that scans the already covered log
//     ranges and backfills missing canonical transactions.
//
// Both paths share a single per-(tx hash, expected block hash) singleflight so
// a given canonical inclusion is only ever fetched once over RPC, no matter how
// many logs/prefetch/backfill requests target it concurrently. The whole
// resolve/validate/revalidate/save sequence runs inside the shared flight (not
// just the RPC call), so a caller that arrives after the RPC response but
// before the save completes still shares the same request and can never trigger
// a duplicate fetch.
//
// They are only enabled when at least one registered syncer opts in through
// aggkittypes.TransactionRequirement{Enabled:true}. When no syncer opts in the
// provider reports a clear error and no transaction RPC or background work is
// ever performed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

const (
	// rpcMethodGetTransactionByHash is the JSON-RPC method used to fetch a
	// transaction by hash.
	rpcMethodGetTransactionByHash = "eth_getTransactionByHash"

	// defaultTransactionWorkerPeriod is the fallback background scan period used
	// when the multidownloader config does not provide one.
	defaultTransactionWorkerPeriod = 5 * time.Second

	// defaultTransactionRequestTimeout bounds a single transaction RPC request so
	// a stuck provider can never hold a shared HTTP slot forever. It is a package
	// constant (deliberately NOT part of Config) so existing config tests stay
	// valid; tests can override the private dh.txRequestTimeout field.
	defaultTransactionRequestTimeout = 30 * time.Second
)

var (
	// errTransactionProviderDisabled is returned when a caller tries to resolve
	// transactions but no registered syncer opted in. It is a normal,
	// non-sentinel error: the caller must treat the transaction path as
	// unavailable, never as "everything is available".
	errTransactionProviderDisabled = errors.New(
		"multidownloader: transaction provider is not enabled (no syncer requested transactions)")
)

// errTransactionUnavailable builds a retryable "not available yet" error that
// wraps the SHARED aggkittypes.ErrTransactionUnavailable sentinel, so consumers
// can detect the condition with errors.Is. A private sentinel would not work
// across packages.
func errTransactionUnavailable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", aggkittypes.ErrTransactionUnavailable, fmt.Sprintf(format, args...))
}

// transactionDemand is the non-lossy, per-registration transaction requirement
// of a single syncer. It is intentionally NOT flattened/merged across syncers:
// the address, from-block, topic0 set and target finality of every registration
// are preserved so distinct requirements are never conflated.
type transactionDemand struct {
	SyncerID  string
	Address   common.Address
	FromBlock uint64
	ToBlock   aggkittypes.BlockNumberFinality
	Topic0    []common.Hash
}

// buildTransactionDemands derives one demand per (registration, address) from
// every registered syncer whose TransactionRequirements are enabled. The
// from-block is max(syncer.FromBlock, TransactionRequirements.FromBlock) as the
// transaction frontier can never be before the log frontier.
//
// It reads the immutable snapshot exposed by the syncer config set (which deep
// copies every nested slice) so a caller mutating a registered config after
// registration can never corrupt the derived demands.
func (dh *EVMMultidownloader) buildTransactionDemands() []transactionDemand {
	configs := dh.syncersConfig.SyncerConfigs()
	demands := make([]transactionDemand, 0)
	for _, cfg := range configs {
		tr := cfg.TransactionRequirements
		if !tr.Enabled {
			continue
		}
		fromBlock := cfg.FromBlock
		if tr.FromBlock > fromBlock {
			fromBlock = tr.FromBlock
		}
		for _, addr := range cfg.ContractAddresses {
			demands = append(demands, transactionDemand{
				SyncerID:  cfg.SyncerID,
				Address:   addr,
				FromBlock: fromBlock,
				ToBlock:   cfg.ToBlock,
				Topic0:    append([]common.Hash(nil), tr.Topic0...),
			})
		}
	}
	sort.Slice(demands, func(i, j int) bool {
		if demands[i].Address != demands[j].Address {
			return demands[i].Address.Hex() < demands[j].Address.Hex()
		}
		if demands[i].FromBlock != demands[j].FromBlock {
			return demands[i].FromBlock < demands[j].FromBlock
		}
		return demands[i].SyncerID < demands[j].SyncerID
	})
	return demands
}

// transactionsEnabled reports whether the transaction provider/worker are
// enabled. It is only mutated during Initialize (before Start) and is therefore
// safe to read without holding the mutex.
func (dh *EVMMultidownloader) transactionsEnabled() bool {
	return dh.requiresTransactions && dh.transactionStorage != nil
}

// TransactionByLog resolves the full canonical transaction that produced the
// given log. It is the optional aggkittypes.TransactionProvider implementation.
//
// Behavior:
//   - When transactions are disabled it returns errTransactionProviderDisabled.
//   - A cache hit is validated against the log (block hash, block number and
//     tx index) and returned without any RPC.
//   - A covered log whose transaction is not stored is fetched once over RPC
//     (deduplicated through the shared flight) and validated against the exact
//     log before being saved.
//   - aggkittypes.ErrTransactionInclusionMismatch is returned only for a genuine
//     inclusion mismatch (the log's block is stale/reorged). Null/pending RPC
//     responses, malformed payloads and uncovered logs are reported as ordinary
//     (retryable) aggkittypes.ErrTransactionUnavailable errors so callers retry
//     rather than refetch.
func (dh *EVMMultidownloader) TransactionByLog(ctx context.Context,
	lg ethtypes.Log) (*aggkittypes.RPCTransaction, error) {
	if !dh.transactionsEnabled() {
		return nil, errTransactionProviderDisabled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lg.BlockHash == (common.Hash{}) {
		return nil, errTransactionUnavailable("log has no block hash")
	}
	if lg.TxHash == (common.Hash{}) {
		return nil, errTransactionUnavailable("log has no transaction hash")
	}

	// Cache hit: a stored canonical transaction is validated against the log.
	cached, err := dh.transactionStorage.GetTransaction(nil, lg.TxHash, lg.BlockHash)
	if err != nil {
		return nil, fmt.Errorf("TransactionByLog: cannot read cached transaction %s: %w", lg.TxHash.Hex(), err)
	}
	if cached != nil {
		if err := dh.validateTransactionInclusion(cached, &lg); err != nil {
			return nil, err
		}
		return cached, nil
	}

	// Require an actually covered canonical source log; a fabricated log can
	// never force the provider to resolve an unsupported range.
	if err := dh.checkLogBlockCoverage(&lg); err != nil {
		return nil, err
	}

	// The whole resolve/validate/revalidate/save runs inside the shared flight,
	// keyed by (tx hash, expected block hash). Waiters that join while the save
	// is still running share the same request, and a re-inclusion of the same tx
	// hash under a different block hash can never reuse (poison) the old flight.
	key := txFlightKey{txHash: lg.TxHash, blockHash: lg.BlockHash}
	res, err := dh.txInflight.do(ctx, key, func(runCtx context.Context) (*aggkittypes.RPCTransaction, error) {
		return dh.fetchAndSaveTransaction(runCtx, &lg)
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errTransactionUnavailable("RPC returned no transaction for %s", lg.TxHash.Hex())
	}
	// Each waiter validates the shared result against its OWN log (including its
	// own log/transaction index), so a corrupted request can never mark the
	// transaction ready for a different caller.
	if err := dh.validateTransactionInclusion(res, &lg); err != nil {
		return nil, err
	}
	return res, nil
}

// checkLogBlockCoverage verifies that the log's block is covered by the syncer
// state AND that the exact source log is stored canonically. It prevents a
// fabricated (never downloaded) log from triggering an unsupported transaction
// RPC.
//
//   - A block/address that is not covered yet (or an unknown address) is a
//     benign, retryable aggkittypes.ErrTransactionUnavailable.
//   - A stored canonical block whose hash differs from the log is stale/reorged
//     and reported as aggkittypes.ErrTransactionInclusionMismatch.
//   - A covered block that disappeared (rolled back), or whose source log was
//     moved to the reorged tables, is reported as
//     aggkittypes.ErrTransactionInclusionMismatch instead of retrying the same
//     stale log forever.
func (dh *EVMMultidownloader) checkLogBlockCoverage(lg *ethtypes.Log) error {
	// Verify the state's full single-block/address coverage under the mutex.
	dh.mutex.Lock()
	covered := dh.state != nil &&
		dh.state.IsAvailable(mdrtypes.NewLogQuery(lg.BlockNumber, lg.BlockNumber, []common.Address{lg.Address}))
	dh.mutex.Unlock()

	header, _, err := dh.storage.GetBlockHeaderByNumber(nil, lg.BlockNumber)
	if err != nil {
		return fmt.Errorf("TransactionByLog: cannot read block %d: %w", lg.BlockNumber, err)
	}
	// A stored canonical block with a different hash means the log is stale.
	if header != nil && header.Hash != lg.BlockHash {
		return fmt.Errorf("%w: log block %d stored hash %s != log hash %s",
			aggkittypes.ErrTransactionInclusionMismatch, lg.BlockNumber, header.Hash.Hex(), lg.BlockHash.Hex())
	}
	if !covered {
		if header == nil && dh.blockWasReorged(lg.BlockNumber, lg.BlockHash) {
			return fmt.Errorf("%w: log block %d/%s was reorged away",
				aggkittypes.ErrTransactionInclusionMismatch, lg.BlockNumber, lg.BlockHash.Hex())
		}
		return errTransactionUnavailable("log block %d address %s is not covered yet",
			lg.BlockNumber, lg.Address.Hex())
	}
	// Covered by the state: the source block must still be stored...
	if header == nil {
		return fmt.Errorf("%w: covered log block %d is no longer stored (rolled back)",
			aggkittypes.ErrTransactionInclusionMismatch, lg.BlockNumber)
	}
	// ...and the exact source log must exist canonically.
	found, err := dh.storedLogMatches(lg)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	if dh.blockWasReorged(lg.BlockNumber, lg.BlockHash) {
		return fmt.Errorf("%w: source log block %d/%s was reorged away",
			aggkittypes.ErrTransactionInclusionMismatch, lg.BlockNumber, lg.BlockHash.Hex())
	}
	return errTransactionUnavailable("no canonical source log for tx %s at block %d/%s",
		lg.TxHash.Hex(), lg.BlockNumber, lg.BlockHash.Hex())
}

// storedLogMatches reports whether the exact source log (address, tx hash, block
// hash, log index and transaction index) is stored canonically for its block.
func (dh *EVMMultidownloader) storedLogMatches(lg *ethtypes.Log) (bool, error) {
	logs, err := dh.storage.GetEthLogs(nil,
		mdrtypes.NewLogQuery(lg.BlockNumber, lg.BlockNumber, []common.Address{lg.Address}))
	if err != nil {
		return false, fmt.Errorf("TransactionByLog: cannot read logs for block %d: %w", lg.BlockNumber, err)
	}
	for i := range logs {
		stored := logs[i]
		if stored.TxHash == lg.TxHash && stored.BlockHash == lg.BlockHash &&
			stored.Index == lg.Index && stored.TxIndex == lg.TxIndex {
			return true, nil
		}
	}
	return false, nil
}

// blockWasReorged reports whether the (blockNumber, blockHash) source was moved
// to the reorged tables. It is best-effort: a storage error is logged and
// treated as "not reorged" so a benign storage hiccup does not turn into a
// spurious mismatch.
func (dh *EVMMultidownloader) blockWasReorged(blockNumber uint64, blockHash common.Hash) bool {
	_, reorged, err := dh.storage.GetBlockReorgedReorgID(nil, blockNumber, blockHash)
	if err != nil {
		dh.log.Warnf("TransactionByLog: cannot check whether block %d/%s was reorged: %v",
			blockNumber, blockHash.Hex(), err)
		return false
	}
	return reorged
}

// revalidateLogBlock rechecks the canonical block right before saving so an
// in-flight fetch cannot resurrect an orphan after a reorg. A block that
// disappeared (previously covered) is a mismatch, not an "unavailable".
func (dh *EVMMultidownloader) revalidateLogBlock(blockNumber uint64, blockHash common.Hash) error {
	header, _, err := dh.storage.GetBlockHeaderByNumber(nil, blockNumber)
	if err != nil {
		return fmt.Errorf("TransactionByLog: cannot revalidate block %d: %w", blockNumber, err)
	}
	if header == nil {
		return fmt.Errorf("%w: block %d disappeared before saving the transaction",
			aggkittypes.ErrTransactionInclusionMismatch, blockNumber)
	}
	if header.Hash != blockHash {
		return fmt.Errorf("%w: block %d stored hash %s changed to %s before saving the transaction",
			aggkittypes.ErrTransactionInclusionMismatch, blockNumber, blockHash.Hex(), header.Hash.Hex())
	}
	return nil
}

// validateTransactionSource checks that a resolved transaction matches the
// expected canonical inclusion (tx hash, block hash and block number) without
// looking at any specific log. Null/pending transactions and malformed
// quantities are reported as ordinary (retryable) errors; a genuine
// disagreement on the mined inclusion is reported as
// aggkittypes.ErrTransactionInclusionMismatch.
func (dh *EVMMultidownloader) validateTransactionSource(tx *aggkittypes.RPCTransaction,
	txHash common.Hash, blockNumber uint64, blockHash common.Hash) error {
	if tx == nil {
		return errTransactionUnavailable("nil transaction for %s", txHash.Hex())
	}
	if tx.Hash() != txHash {
		return errTransactionUnavailable("transaction hash %s does not match requested %s",
			tx.HashRaw, txHash.Hex())
	}
	minedHash, ok := tx.BlockHash()
	if !ok {
		return errTransactionUnavailable("transaction %s is pending (no block hash)", tx.HashRaw)
	}
	if minedHash != blockHash {
		return fmt.Errorf("%w: transaction %s mined in block %s but expected %s",
			aggkittypes.ErrTransactionInclusionMismatch, tx.HashRaw, minedHash.Hex(), blockHash.Hex())
	}
	minedNumber, ok := tx.BlockNumber()
	if !ok {
		return errTransactionUnavailable("transaction %s has no/malformed block number", tx.HashRaw)
	}
	if minedNumber != blockNumber {
		return fmt.Errorf("%w: transaction %s mined in block %d but expected %d",
			aggkittypes.ErrTransactionInclusionMismatch, tx.HashRaw, minedNumber, blockNumber)
	}
	return nil
}

// validateTransactionInclusion checks that a resolved transaction is mined and
// exactly matches the log that requested it, including the log/transaction
// index. Null/pending transactions and malformed quantities are reported as
// ordinary (retryable) errors; a genuine disagreement on the mined inclusion is
// reported as aggkittypes.ErrTransactionInclusionMismatch.
func (dh *EVMMultidownloader) validateTransactionInclusion(tx *aggkittypes.RPCTransaction,
	lg *ethtypes.Log) error {
	if err := dh.validateTransactionSource(tx, lg.TxHash, lg.BlockNumber, lg.BlockHash); err != nil {
		return err
	}
	txIndex, ok := tx.TransactionIndex()
	if !ok {
		return errTransactionUnavailable("transaction %s has no/malformed transaction index", tx.HashRaw)
	}
	if txIndex != uint64(lg.TxIndex) {
		return fmt.Errorf("%w: transaction %s index %d does not match log index %d",
			aggkittypes.ErrTransactionInclusionMismatch, tx.HashRaw, txIndex, lg.TxIndex)
	}
	return nil
}

// fetchAndSaveTransaction is the shared flight body. It re-checks the cache
// (another flight may have stored the row), performs the RPC call, validates
// the payload against the FULL expected canonical inclusion (hash, block hash,
// block number AND transaction index), revalidates the canonical block and
// persists the transaction before the flight is deleted. Keeping the save
// inside the flight guarantees a caller arriving after the RPC response but
// before the save completes shares the same single request.
//
// The expected log is passed as a whole (not just hash/number/block hash) so
// the mined transaction index is validated BEFORE any row is written. An RPC
// that reports the wrong index is a genuine inclusion mismatch and is returned
// as aggkittypes.ErrTransactionInclusionMismatch without saving or marking the
// availability row as complete, so a bad payload can never poison the cache.
func (dh *EVMMultidownloader) fetchAndSaveTransaction(ctx context.Context,
	expectedLog *ethtypes.Log) (*aggkittypes.RPCTransaction, error) {
	txHash := expectedLog.TxHash
	blockNumber := expectedLog.BlockNumber
	blockHash := expectedLog.BlockHash
	cached, err := dh.transactionStorage.GetTransaction(nil, txHash, blockHash)
	if err != nil {
		return nil, fmt.Errorf("TransactionByLog: cannot read cached transaction %s: %w", txHash.Hex(), err)
	}
	if cached != nil {
		// A cached row is only reusable when it exactly matches the expected
		// canonical inclusion, including the transaction index. A row written by
		// older code with a wrong index is surfaced as a mismatch, never
		// silently returned as if it were the expected transaction.
		if err := dh.validateTransactionInclusion(cached, expectedLog); err != nil {
			return nil, err
		}
		return cached, nil
	}

	res, err := dh.rpcTransactionByHash(ctx, txHash)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errTransactionUnavailable("RPC returned no transaction for %s", txHash.Hex())
	}
	if err := res.Validate(); err != nil {
		return nil, errTransactionUnavailable("invalid transaction %s: %v", txHash.Hex(), err)
	}
	// Validate the full canonical inclusion (hash, block hash, block number and
	// transaction index) BEFORE persisting: an RPC that reports the wrong mined
	// index must fail here so no wrong row is ever inserted.
	if err := dh.validateTransactionInclusion(res, expectedLog); err != nil {
		return nil, err
	}
	if err := dh.revalidateLogBlock(blockNumber, blockHash); err != nil {
		return nil, err
	}
	// SaveTransactions revalidates the canonical (block_number, block_hash)
	// atomically and never resurrects an orphan.
	if err := dh.transactionStorage.SaveTransactions(nil, []aggkittypes.RPCTransaction{*res}); err != nil {
		return nil, fmt.Errorf("TransactionByLog: cannot save transaction %s: %w", txHash.Hex(), err)
	}
	return res, nil
}

// rpcTransactionByHash performs a single eth_getTransactionByHash call bounded
// by the configured request timeout. A JSON null (unknown/pending tx) is
// returned as (nil, nil). Malformed payloads are reported as
// aggkittypes.ErrTransactionUnavailable (retryable), never as a mismatch.
func (dh *EVMMultidownloader) rpcTransactionByHash(ctx context.Context,
	txHash common.Hash) (*aggkittypes.RPCTransaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dh.rpcClient == nil {
		return nil, errTransactionUnavailable("no RPC client configured")
	}
	timeout := dh.txRequestTimeout
	if timeout <= 0 {
		timeout = defaultTransactionRequestTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var raw json.RawMessage
	if err := dh.rpcClient.CallContext(callCtx, &raw, rpcMethodGetTransactionByHash, txHash); err != nil {
		return nil, fmt.Errorf("eth_getTransactionByHash(%s): %w", txHash.Hex(), err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var tx aggkittypes.RPCTransaction
	if err := json.Unmarshal(raw, &tx); err != nil {
		return nil, errTransactionUnavailable("cannot decode transaction %s: %v", txHash.Hex(), err)
	}
	return &tx, nil
}

// transactionWorkerPeriod returns the background scan period. It inherits the
// reorg-check period when configured and otherwise falls back to a small
// default, so there is no unbounded busy-retry loop.
func (dh *EVMMultidownloader) transactionWorkerPeriod() time.Duration {
	if dh.cfg.PeriodToCheckReorgs.Duration > 0 {
		return dh.cfg.PeriodToCheckReorgs.Duration
	}
	return defaultTransactionWorkerPeriod
}

// txMaxConcurrency returns the maximum number of concurrent transaction RPC
// requests. It reuses the configured block-header parallelism so the transaction
// path never opens more sockets than the rest of the multidownloader.
func (dh *EVMMultidownloader) txMaxConcurrency() int {
	limit := dh.cfg.MaxParallelBlockHeaderRetrieval
	if limit <= 0 {
		limit = 1
	}
	return limit
}

// startTransactionWorker starts the background backfill worker. The worker is
// tracked by dh.workerWG (a run-scoped group that does NOT include the run-loop
// goroutine) and stops when the run context is canceled. The shared flight base
// context is derived from the run context so all in-flight RPCs are canceled
// and joined when the run stops.
func (dh *EVMMultidownloader) startTransactionWorker(ctx context.Context) {
	if !dh.transactionsEnabled() {
		return
	}
	dh.txInflight.setBaseContext(ctx)
	dh.workerWG.Add(1)
	go func() {
		defer dh.workerWG.Done()
		dh.runTransactionWorker(ctx)
	}()
}

// runTransactionWorker periodically scans the covered log ranges and backfills
// missing canonical transactions. It also wakes immediately when new logs are
// committed (notifyTransactionWorker).
func (dh *EVMMultidownloader) runTransactionWorker(ctx context.Context) {
	ticker := time.NewTicker(dh.transactionWorkerPeriod())
	defer ticker.Stop()
	dh.scanMissingTransactions(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			dh.scanMissingTransactions(ctx)
		case <-dh.txNotify:
			dh.scanMissingTransactions(ctx)
		}
	}
}

// notifyTransactionWorker wakes the background worker after new logs have been
// committed. It never blocks and is a no-op when transactions are disabled.
func (dh *EVMMultidownloader) notifyTransactionWorker() {
	if !dh.transactionsEnabled() {
		return
	}
	select {
	case dh.txNotify <- struct{}{}:
	default:
	}
}

// snapshotSyncedSegment returns a copy of the synced block range currently
// registered for the given address, taken under the multidownloader mutex.
func (dh *EVMMultidownloader) snapshotSyncedSegment(addr common.Address) (aggkitcommon.BlockRange, bool) {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	if dh.state == nil {
		return aggkitcommon.BlockRange{}, false
	}
	segment, ok := dh.state.Synced.GetByContract(addr)
	if !ok {
		return aggkitcommon.BlockRange{}, false
	}
	return segment.BlockRange, true
}

// scanMissingTransactions performs one bounded background scan. It only looks
// at ranges that are already covered (present in the in-memory synced state),
// so an uncovered query is never treated as complete.
//
// It scans page by page and fetches each page with a fixed worker pool, so the
// memory used is O(page size) and the number of goroutines is O(max
// concurrency), never O(history).
func (dh *EVMMultidownloader) scanMissingTransactions(ctx context.Context) {
	if !dh.transactionsEnabled() {
		return
	}
	demands := dh.transactionDemands
	if len(demands) == 0 {
		return
	}
	pageSize := uint64(dh.cfg.BlockChunkSize)
	if pageSize == 0 {
		pageSize = 1
	}
	for _, demand := range demands {
		if ctx.Err() != nil {
			return
		}
		toBlock, err := dh.blockNotifierManager.GetCurrentBlockNumber(ctx, demand.ToBlock)
		if err != nil {
			dh.log.Warnf("transaction worker: cannot resolve ToBlock %s for syncer %s: %v",
				demand.ToBlock.String(), demand.SyncerID, err)
			continue
		}
		synced, ok := dh.snapshotSyncedSegment(demand.Address)
		if !ok || synced.IsEmpty() {
			continue
		}
		scanRange := synced.Intersect(aggkitcommon.NewBlockRange(demand.FromBlock, toBlock))
		if scanRange.IsEmpty() {
			continue
		}
		for pageStart := scanRange.FromBlock; pageStart <= scanRange.ToBlock; {
			if ctx.Err() != nil {
				return
			}
			pageEnd := pageStart + pageSize - 1
			// Guard against uint64 overflow: if the addition wrapped, fall back
			// to the end of the scan range.
			if pageEnd < pageStart {
				pageEnd = scanRange.ToBlock
			}
			if pageEnd > scanRange.ToBlock {
				pageEnd = scanRange.ToBlock
			}
			dh.scanAndFetchPage(ctx, demand, pageStart, pageEnd)
			if pageEnd >= scanRange.ToBlock {
				break
			}
			pageStart = pageEnd + 1
		}
	}
}

// collectMissingTransactionsForPage returns the logs of one page that still
// miss a canonical transaction, deduplicated by (tx hash, block hash) within the
// page. Duplicates across pages are naturally avoided because the transaction
// row (or the shared flight) is reused.
func (dh *EVMMultidownloader) collectMissingTransactionsForPage(_ context.Context,
	demand transactionDemand, fromBlock, toBlock uint64) []ethtypes.Log {
	query := mdrtypes.NewLogQuery(fromBlock, toBlock, []common.Address{demand.Address})
	logs, err := dh.storage.GetEthLogs(nil, query)
	if err != nil {
		dh.log.Warnf("transaction worker: cannot read logs for %s [%d-%d]: %v",
			demand.Address.Hex(), fromBlock, toBlock, err)
		return nil
	}
	result := make([]ethtypes.Log, 0, len(logs))
	seen := make(map[string]struct{}, len(logs))
	for _, lg := range logs {
		if lg.BlockNumber < demand.FromBlock {
			continue
		}
		if !mdrtypes.Topic0Match(demand.Topic0, lg.Topics) {
			continue
		}
		key := lg.TxHash.Hex() + ":" + lg.BlockHash.Hex()
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		stored, err := dh.transactionStorage.GetTransaction(nil, lg.TxHash, lg.BlockHash)
		if err != nil {
			dh.log.Warnf("transaction worker: cannot read transaction %s: %v", lg.TxHash.Hex(), err)
			continue
		}
		if stored != nil {
			continue
		}
		result = append(result, lg)
	}
	return result
}

// scanAndFetchPage resolves the missing transactions of one page with a fixed
// pool of at most txMaxConcurrency() workers. Each distinct transaction is
// fetched at most once thanks to the shared flight (and the shared HTTP
// limiter). A transaction failure never blocks log ingestion: it is only logged
// and retried on the next scan.
func (dh *EVMMultidownloader) scanAndFetchPage(ctx context.Context,
	demand transactionDemand, fromBlock, toBlock uint64) {
	missing := dh.collectMissingTransactionsForPage(ctx, demand, fromBlock, toBlock)
	if len(missing) == 0 {
		return
	}
	limit := dh.txMaxConcurrency()
	if limit > len(missing) {
		limit = len(missing)
	}
	queue := make(chan ethtypes.Log)
	var wg sync.WaitGroup
	for i := 0; i < limit; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for lg := range queue {
				if ctx.Err() != nil {
					return
				}
				if _, err := dh.resolveAndCacheTransaction(ctx, lg.TxHash, &lg); err != nil {
					dh.log.Warnf("transaction worker: cannot resolve transaction %s (block %d): %v",
						lg.TxHash.Hex(), lg.BlockNumber, err)
				}
			}
		}()
	}
	for i := range missing {
		if ctx.Err() != nil {
			break
		}
		select {
		case queue <- missing[i]:
		case <-ctx.Done():
		}
	}
	close(queue)
	wg.Wait()
}

// resolveAndCacheTransaction resolves a single log's transaction through the
// shared flight. The actual fetch/validate/save happens inside the flight; this
// wrapper only validates the shared result against the given log.
func (dh *EVMMultidownloader) resolveAndCacheTransaction(ctx context.Context, txHash common.Hash,
	expectedLog *ethtypes.Log) (*aggkittypes.RPCTransaction, error) {
	if expectedLog == nil {
		return nil, errTransactionUnavailable("resolveAndCacheTransaction: nil log for %s", txHash.Hex())
	}
	key := txFlightKey{txHash: txHash, blockHash: expectedLog.BlockHash}
	res, err := dh.txInflight.do(ctx, key, func(runCtx context.Context) (*aggkittypes.RPCTransaction, error) {
		return dh.fetchAndSaveTransaction(runCtx, expectedLog)
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errTransactionUnavailable("RPC returned no transaction for %s", txHash.Hex())
	}
	if err := dh.validateTransactionInclusion(res, expectedLog); err != nil {
		return nil, err
	}
	return res, nil
}

// txFlightKey identifies a canonical transaction inclusion. Keying on the
// block hash as well as the tx hash guarantees that a re-inclusion of the same
// transaction hash in a different block never reuses (poisons) an older flight.
type txFlightKey struct {
	txHash    common.Hash
	blockHash common.Hash
}

// transactionInflight is a small singleflight keyed by canonical inclusion. The
// whole resolve/validate/save runs inside the flight so waiters arriving at any
// point share the same single RPC request. A shared RPC call runs under a base
// context owned by the multidownloader lifetime, so a canceled requester never
// aborts the fetch for the other waiters; each waiter still returns promptly
// when its own context is done. A shared HTTP limiter bounds the number of
// concurrent RPCs for both foreground and background callers, and the flight
// goroutines are tracked so the lifetime can join them on stop.
//
// Admission and shutdown are serialized by mu: a flight is inserted into calls
// AND registered in wg under the same critical section, and wait() sets closing
// under that same mutex before it blocks on wg. As a result no wg.Add can ever
// race wg.Wait (which would be a WaitGroup misuse and could let a late flight
// return after the stopped state was published), and no new flight can be
// admitted once a run has sealed the group.
type transactionInflight struct {
	mu      sync.Mutex
	calls   map[txFlightKey]*transactionCall
	limiter chan struct{}
	wg      sync.WaitGroup

	baseCtx context.Context
	// closing is set by wait() to seal the group for the current run and reset
	// by setBaseContext when the next run reopens admission. It is always
	// accessed under mu.
	closing bool
}

type transactionCall struct {
	done chan struct{}
	// cancel cancels this flight's own context. The context is derived from the
	// base context, NOT from any single waiter, so canceling one waiter can
	// never abort the fetch its co-waiters still need. wait() cancels it so a
	// flight admitted before the current run (e.g. under the Initialize
	// context) is drained too, instead of waiting for its request timeout.
	cancel context.CancelFunc
	tx     *aggkittypes.RPCTransaction
	err    error
}

func newTransactionInflight(ctx context.Context, maxConcurrent int) *transactionInflight {
	if ctx == nil {
		ctx = context.Background()
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &transactionInflight{
		calls:   make(map[txFlightKey]*transactionCall),
		limiter: make(chan struct{}, maxConcurrent),
		baseCtx: ctx,
	}
}

// setBaseContext installs the base context of a new run and reopens admission
// for it. It is only called by the Start/Initialize lifecycle, i.e. after the
// previous run's wait() has joined every flight, so reopening cannot race a late
// wg.Add from a previous run.
func (g *transactionInflight) setBaseContext(ctx context.Context) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	g.baseCtx = ctx
	g.closing = false
}

// wait blocks until every shared flight goroutine has returned. It is used when
// the multidownloader lifetime stops so no flight is leaked past Stop. It first
// seals admission (so no new flight can be registered while it joins) and then
// cancels every in-flight flight's own context, so a flight admitted before the
// current run is drained immediately rather than waiting for its request
// timeout.
func (g *transactionInflight) wait() {
	g.mu.Lock()
	g.closing = true
	for _, call := range g.calls {
		call.cancel()
	}
	g.mu.Unlock()
	g.wg.Wait()
}

func (g *transactionInflight) do(ctx context.Context, key txFlightKey,
	fn func(context.Context) (*aggkittypes.RPCTransaction, error)) (*aggkittypes.RPCTransaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	// Reject before creating any flight when the group is sealed or the base
	// context is already done: a caller arriving during/after Stop must get an
	// explicit error instead of a flight that would outlive the stopped state.
	if g.closing {
		baseErr := g.baseCtx.Err()
		g.mu.Unlock()
		if baseErr != nil {
			return nil, baseErr
		}
		return nil, errTransactionUnavailable("transaction inflight is stopped")
	}
	if err := g.baseCtx.Err(); err != nil {
		g.mu.Unlock()
		return nil, err
	}
	if call, ok := g.calls[key]; ok {
		g.mu.Unlock()
		return call.wait(ctx)
	}
	runCtx, cancel := context.WithCancel(g.baseCtx)
	call := &transactionCall{done: make(chan struct{}), cancel: cancel}
	g.calls[key] = call
	// Register in wg inside the SAME critical section as the map insert, so
	// wait() (which sets closing under mu before wg.Wait) can never observe the
	// counter at zero and race a late Add.
	g.wg.Add(1)
	g.mu.Unlock()

	go func() {
		defer g.wg.Done()
		// The flight owns its context: it is canceled when the flight ends (or
		// by wait() when the lifetime stops). It is never derived from a single
		// waiter, so one waiter giving up does not cancel its co-waiters.
		defer cancel()
		var tx *aggkittypes.RPCTransaction
		var err error
		if g.acquire(runCtx) {
			tx, err = fn(runCtx)
			g.release()
		} else {
			err = runCtx.Err()
		}
		g.mu.Lock()
		call.tx = tx
		call.err = err
		if g.calls[key] == call {
			delete(g.calls, key)
		}
		g.mu.Unlock()
		close(call.done)
	}()

	return call.wait(ctx)
}

// acquire takes one HTTP slot for the shared flight. It returns false when the
// lifetime context is canceled before a slot becomes available. It is called
// outside the flight mutex.
func (g *transactionInflight) acquire(ctx context.Context) bool {
	select {
	case g.limiter <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (g *transactionInflight) release() {
	<-g.limiter
}

func (c *transactionCall) wait(ctx context.Context) (*aggkittypes.RPCTransaction, error) {
	select {
	case <-c.done:
		return c.tx, c.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
