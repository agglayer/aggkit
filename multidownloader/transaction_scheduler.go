package multidownloader

// This file implements the H1 INCREMENTAL transaction backfill scheduler.
//
// The original background worker (see evm_multidownloader_transactions.go) is
// woken every few seconds and rescans the ENTIRE already covered log history
// page by page, re-loading every relevant log and re-issuing a per-transaction
// GetTransaction cache lookup, even for pages that were fully enriched on a
// previous tick. That repeated SQL/CPU work competes with the foreground
// consumer (bridgesync/claimsync) for the same SQLite connection.
//
// The incremental scheduler keeps, per IMMUTABLE registration (address +
// from-block frontier + topic0 set + target finality), an in-memory cursor that
// only ever advances over LOG-COVERED pages that are known complete OR have
// been explicitly recorded in a bounded retry queue of failed pages:
//
//   - a page that resolved every missing transaction advances the cursor to
//     pageEnd+1 and is never rescanned on a later tick;
//   - a page that still has unresolved transactions advances the cursor too,
//     but ONLY because its exact (from,to) is retained in the retry queue, so
//     no gap is ever silently lost (no false "complete" class is ever stored);
//   - the cursor is never advanced past a page that is not log-covered.
//
// New logs that get committed extend the covered range; the worker is woken
// (notifyTransactionWorker) and the next plan resumes at frontier (= last
// completed page + 1), so only genuinely new pages are read.
//
// On a reorg the scheduler is rewound from the first reorged block (or fully
// rebuilt) so new canonical logs re-downloaded behind the cursor are never
// skipped: the cursor, not the cached transaction rows, is what is rewound. A
// plan is stamped with the generation it was built under and a plan whose
// generation has been superseded is dropped when its result is committed, so an
// in-flight plan can never advance the cursor or queue a retry after the rewind.
//
// The scheduler holds only O(pending pages) state, is protected by its own
// mutex and never performs I/O while holding it.

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	// maxNewPagesPerScan bounds how many brand new pages a single demand may
	// process in one scan, so a large first replay can never starve the native
	// head (the next tick resumes at the new frontier).
	maxNewPagesPerScan = 4096

	// maxRetryPages bounds the in-memory failed-page retry queue. When it would
	// be exceeded the cursor is rewound to the oldest queued page and the queue
	// is cleared, so memory stays bounded without ever dropping a gap: the
	// rewound region is simply re-scanned (which is at worst recomputation, never
	// data loss or a false "complete").
	maxRetryPages = 256
)

// transactionPageRange is a half-open inclusive block range of one page.
type transactionPageRange struct {
	from uint64
	to   uint64
}

// transactionRetryPage is a page that still has unresolved transactions and is
// therefore kept in the retry queue instead of being forgotten.
type transactionRetryPage struct {
	from     uint64
	to       uint64
	attempts int
}

// transactionSchedule is the per-registration incremental cursor.
type transactionSchedule struct {
	signature string

	initialized bool
	// frontier is the next block to scan. Every block below it is either outside
	// the log-covered range or belongs to a page that is complete / queued for
	// retry.
	frontier uint64

	// retry is the bounded set of failed pages, keyed by page start.
	retry map[uint64]transactionRetryPage
}

// transactionPagePlan is the planned work of one demand for a single scan.
type transactionPagePlan struct {
	// retries are failed pages to re-attempt (processed first).
	retries []transactionPageRange
	// pages are brand new pages, in order, starting at the frontier.
	pages []transactionPageRange
	// generation is the scheduler generation the plan was built under. Every
	// page (retry or brand new) in the plan is stamped with it, and commit
	// rejects a page whose generation no longer matches the scheduler's. This
	// is what stops a plan built before a reorg rewind (or a run reset / demand
	// reconciliation) from advancing the cursor or queueing a retry afterwards.
	generation uint64
}

// transactionScheduler holds one transactionSchedule per registered demand.
type transactionScheduler struct {
	mu        sync.Mutex
	schedules map[string]*transactionSchedule
	// generation is bumped on every reset, reorg rewind and demand
	// reconciliation that removes a registration. A plan records the generation
	// it was built under (see transactionPagePlan.generation) and commit rejects
	// it when the generation has since changed, so a stale plan can never
	// advance the cursor past a reorg or queue a retry in a later epoch.
	generation uint64
}

func newTransactionScheduler() *transactionScheduler {
	return &transactionScheduler{schedules: make(map[string]*transactionSchedule)}
}

// transactionDemandSignature builds a stable identity for a demand. It covers
// the immutable registration fields (address, frontier start, target finality
// and topic0 set) so a schedule is reused only for the exact same requirement
// and is never conflated with a widened/narrowed one.
func transactionDemandSignature(d transactionDemand) string {
	topics := make([]string, 0, len(d.Topic0))
	for _, t := range d.Topic0 {
		topics = append(topics, t.Hex())
	}
	sort.Strings(topics)
	return d.Address.Hex() + "|" + strconv.FormatUint(d.FromBlock, 10) + "|" +
		d.ToBlock.String() + "|" + strings.Join(topics, ",")
}

// syncDemands reconciles the scheduler with the current immutable demand set:
// it creates a schedule for every new demand and drops the schedules of demands
// that are no longer registered. Existing schedules are preserved untouched.
func (s *transactionScheduler) syncDemands(demands []transactionDemand) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := make(map[string]struct{}, len(demands))
	for i := range demands {
		sig := transactionDemandSignature(demands[i])
		wanted[sig] = struct{}{}
		if _, ok := s.schedules[sig]; !ok {
			s.schedules[sig] = &transactionSchedule{
				signature: sig,
				retry:     make(map[uint64]transactionRetryPage),
			}
		}
	}
	removed := false
	for sig := range s.schedules {
		if _, ok := wanted[sig]; !ok {
			delete(s.schedules, sig)
			removed = true
		}
	}
	if removed {
		// A removed (or removed-and-readded) registration invalidates any plan
		// that was built before this reconciliation: bump the generation marker
		// so such a stale plan can never advance the cursor or queue a retry on
		// the reconstructed schedule.
		s.generation++
	}
}

// plan returns the work to perform for one demand in a single scan. It is
// cheap: it only reads/initializes the in-memory cursor under the scheduler
// mutex and performs no I/O.
func (s *transactionScheduler) plan(demand transactionDemand,
	coveredFrom, coveredTo, pageSize uint64) transactionPagePlan {
	if s == nil || pageSize == 0 {
		return transactionPagePlan{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Stamp the plan with the current generation while holding the mutex, so
	// every page it yields is tied to the epoch it was planned in.
	generation := s.generation
	sched, ok := s.schedules[transactionDemandSignature(demand)]
	if !ok || sched == nil {
		return transactionPagePlan{generation: generation}
	}
	if !sched.initialized {
		start := demand.FromBlock
		if coveredFrom > start {
			start = coveredFrom
		}
		sched.frontier = start
		sched.initialized = true
	}

	plan := transactionPagePlan{generation: generation}
	plan.retries = sched.planRetriesLocked()

	// Brand new pages: [frontier, coveredTo]. The frontier is always advanced
	// past a planned page (see commit), so this is monotonic and a completed
	// page is never planned again.
	start := sched.frontier
	if coveredFrom > start {
		start = coveredFrom
	}
	if coveredTo >= start && coveredTo >= coveredFrom {
		for pageStart, count := start, 0; pageStart <= coveredTo && count < maxNewPagesPerScan; count++ {
			pageEnd := pageStart + pageSize - 1
			// Guard against uint64 overflow.
			if pageEnd < pageStart {
				pageEnd = coveredTo
			}
			if pageEnd > coveredTo {
				pageEnd = coveredTo
			}
			plan.pages = append(plan.pages, transactionPageRange{from: pageStart, to: pageEnd})
			if pageEnd >= coveredTo {
				break
			}
			pageStart = pageEnd + 1
		}
	}
	return plan
}

// planRetriesLocked returns the queued failed pages in ascending order. The
// caller must hold the scheduler mutex.
func (s *transactionSchedule) planRetriesLocked() []transactionPageRange {
	if len(s.retry) == 0 {
		return nil
	}
	starts := make([]uint64, 0, len(s.retry))
	for from := range s.retry {
		starts = append(starts, from)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	result := make([]transactionPageRange, 0, len(starts))
	for _, from := range starts {
		entry := s.retry[from]
		result = append(result, transactionPageRange{from: entry.from, to: entry.to})
	}
	return result
}

// commit records the outcome of one processed page. The cursor always advances
// to pageEnd+1, but a page that still has unresolved transactions is retained
// in the retry queue so the advance is never lossy. A completed page is removed
// from the retry queue.
//
// expectedGeneration is the generation the page was planned under (see
// transactionPagePlan.generation). If the scheduler generation has changed since
// (a reorg rewind, a run reset, a demand reconciliation or a retry-queue
// overflow rewind), the page belongs to a superseded epoch and is dropped: it
// must not advance the cursor past the rewound region nor queue a retry that the
// current epoch no longer owns.
//
// It reports whether the outcome was applied. A false return means the plan was
// superseded, so the caller can stop processing the remaining pages of that
// (now stale) plan instead of doing work whose result would be rejected.
func (s *transactionScheduler) commit(demand transactionDemand,
	from, to uint64, failed bool, expectedGeneration uint64) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedGeneration != s.generation {
		return false
	}
	sched, ok := s.schedules[transactionDemandSignature(demand)]
	if !ok || sched == nil {
		return false
	}
	if !sched.initialized || to >= sched.frontier {
		sched.frontier = to + 1
		sched.initialized = true
	}
	if sched.retry == nil {
		sched.retry = make(map[uint64]transactionRetryPage)
	}
	if !failed {
		delete(sched.retry, from)
		return true
	}
	if existing, ok := sched.retry[from]; ok {
		existing.attempts++
		sched.retry[from] = existing
		return true
	}
	if len(sched.retry) >= maxRetryPages {
		// Bound memory without losing the gap: rewind the cursor to the oldest
		// queued page and drop the queue. The rewound region is re-scanned, so
		// the gap is still covered (at worst recomputed).
		minFrom := from
		for k := range sched.retry {
			if k < minFrom {
				minFrom = k
			}
		}
		if minFrom < sched.frontier {
			sched.frontier = minFrom
		}
		sched.retry = make(map[uint64]transactionRetryPage)
		// The rewind supersedes every plan built before it, INCLUDING the still
		// unprocessed remainder of the very plan that just overflowed. Bump the
		// generation so a later page of that stale plan can never be accepted and
		// advance the cursor past the rewound region (which would silently drop
		// every gap below it).
		s.generation++
	}
	sched.retry[from] = transactionRetryPage{from: from, to: to, attempts: 1}
	return true
}

// rewindFrom rewinds every schedule so the region at/after firstReorgedBlock is
// re-examined after a reorg. Blocks below the reorg are immutable and stay
// behind the frontier. Only the retry pages the rewind re-covers (those that
// start at/after the reorg) are dropped; a queued page that starts strictly
// below the reorg stays canonical and is retained, so its unresolved
// transactions are not lost when the frontier is rewound past it. An
// uninitialized schedule is left to be (re)built from the new coverage on its
// next plan.
func (s *transactionScheduler) rewindFrom(firstReorgedBlock uint64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	for _, sched := range s.schedules {
		// Drop only the retry pages the rewind re-covers: a page whose start is
		// at/after the reorg is fully re-planned from the rewound frontier, but a
		// page that starts strictly below it is never planned again (the frontier
		// is only rewound to firstReorgedBlock), so dropping it would lose the
		// gap. A page straddling the reorg is retained whole: re-scanning its
		// canonical prefix is required and re-scanning its reorged suffix is at
		// worst recomputation.
		for from, entry := range sched.retry {
			if entry.from >= firstReorgedBlock {
				delete(sched.retry, from)
			}
		}
		if !sched.initialized {
			continue
		}
		if firstReorgedBlock < sched.frontier {
			sched.frontier = firstReorgedBlock
		}
	}
}

// resetAll discards every cursor so a new run reconstructs its requirements and
// missing cache from storage exactly once. It is called at the start of every
// run so no ephemeral state leaks across runs.
func (s *transactionScheduler) resetAll() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	for _, sched := range s.schedules {
		sched.initialized = false
		sched.frontier = 0
		sched.retry = make(map[uint64]transactionRetryPage)
	}
}
