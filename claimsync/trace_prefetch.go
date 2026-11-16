package claimsync

import (
	"context"
	"errors"
	"fmt"
	"sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// ClaimTracePrefetchConcurrency is the fixed, bounded number of concurrent
// debug_traceTransaction requests used to prefetch the root call traces of the
// ordinary/pre-Etrog ClaimEvent logs of one downloaded log range.
//
// It is hardwired: no operator configuration flag is added, so the exact
// concurrency is a fixed constant.
const ClaimTracePrefetchConcurrency = 8

// ErrTraceUnavailable marks a root call trace that was not published for the
// current downloaded log range (generation). It is never satisfied by reusing a
// stale trace: the caller must re-run the logs enricher (which refetches the
// whole range) instead. A missing trace is never reported as a nil success.
var ErrTraceUnavailable = errors.New("claimsync: root call trace unavailable for the current log range")

// traceKey identifies a required root call trace within a single downloaded log
// range. The expected block hash is part of the key so that a reorg which
// re-includes the same transaction hash in a different block can never reuse a
// trace resolved for the old inclusion.
type traceKey struct {
	BlockHash common.Hash
	TxHash    common.Hash
}

// traceTarget is one distinct root trace request for the current range.
type traceTarget struct {
	key      traceKey
	blockNum uint64
}

// tracePrefetchClient is the minimal backend the resolver needs: the generic
// RPC caller (debug_traceTransaction) plus canonical header lookups used to
// revalidate that the log's block is still canonical before trusting a trace.
type tracePrefetchClient interface {
	aggkittypes.RPCClienter
	CustomHeaderByNumber(
		ctx context.Context, number *aggkittypes.BlockNumberFinality,
	) (*aggkittypes.BlockHeader, error)
}

// TraceResolver prefetches, with a bounded pool, the root call traces of a
// single downloaded log range (generation) and publishes them atomically so the
// claim handlers can resolve the trace for the exact original log without a
// per-log synchronous RPC call.
//
// The published snapshot is immutable and replaced wholesale on every
// successful enrichment; entries are never reused across generations. A missing
// lookup (for example after a reorg changed the log's block hash) is reported as
// an explicit error so the range is refetched: the legacy synchronous path is
// never consulted as a fallback and a nil trace is never reported as success.
type TraceResolver struct {
	client  tracePrefetchClient
	bridge  common.Address
	logger  aggkitcommon.Logger
	workers int

	mu    sync.RWMutex
	roots map[traceKey]*Call
}

// NewTraceResolver creates a resolver that prefetches root call traces for the
// given bridge contract using at most workers concurrent CallContext requests.
// A workers value below 1 is clamped to 1.
func NewTraceResolver(
	client tracePrefetchClient,
	bridge common.Address,
	logger aggkitcommon.Logger,
	workers int,
) *TraceResolver {
	if workers < 1 {
		workers = 1
	}
	return &TraceResolver{
		client:  client,
		bridge:  bridge,
		logger:  logger,
		workers: workers,
		roots:   make(map[traceKey]*Call),
	}
}

// ResolveRoot returns the prefetched root call for the exact original log. It
// returns ErrTraceUnavailable (which also matches
// aggkittypes.ErrTransactionInclusionMismatch so the downloader refetches the
// whole range) when the trace was not published for the current generation.
func (r *TraceResolver) ResolveRoot(l types.Log) (*Call, error) {
	if r == nil {
		return nil, nil
	}
	key := traceKey{BlockHash: l.BlockHash, TxHash: l.TxHash}
	r.mu.RLock()
	root, ok := r.roots[key]
	r.mu.RUnlock()
	if !ok || root == nil {
		return nil, &traceUnavailableError{key: key}
	}
	return root, nil
}

// traceUnavailableError is the explicit error returned when a prefetched root
// call is missing for the current generation.
type traceUnavailableError struct {
	key traceKey
}

func (e *traceUnavailableError) Error() string {
	return fmt.Sprintf("%s: blockHash=%s txHash=%s",
		ErrTraceUnavailable.Error(), e.key.BlockHash.Hex(), e.key.TxHash.Hex())
}

// Is makes a miss behave both as an explicit "unavailable" marker and as an
// inclusion mismatch, so the downloader refetches the whole range (re-running
// the enricher) instead of retrying the same stale log or reusing a stale trace.
func (e *traceUnavailableError) Is(target error) bool {
	return target == ErrTraceUnavailable || target == aggkittypes.ErrTransactionInclusionMismatch
}

// EnrichLogs is the logs enricher installed on the multidownloader-backed claim
// syncer. For the current downloaded log range it revalidates that the logs'
// blocks are still canonical, prefetches the root call traces of the ordinary
// and pre-Etrog ClaimEvent logs with a bounded pool, and publishes them
// atomically only when every needed request succeeded.
//
// The canonical revalidation runs twice: once before the trace RPCs and once
// after every worker has joined, immediately before publishing. Each phase uses
// its own bounded worker pool, so the header lookups overlap too; every phase
// reads exactly one header per distinct block, so a range costs 2 * distinct
// block lookups on top of the trace RPCs. A trace result carries no block hash,
// so the second read is what ties the published snapshot to the blocks the
// range was revalidated against.
//
// Failure behavior:
//   - a request error aborts without publishing (the downloader retries it);
//   - a download-context cancellation aborts without publishing, returns the
//     context error and fully joins the pool (no RPC call leaks);
//   - a log whose block is no longer canonical, before or after the trace RPCs,
//     returns aggkittypes.ErrTransactionInclusionMismatch so the whole range is
//     refetched;
//   - any failure purges the published snapshot, so a trace from an earlier
//     generation can never be resolved for the failed range.
func (r *TraceResolver) EnrichLogs(ctx context.Context, logs []types.Log) error {
	if r == nil {
		return nil
	}
	targets := collectTraceTargets(logs)
	if len(targets) == 0 {
		// Publish an empty snapshot so a stale entry from a previous generation
		// can never be resolved for this range.
		r.publish(make(map[traceKey]*Call))
		return nil
	}

	// Pre-validation: reject a range whose blocks are already no longer
	// canonical before spending any trace RPC on it.
	if err := r.revalidateCanonical(ctx, targets); err != nil {
		r.purge()
		return err
	}

	roots := make(map[traceKey]*Call, len(targets))
	if err := r.prefetch(ctx, targets, roots); err != nil {
		r.purge()
		return err
	}
	if err := ctx.Err(); err != nil {
		r.purge()
		return err
	}

	// Post-validation: a reorg can re-include a traced transaction in a
	// different block while its trace is in flight. The trace result carries no
	// block hash, so re-read every required block header after all workers have
	// joined and only then publish; a changed or absent header makes the whole
	// range be refetched instead of publishing a trace tied to a non-canonical
	// block.
	if err := r.revalidateCanonical(ctx, targets); err != nil {
		r.purge()
		return err
	}

	r.publish(roots)
	return nil
}

// collectTraceTargets returns the distinct root trace requests for the logs
// that are handled by the ordinary/pre-Etrog claim handlers. Logs without
// topics and logs of other events (detailed/unset/set/outdated) are ignored, so
// no unnecessary or failing trace is requested for them.
func collectTraceTargets(logs []types.Log) []traceTarget {
	seen := make(map[traceKey]struct{})
	targets := make([]traceTarget, 0, len(logs))
	for _, l := range logs {
		if len(l.Topics) == 0 {
			continue
		}
		if l.Topics[0] != claimEventSignature && l.Topics[0] != claimEventSignaturePreEtrog {
			continue
		}
		key := traceKey{BlockHash: l.BlockHash, TxHash: l.TxHash}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, traceTarget{key: key, blockNum: l.BlockNumber})
	}
	return targets
}

// blockExpectation is one distinct canonical header to re-read: the expected
// hash is the block hash carried by the log(s) of that height in the range.
type blockExpectation struct {
	blockNum uint64
	hash     common.Hash
}

// distinctBlockExpectations folds the targets to exactly one expectation per
// distinct block height. A range carrying two different expected hashes for the
// same height is internally inconsistent and is rejected before any header RPC,
// since no single canonical header could satisfy both. A zero block hash can
// only come from a synthetic log (real FilterLogs results always carry one); the
// downloader's own header check still guards the append, so it is skipped. The
// order is stable (first-seen) so the number of header lookups is deterministic.
func distinctBlockExpectations(targets []traceTarget) ([]blockExpectation, error) {
	seen := make(map[uint64]common.Hash, len(targets))
	order := make([]uint64, 0, len(targets))
	for _, t := range targets {
		if t.key.BlockHash == (common.Hash{}) {
			continue
		}
		if prev, ok := seen[t.blockNum]; ok {
			if prev != t.key.BlockHash {
				return nil, fmt.Errorf("claimsync: inconsistent block hash for block %d in one range: %w",
					t.blockNum, aggkittypes.ErrTransactionInclusionMismatch)
			}
			continue
		}
		seen[t.blockNum] = t.key.BlockHash
		order = append(order, t.blockNum)
	}
	out := make([]blockExpectation, 0, len(order))
	for _, num := range order {
		out = append(out, blockExpectation{blockNum: num, hash: seen[num]})
	}
	return out, nil
}

// revalidateCanonical re-reads, with a bounded worker pool, the header of every
// distinct block referenced by the targets and requires it to still have the
// hash carried by the log. The root trace result does not carry a block hash
// itself, so this is what ties a prefetched trace back to the exact log
// inclusion; a changed or missing header is reported as an inclusion mismatch so
// the whole range is refetched.
//
// Exactly one lookup is made per distinct block per call, so running it once
// before the trace RPCs and once after every worker has joined performs two
// header lookups per distinct block. The pool is bounded by
// ClaimTracePrefetchConcurrency, so the header phase can never fan out more
// RPCs than the trace phase. On the first error the remaining work is canceled,
// every worker is joined and that error is returned; a canceled download context
// returns the context error after joining.
func (r *TraceResolver) revalidateCanonical(ctx context.Context, targets []traceTarget) error {
	jobs, err := distinctBlockExpectations(targets)
	if err != nil {
		return err
	}
	return runBoundedPool(ctx, r.headerWorkers(), jobs, func(runCtx context.Context, job blockExpectation) error {
		return r.checkCanonicalHeader(runCtx, job)
	})
}

// checkCanonicalHeader reads one block header and requires it to match the
// expected hash. A transport error stays retryable unless the request context
// was canceled; an absent header is an inclusion mismatch.
func (r *TraceResolver) checkCanonicalHeader(ctx context.Context, job blockExpectation) error {
	header, err := r.client.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(job.blockNum))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, ethereum.NotFound) {
			return fmt.Errorf("claimsync: block %d not found while prefetching traces: %w",
				job.blockNum, aggkittypes.ErrTransactionInclusionMismatch)
		}
		return fmt.Errorf("claimsync: failed to revalidate block %d before trace prefetch: %w", job.blockNum, err)
	}
	if header == nil || header.Hash != job.hash {
		return fmt.Errorf("claimsync: block %d header hash changed while prefetching traces: %w",
			job.blockNum, aggkittypes.ErrTransactionInclusionMismatch)
	}
	return nil
}

// prefetch resolves every target with a bounded worker pool. It writes into out
// only on full success: on the first error it cancels the remaining work,
// joins every worker and returns the error without publishing anything. When
// the download context is canceled it returns the context error after joining.
func (r *TraceResolver) prefetch(ctx context.Context, targets []traceTarget, out map[traceKey]*Call) error {
	var mu sync.Mutex
	return runBoundedPool(ctx, r.workers, targets, func(runCtx context.Context, t traceTarget) error {
		root, err := r.traceRoot(runCtx, t.key.TxHash)
		if err != nil {
			return err
		}
		mu.Lock()
		out[t.key] = root
		mu.Unlock()
		return nil
	})
}

// headerWorkers is the size of the canonical-header revalidation pool. It is
// never larger than the fixed ClaimTracePrefetchConcurrency bound, nor than the
// resolver's configured trace pool, so the header phase can never fan out more
// RPCs than the trace phase. The actual pool is additionally bounded by the
// number of distinct blocks via runBoundedPool.
func (r *TraceResolver) headerWorkers() int {
	workers := r.workers
	if workers > ClaimTracePrefetchConcurrency {
		workers = ClaimTracePrefetchConcurrency
	}
	if workers < 1 {
		workers = 1
	}
	return workers
}

// runBoundedPool runs fn for every item with at most workers concurrent
// goroutines. It returns the first error a worker reports, after canceling the
// remaining work and joining every worker; otherwise it returns the parent
// context error (if the download context was canceled) once every worker has
// joined. runCtx is the per-run context handed to fn so an in-flight RPC is
// canceled together with the pool. The pool is always bounded by the number of
// items.
func runBoundedPool[T any](
	ctx context.Context, workers int, items []T, fn func(context.Context, T) error,
) error {
	if len(items) == 0 {
		return ctx.Err()
	}
	if workers > len(items) {
		workers = len(items)
	}
	if workers < 1 {
		workers = 1
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan T)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)

	recordErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
		cancel()
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-runCtx.Done():
					return
				case item, ok := <-jobs:
					if !ok {
						return
					}
					if err := fn(runCtx, item); err != nil {
						recordErr(err)
						return
					}
				}
			}
		}()
	}

feed:
	for _, item := range items {
		select {
		case <-runCtx.Done():
			break feed
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// traceRoot fetches a single root call through CallContext so the download
// context (and therefore the current generation) cancels an in-flight request.
//
// The decoded root is validated before it can be published: a node that answers
// with a nil error but no call frame (for example a JSON null) would otherwise
// leave the pre-seeded root untouched and be cached as an empty trace forever,
// because once a trace is published the downloader retries the appender rather
// than the enrichment. A legitimate reverted root, and a router fallback whose
// own input is empty but which nests the bridge call, both keep their frame type
// and are accepted unchanged.
func (r *TraceResolver) traceRoot(ctx context.Context, txHash common.Hash) (*Call, error) {
	root := &Call{To: r.bridge}
	callArgs := []any{txHash, tracerCfg{Tracer: callTracerType}}
	if err := r.client.CallContext(ctx, root, DebugTraceTxEndpoint, callArgs...); err != nil {
		return nil, fmt.Errorf("claimsync: %s(%s) failed: %w", DebugTraceTxEndpoint, txHash.Hex(), err)
	}
	if err := validateRootCall(root); err != nil {
		return nil, fmt.Errorf("claimsync: %s(%s): %w", DebugTraceTxEndpoint, txHash.Hex(), err)
	}
	return root, nil
}

// rootCallTypes are the callTracer frame types a usable root call may carry.
// The frame type is the field a real callTracer frame always sets, so an empty
// type means the response decoded into nothing. Reverted frames and
// router/fallback wrappers keep their type and therefore stay valid.
var rootCallTypes = map[string]struct{}{
	CallTypeCall:         {},
	CallTypeCallCode:     {},
	CallTypeDelegateCall: {},
	CallTypeStaticCall:   {},
	CallTypeCreate:       {},
	CallTypeCreate2:      {},
	CallTypeSelfDestruct: {},
}

// validateRootCall rejects a decoded root that cannot be a callTracer frame. It
// deliberately checks the frame type only, so it never rejects a reverted call
// or an empty-input wrapper that still nests the bridge call; the existing claim
// extraction reports ErrNotFound for a reverted-only trace exactly as the legacy
// synchronous path does. A rejection is a retryable error (never an inclusion
// mismatch), so the range is re-enriched instead of caching the empty root.
func validateRootCall(root *Call) error {
	if root == nil {
		return errors.New("empty root call")
	}
	if _, ok := rootCallTypes[root.Type]; !ok {
		return fmt.Errorf("unusable root call (type %q)", root.Type)
	}
	return nil
}

// publish atomically replaces the current snapshot.
func (r *TraceResolver) publish(roots map[traceKey]*Call) {
	r.mu.Lock()
	r.roots = roots
	r.mu.Unlock()
}

// purge drops the current snapshot. It is called on every failed enrichment so
// that no trace published by an earlier generation (for example one resolved
// before a reorg changed the block hash) can be served for the failed range.
func (r *TraceResolver) purge() {
	r.publish(make(map[traceKey]*Call))
}
