package bridgedetector

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/bridgeservice"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/common"
)

const (
	defaultL2ToLxCursorName = "l2-to-lx"
	defaultClaimPageSize    = uint32(100)
)

var (
	// ErrURLNotFound signals that a source network has no resolvable, healthy bridge service URL.
	// The detector logs and skips the source for this round without advancing its LER cursor.
	ErrURLNotFound = errors.New("autoclaim l2-to-lx bridge detector: source bridge service url not found")
	// ErrCandidatesNotSynced signals that the source bridge service has not yet synced the requested
	// LER. The detector treats it as "retry later": it skips the source without advancing its LER cursor.
	ErrCandidatesNotSynced = errors.New("autoclaim l2-to-lx bridge detector: source bridge service not synced yet")
)

// VerifiedBatchSource exposes the l1infotreesync data the L2-to-Lx detector needs to discover
// per-source-network local-exit-root (LER) updates and to derive per-source initial LER cursors.
type VerifiedBatchSource interface {
	// GetLastProcessedBlock returns the highest L1 block l1infotreesync has processed.
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
	// GetVerifiedBatchesInBlockRange returns every verified-batches row (both zkEVM and pessimistic
	// transitions, across all rollups) whose block is in the inclusive range [fromBlock, toBlock],
	// ordered by block_num ASC, block_pos ASC.
	GetVerifiedBatchesInBlockRange(fromBlock, toBlock uint64) ([]*l1infotreesync.VerifyBatches, error)
	// GetLatestL1InfoLeafUntilBlock returns the most recent L1 info tree leaf at or before blockNum.
	GetLatestL1InfoLeafUntilBlock(ctx context.Context, blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error)
	// GetLocalExitRoot returns the LER of networkID contained in the given rollup exit root.
	GetLocalExitRoot(ctx context.Context, networkID uint32, rollupExitRoot common.Hash) (common.Hash, error)
}

// LERCursorStore persists the durable per-(source, destination) LER discovery cursor.
type LERCursorStore interface {
	GetLERCursor(
		ctx context.Context, sourceNetwork, destinationNetwork uint32,
	) (*autoclaimtypes.LERCursor, bool, error)
	SaveLERCursor(
		ctx context.Context, sourceNetwork, destinationNetwork uint32,
		cursor autoclaimtypes.LERCursor, now time.Time,
	) error
	// SeedLERCursorsFromLegacy fans a pre-autoclaim0003 per-source cursor out to destinations.
	SeedLERCursorsFromLegacy(
		ctx context.Context, sourceNetwork uint32, destinationNetworks []uint32, now time.Time,
	) (bool, error)
}

// RequestEnqueuer persists discovered Auto Claim requests idempotently.
type RequestEnqueuer interface {
	EnqueueRequest(
		ctx context.Context,
		request autoclaimtypes.AutoClaimRequest,
	) (*autoclaimtypes.AutoClaimRequest, bool, error)
}

// ClaimCandidate is one bridge exit a source network offers for claiming. The leaf-to-LER Merkle
// proof is no longer carried here; it is fetched fresh at claim time by the proof preparer.
type ClaimCandidate struct {
	Bridge autoclaimtypes.BridgeExit
}

// ClaimCandidatesQuery parameterises a single page fetch of claim candidates from a source network's
// bridge service.
type ClaimCandidatesQuery struct {
	// URL is the resolved bridge service base URL of the source network.
	URL string
	// DestinationNetworkIDs restricts candidates to these destination networks (the enabled claimer
	// networks, excluding the source itself).
	DestinationNetworkIDs []uint32
	// FromLER is the exclusive lower-bound local exit root; nil requests the full history.
	FromLER *common.Hash
	// ToLER is the local exit root the proofs are built against (mandatory).
	ToLER common.Hash
	// PageNumber and PageSize follow the standard bridge service pagination.
	PageNumber uint32
	PageSize   uint32
}

// ClaimCandidatesFetcher resolves a source network's bridge service URL and fetches its claim
// candidates. The detector consumes it so unit tests can mock the remote bridge service; the
// production implementation wraps bridgeservicefinder.Finder and bridgeservice/client.Client.
type ClaimCandidatesFetcher interface {
	// GetURL resolves the source network's bridge service base URL. It returns ErrURLNotFound when no
	// healthy URL is cached, which the detector treats as "skip this source this round".
	GetURL(sourceNetwork uint32) (string, error)
	// GetClaimCandidates fetches one page of claim candidates. It returns ErrCandidatesNotSynced when
	// the source has not yet synced the requested LER (retry later). The returned count is the total
	// number of candidates matching the query, used by the caller to drive pagination.
	GetClaimCandidates(ctx context.Context, query ClaimCandidatesQuery) (candidates []ClaimCandidate, count int, err error)
}

// L2ToLxOption configures an L2ToLx bridge detector.
type L2ToLxOption func(*L2ToLx)

// WithL2ToLxCursorName configures the durable block-window cursor name.
func WithL2ToLxCursorName(name string) L2ToLxOption {
	return func(w *L2ToLx) {
		if name != "" {
			w.cursorName = name
		}
	}
}

// WithL2ToLxBlockWindow configures the maximum L1 block range queried in one poll.
func WithL2ToLxBlockWindow(blockWindow uint64) L2ToLxOption {
	return func(w *L2ToLx) {
		if blockWindow > 0 {
			w.blockWindow = blockWindow
		}
	}
}

// WithL2ToLxOverlapBlocks configures how many already-processed L1 blocks to re-query on each poll.
func WithL2ToLxOverlapBlocks(overlapBlocks uint64) L2ToLxOption {
	return func(w *L2ToLx) {
		w.overlapBlocks = overlapBlocks
	}
}

// WithL2ToLxStartL1Block configures the first L1 block used when no durable cursor exists, and the
// block used to derive each source's initial LER cursor (0 = full history).
func WithL2ToLxStartL1Block(startBlock uint64) L2ToLxOption {
	return func(w *L2ToLx) {
		w.startL1Block = startBlock
	}
}

// WithL2ToLxPollPeriod configures how often Start polls the verified-batch source.
func WithL2ToLxPollPeriod(period time.Duration) L2ToLxOption {
	return func(w *L2ToLx) {
		if period > 0 {
			w.pollPeriod = period
		}
	}
}

// WithL2ToLxPageSize configures the claim-candidates page size requested from source bridge services.
func WithL2ToLxPageSize(pageSize uint32) L2ToLxOption {
	return func(w *L2ToLx) {
		if pageSize > 0 {
			w.pageSize = pageSize
		}
	}
}

// WithL2ToLxEnabled configures whether Start and PollOnce should perform work.
func WithL2ToLxEnabled(enabled bool) L2ToLxOption {
	return func(w *L2ToLx) {
		w.enabled = enabled
	}
}

// WithL2ToLxNow configures the clock used for cursor timestamps.
func WithL2ToLxNow(now func() time.Time) L2ToLxOption {
	return func(w *L2ToLx) {
		if now != nil {
			w.now = now
		}
	}
}

// WithL2ToLxLogger configures optional background processing logs.
func WithL2ToLxLogger(log aggkitcommon.Logger) L2ToLxOption {
	return func(w *L2ToLx) {
		w.log = log
	}
}

// L2ToLxPollResult summarizes one L2-to-Lx bridge detector poll. Since the LER discovery cursor is
// now keyed by (source, destination) (issue #1651), the per-source counters below aggregate over
// every destination pair of that source: a source is "processed" only when every one of its pairs
// advanced, and "skipped" when at least one of them must be retried.
type L2ToLxPollResult struct {
	FromBlock          uint64
	ToBlock            uint64
	LastProcessedBlock uint64
	// SourceCount is the number of distinct source networks with a verify row in the window.
	SourceCount int
	// NewLERSourceCount is the number of sources with at least one (source, destination) pair whose
	// cursor differs from the source's newest LER, i.e. at least one pair with candidates to fetch.
	NewLERSourceCount int
	// ProcessedSourceCount is the number of sources fully processed this poll: every one of their
	// (source, destination) pair cursors advanced to the source's newest LER.
	ProcessedSourceCount int
	// SkippedSourceCount is the number of sources skipped this poll: nothing new for any pair, or at
	// least one pair's fetch group must be retried (finder miss or not synced yet). A source whose
	// groups partially succeeded counts as skipped, since its block-window position is held back.
	SkippedSourceCount int
	// CandidateCount is the total number of claim candidates fetched across every fetch group of
	// every processed source.
	CandidateCount int
	// EnqueuedCount is the number of newly enqueued requests.
	EnqueuedCount int
	// AlreadyClaimedCount is the number of candidates skipped because the target already claimed them.
	AlreadyClaimedCount int
	// CursorAdvanced reports whether the block-window cursor was advanced.
	CursorAdvanced bool
}

// L2ToLx is the bridge detector that discovers L2-initiated (rollup-origin) bridge exits by watching
// per-source LER updates in l1infotreesync, fetching claim candidates from each source network's
// bridge service, and routing them to the matching destination claimers.
type L2ToLx struct {
	source        VerifiedBatchSource
	fetcher       ClaimCandidatesFetcher
	registry      autoclaimtypes.ClaimerRegistry
	cursorStore   CursorStore
	lerCursors    LERCursorStore
	enqueuer      RequestEnqueuer
	cursorName    string
	blockWindow   uint64
	overlapBlocks uint64
	startL1Block  uint64
	pollPeriod    time.Duration
	pageSize      uint32
	enabled       bool
	now           func() time.Time
	log           aggkitcommon.Logger
}

// NewL2ToLx creates an L2-to-Lx Auto Claim bridge detector.
func NewL2ToLx(
	source VerifiedBatchSource,
	fetcher ClaimCandidatesFetcher,
	registry autoclaimtypes.ClaimerRegistry,
	cursorStore CursorStore,
	lerCursors LERCursorStore,
	enqueuer RequestEnqueuer,
	options ...L2ToLxOption,
) (*L2ToLx, error) {
	if source == nil {
		return nil, fmt.Errorf("autoclaim l2-to-lx bridge detector verified batch source is nil")
	}
	if fetcher == nil {
		return nil, fmt.Errorf("autoclaim l2-to-lx bridge detector claim candidates fetcher is nil")
	}
	if registry == nil {
		return nil, fmt.Errorf("autoclaim l2-to-lx bridge detector claimer registry is nil")
	}
	if cursorStore == nil {
		return nil, fmt.Errorf("autoclaim l2-to-lx bridge detector cursor store is nil")
	}
	if lerCursors == nil {
		return nil, fmt.Errorf("autoclaim l2-to-lx bridge detector ler cursor store is nil")
	}
	if enqueuer == nil {
		return nil, fmt.Errorf("autoclaim l2-to-lx bridge detector request enqueuer is nil")
	}

	detector := &L2ToLx{
		source:        source,
		fetcher:       fetcher,
		registry:      registry,
		cursorStore:   cursorStore,
		lerCursors:    lerCursors,
		enqueuer:      enqueuer,
		cursorName:    defaultL2ToLxCursorName,
		blockWindow:   defaultBlockWindow,
		overlapBlocks: 1,
		startL1Block:  defaultStartBlock,
		pollPeriod:    defaultPollPeriod,
		pageSize:      defaultClaimPageSize,
		enabled:       true,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
	for _, option := range options {
		option(detector)
	}

	return detector, nil
}

// Start polls the verified-batch source until ctx is cancelled.
func (w *L2ToLx) Start(ctx context.Context) {
	if !w.enabled {
		return
	}

	if _, err := w.PollOnce(ctx); err != nil {
		w.logErrorf("autoclaim l2-to-lx bridge detector poll failed: %v", err)
	}

	ticker := time.NewTicker(w.pollPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.PollOnce(ctx); err != nil {
				w.logErrorf("autoclaim l2-to-lx bridge detector poll failed: %v", err)
			}
		}
	}
}

// sourceLER captures the newest LER observed for a source network within one poll window.
type sourceLER struct {
	ler       common.Hash
	verifyNum uint64
	sourceID  uint32
}

// pendingDestination is one (source, destination) pair that has not yet processed the source's
// newest LER, together with the exclusive lower-bound LER its candidates must be fetched from
// (nil = full history).
type pendingDestination struct {
	destination uint32
	fromLER     *common.Hash
}

// lerGroup is the set of a source's pending destinations that share one from_ler and can therefore
// be fetched with a single claim-candidates query (the bridge service accepts exactly one from_ler
// per request, but many destination network IDs).
type lerGroup struct {
	fromLER      *common.Hash
	destinations []uint32
}

// sourceOutcome is the result of processing one source network within a poll.
type sourceOutcome int

const (
	// sourceUpToDate means every (source, destination) pair's cursor already matches the source's
	// newest LER, or the source has no destination pair at all: nothing to do.
	sourceUpToDate sourceOutcome = iota
	// sourceProcessed means every fetch group's candidates were enqueued and every pair's LER cursor
	// advanced.
	sourceProcessed
	// sourceRetryLater means at least one of the source's fetch groups was skipped for a transient
	// reason (finder miss or source bridge service not synced yet) and must be retried on a later
	// poll. Groups that did succeed keep their persisted pair cursors.
	sourceRetryLater
)

// PollOnce processes at most one L1 block window of verified-batch rows. Each source found in the
// window resolves an independent from_ler per destination claimer and issues one claim-candidates
// query per distinct from_ler (issue #1651), so the number of fetcher calls per poll stays bounded by
// the number of enabled claimers times the pages each of their queries needs, and collapses back to
// today's single batched query set as soon as every destination of a source shares one cursor.
func (w *L2ToLx) PollOnce(ctx context.Context) (*L2ToLxPollResult, error) {
	if !w.enabled {
		return &L2ToLxPollResult{}, nil
	}

	lastProcessedBlock, err := w.source.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("get l1infotreesync last processed block: %w", err)
	}

	blockCursor, cursorFound, err := w.cursorStore.GetBridgeCursor(ctx, w.cursorName)
	if err != nil {
		return nil, fmt.Errorf("get autoclaim l2-to-lx bridge detector cursor %s: %w", w.cursorName, err)
	}

	result := &L2ToLxPollResult{LastProcessedBlock: lastProcessedBlock}
	fromBlock := w.nextFromBlock(blockCursor, cursorFound, lastProcessedBlock)
	result.FromBlock = fromBlock
	if fromBlock > lastProcessedBlock {
		return result, nil
	}
	toBlock := minUint64(lastProcessedBlock, fromBlock+w.blockWindow-1)
	result.ToBlock = toBlock

	rows, err := w.source.GetVerifiedBatchesInBlockRange(fromBlock, toBlock)
	if err != nil {
		return result, fmt.Errorf("get verified batches from %d to %d: %w", fromBlock, toBlock, err)
	}

	latestBySource := newestLERPerSource(rows)
	result.SourceCount = len(latestBySource)

	destinationNetworks, err := w.enabledDestinationNetworks(ctx)
	if err != nil {
		return result, err
	}

	// retryBlock is the block of the earliest verify row whose source was skipped for a transient
	// reason (finder miss or source not synced yet); 0 = none.
	var retryBlock uint64
	for _, source := range orderedSourceLERs(latestBySource) {
		outcome, err := w.processSource(ctx, source, destinationNetworks, result)
		if err != nil {
			return result, err
		}
		switch outcome {
		case sourceProcessed:
			result.ProcessedSourceCount++
		case sourceRetryLater:
			result.SkippedSourceCount++
			if retryBlock == 0 || source.verifyNum < retryBlock {
				retryBlock = source.verifyNum
			}
		case sourceUpToDate:
			result.SkippedSourceCount++
		}
	}

	// The block-window cursor advances on every non-erroring poll, except that it never advances past
	// the verify row of a source skipped for a transient reason: it is held just before that row, so
	// the next poll re-observes it and retries the source even if it never publishes another LER.
	// A (source, destination) pair skipped this round keeps its LER cursor at its previous value, so
	// the retry fetch still uses from_ler = <old pair cursor>, which covers every candidate missed in
	// between; a pair whose group did succeed keeps its advanced cursor and is simply not re-fetched.
	// A hard error above returns before this point, leaving the cursor unchanged so the whole window
	// is retried.
	cursorToBlock := toBlock
	if retryBlock > 0 {
		if retryBlock <= fromBlock {
			// The retried row sits at the very start of the window: there is no forward progress to
			// record, keep the stored cursor untouched and retry the same window next poll.
			return result, nil
		}
		cursorToBlock = retryBlock - 1
	}
	nextCursor := autoclaimtypes.BridgeCursor{
		FromBlock: fromBlock,
		ToBlock:   cursorToBlock,
		BlockNum:  cursorToBlock,
		BlockPos:  0,
	}
	if err := w.cursorStore.SaveBridgeCursor(ctx, w.cursorName, nextCursor, w.now()); err != nil {
		return result, fmt.Errorf("save autoclaim l2-to-lx bridge detector cursor %s: %w", w.cursorName, err)
	}
	result.CursorAdvanced = true

	return result, nil
}

// processSource evaluates one source network's newest LER against every destination the enabled
// claimers cover. Each (source, destination) pair resolves its own from_ler from its own cursor, the
// pending pairs are grouped by that from_ler, and every group is fetched, enqueued and persisted
// independently (issue #1651): a destination added to a running deployment backfills from its own
// baseline instead of inheriting an already-advanced cursor that would skip its history, and it does
// not disturb the established destinations, which keep batching exactly as before.
//
// It returns sourceUpToDate when no pair has anything new (or the source has no destination pair at
// all), sourceRetryLater when at least one group was skipped for a transient reason (finder miss or
// not synced yet), and sourceProcessed when every group advanced its pairs' LER cursors.
func (w *L2ToLx) processSource(
	ctx context.Context,
	source sourceLER,
	destinationNetworks []uint32,
	result *L2ToLxPollResult,
) (sourceOutcome, error) {
	destinationIDs := excludeNetwork(destinationNetworks, source.sourceID)
	if len(destinationIDs) == 0 {
		// No enabled destination claimer other than the source itself: there is no (source,
		// destination) pair to track, so there is nothing to fetch and no cursor to write. The source
		// is simply re-evaluated next poll, which costs nothing (not even a cursor read).
		return sourceUpToDate, nil
	}

	// Fan a pre-autoclaim0003 per-source cursor out to the destinations configured right now, before
	// any pair is resolved, so an upgraded deployment's established destinations resume where they
	// were instead of re-scanning their whole history. It is a no-op once the source has been seeded.
	_, err := w.lerCursors.SeedLERCursorsFromLegacy(ctx, source.sourceID, destinationIDs, w.now())
	if err != nil {
		return sourceUpToDate, fmt.Errorf("seed autoclaim ler cursors from legacy for source %d: %w",
			source.sourceID, err)
	}

	pending, err := w.resolveFromLER(ctx, source, destinationIDs)
	if err != nil {
		return sourceUpToDate, err
	}
	if len(pending) == 0 {
		// Every pair's cursor already holds the source's newest LER: nothing new to process.
		return sourceUpToDate, nil
	}
	result.NewLERSourceCount++

	url, err := w.fetcher.GetURL(source.sourceID)
	if err != nil {
		w.logInfof("autoclaim l2-to-lx bridge detector: skip source %d (url not resolved): %v", source.sourceID, err)
		return sourceRetryLater, nil
	}

	return w.processLERGroups(ctx, source, url, groupPendingByFromLER(pending), result)
}

// processLERGroups fetches, enqueues and persists each from_ler group of one source in turn. A group
// is fully independent: its pair cursors are written as soon as its own candidates are enqueued (never
// before), and neither a later group's failure nor an earlier group's failure rolls that write back or
// gates it. A group that failed leaves its pairs' cursors untouched, so the next poll re-fetches only
// that group; the source itself still reports retry-later (or the hard error), which holds the
// block-window cursor just before this source's verify row so the row is re-observed.
func (w *L2ToLx) processLERGroups(
	ctx context.Context,
	source sourceLER,
	url string,
	groups []lerGroup,
	result *L2ToLxPollResult,
) (sourceOutcome, error) {
	var (
		firstErr   error
		retryLater bool
	)
	for _, group := range groups {
		candidates, err := w.fetchAllCandidates(ctx, url, group.destinations, group.fromLER, source.ler)
		if err != nil {
			if errors.Is(err, ErrCandidatesNotSynced) {
				w.logInfof("autoclaim l2-to-lx bridge detector: skip source %d destinations %v (not synced yet)",
					source.sourceID, group.destinations)
				retryLater = true
				continue
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("fetch claim candidates for source %d destinations %v: %w",
					source.sourceID, group.destinations, err)
			}
			continue
		}
		result.CandidateCount += len(candidates)

		if err := w.enqueueCandidates(ctx, source, candidates, result); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		if err := w.advanceLERCursors(ctx, source, group.destinations); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
	}

	if firstErr != nil {
		return sourceUpToDate, firstErr
	}
	if retryLater {
		return sourceRetryLater, nil
	}
	return sourceProcessed, nil
}

// resolveFromLER returns, for every destination that has not yet processed the source's newest LER,
// the exclusive lower-bound LER its candidates must be fetched from (nil = full history). A pair with
// a cursor resolves to its own stored LER; a pair with no cursor — a destination added after the
// source was already established, or a brand new deployment — resolves to the baseline derived from
// the configured StartL1Block. Destinations whose cursor already holds source.ler are omitted, so an
// empty result means the source is fully up to date. Destinations are visited in ascending order, so
// the resulting query and batching order is deterministic.
func (w *L2ToLx) resolveFromLER(
	ctx context.Context,
	source sourceLER,
	destinationIDs []uint32,
) ([]pendingDestination, error) {
	// initialFromLER depends only on the source and StartL1Block, never on the destination, so it is
	// derived at most once per source per poll and shared by every unseeded destination of it.
	var (
		initialLER      *common.Hash
		initialResolved bool
	)

	pending := make([]pendingDestination, 0, len(destinationIDs))
	for _, destination := range sortedNetworks(destinationIDs) {
		cursor, found, err := w.lerCursors.GetLERCursor(ctx, source.sourceID, destination)
		if err != nil {
			return nil, fmt.Errorf("get autoclaim ler cursor for source %d destination %d: %w",
				source.sourceID, destination, err)
		}
		if found {
			if cursor.LastLER == source.ler {
				// This pair has already processed the source's newest LER.
				continue
			}
			fromLER := cursor.LastLER
			pending = append(pending, pendingDestination{destination: destination, fromLER: &fromLER})
			continue
		}

		if !initialResolved {
			initialLER, err = w.initialFromLER(ctx, source.sourceID)
			if err != nil {
				return nil, err
			}
			initialResolved = true
		}
		pending = append(pending, pendingDestination{destination: destination, fromLER: initialLER})
	}

	return pending, nil
}

// groupPendingByFromLER partitions pending destinations into fetch groups sharing one from_ler, so
// each group can be queried with a single claim-candidates request (issue #1651). Groups keep the
// order in which their first destination appears, which is ascending destination order, and in
// steady state every pair shares the previous source LER and there is exactly one group.
func groupPendingByFromLER(pending []pendingDestination) []lerGroup {
	groups := make([]lerGroup, 0, len(pending))
	indexByLER := make(map[string]int, len(pending))
	for _, destination := range pending {
		key := lerGroupKey(destination.fromLER)
		if index, ok := indexByLER[key]; ok {
			groups[index].destinations = append(groups[index].destinations, destination.destination)
			continue
		}
		indexByLER[key] = len(groups)
		groups = append(groups, lerGroup{
			fromLER:      destination.fromLER,
			destinations: []uint32{destination.destination},
		})
	}
	return groups
}

// lerGroupKey is the grouping key of a from_ler bound: the empty string for nil (full history),
// otherwise the hash's hex representation.
func lerGroupKey(fromLER *common.Hash) string {
	if fromLER == nil {
		return ""
	}
	return fromLER.Hex()
}

// sortedNetworks returns a copy of networks in ascending order.
func sortedNetworks(networks []uint32) []uint32 {
	sorted := make([]uint32, len(networks))
	copy(sorted, networks)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	return sorted
}

// initialFromLER derives the exclusive lower-bound LER the first time a source network is seen. When
// StartL1Block is 0, the full history is requested (nil). Otherwise the source's LER at StartL1Block
// is used; a zero LER (the network had no LER yet at that block) also requests the full history.
func (w *L2ToLx) initialFromLER(ctx context.Context, sourceID uint32) (*common.Hash, error) {
	if w.startL1Block == 0 {
		return nil, nil
	}

	leaf, err := w.source.GetLatestL1InfoLeafUntilBlock(ctx, w.startL1Block)
	if err != nil {
		if errors.Is(err, l1infotreesync.ErrNotFound) {
			// StartL1Block predates the first L1 info tree leaf, so there is no baseline to derive a
			// lower-bound LER from. Same situation as a zero LER at that block: fetch the full history.
			return nil, nil
		}
		return nil, fmt.Errorf("get latest l1 info leaf until block %d for source %d: %w",
			w.startL1Block, sourceID, err)
	}

	ler, err := w.source.GetLocalExitRoot(ctx, sourceID, leaf.RollupExitRoot)
	if err != nil {
		return nil, fmt.Errorf("get local exit root for source %d at rollup exit root %s: %w",
			sourceID, leaf.RollupExitRoot, err)
	}
	if ler == (common.Hash{}) {
		return nil, nil
	}
	return &ler, nil
}

// fetchAllCandidates collects every claim candidate matching the query. The bridge service caps the
// number of destination network IDs per request (bridgeservice.MaxNetworkIDs), so the destination
// filter is split into batches of at most that size, paging through each batch.
func (w *L2ToLx) fetchAllCandidates(
	ctx context.Context,
	url string,
	destinationIDs []uint32,
	fromLER *common.Hash,
	toLER common.Hash,
) ([]ClaimCandidate, error) {
	all := make([]ClaimCandidate, 0)
	for start := 0; start < len(destinationIDs); start += bridgeservice.MaxNetworkIDs {
		end := min(start+bridgeservice.MaxNetworkIDs, len(destinationIDs))
		batch, err := w.fetchCandidatesForDestinations(ctx, url, destinationIDs[start:end], fromLER, toLER)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
	}
	return all, nil
}

// fetchCandidatesForDestinations pages through the source bridge service's claim candidates for one
// batch of destination network IDs until every matching candidate has been collected.
func (w *L2ToLx) fetchCandidatesForDestinations(
	ctx context.Context,
	url string,
	destinationIDs []uint32,
	fromLER *common.Hash,
	toLER common.Hash,
) ([]ClaimCandidate, error) {
	all := make([]ClaimCandidate, 0)
	// The bridge service /claim-candidates endpoint uses 1-based pagination (page_number must be
	// > 0, mirroring /bridges); page 0 is rejected with HTTP 400. Start at page 1.
	pageNumber := uint32(1)
	for {
		query := ClaimCandidatesQuery{
			URL:                   url,
			DestinationNetworkIDs: destinationIDs,
			FromLER:               fromLER,
			ToLER:                 toLER,
			PageNumber:            pageNumber,
			PageSize:              w.pageSize,
		}
		candidates, count, err := w.fetcher.GetClaimCandidates(ctx, query)
		if err != nil {
			return nil, err
		}
		all = append(all, candidates...)
		if len(candidates) == 0 || len(all) >= count {
			return all, nil
		}
		pageNumber++
	}
}

// enqueueCandidates routes every candidate to its destination claimer and enqueues the ones that are
// not already claimed on the target.
func (w *L2ToLx) enqueueCandidates(
	ctx context.Context,
	source sourceLER,
	candidates []ClaimCandidate,
	result *L2ToLxPollResult,
) error {
	for i := range candidates {
		candidate := candidates[i]
		exit := candidate.Bridge
		exit.SourceNetwork = source.sourceID

		claimer, ok, err := w.registry.ClaimerForDestination(ctx, exit.DestinationNetwork)
		if err != nil {
			return fmt.Errorf("resolve claimer for destination %d: %w", exit.DestinationNetwork, err)
		}
		if !ok {
			// The destination was requested from the source bridge service but no claimer handles it;
			// nothing to enqueue for it.
			continue
		}

		claimed, err := claimer.IsClaimed(ctx, exit)
		if err != nil {
			return fmt.Errorf("check target claim state for source %d deposit %d: %w",
				source.sourceID, exit.DepositCount, err)
		}
		if claimed {
			result.AlreadyClaimedCount++
			continue
		}

		request := autoclaimtypes.NewRequestFromBridgeExit(exit, w.now())
		request.MaxRetries = claimer.Target().MaxRetries
		request.LER = source.ler
		request.VerifyBlockNum = source.verifyNum

		if _, inserted, err := w.enqueuer.EnqueueRequest(ctx, request); err != nil {
			return fmt.Errorf("enqueue autoclaim request %s: %w", request.Key, err)
		} else if inserted {
			result.EnqueuedCount++
		}
	}
	return nil
}

// advanceLERCursors records the source's newest LER as processed for each of the given destinations.
// It is called once per fetch group and only after every candidate of that group has been enqueued,
// so a pair's cursor never moves past candidates that were not persisted. Destinations whose group
// did not complete are not passed in, which leaves their own cursors at their previous value.
func (w *L2ToLx) advanceLERCursors(ctx context.Context, source sourceLER, destinations []uint32) error {
	for _, destination := range destinations {
		cursor := autoclaimtypes.LERCursor{
			SourceNetwork:      source.sourceID,
			DestinationNetwork: destination,
			LastLER:            source.ler,
			LastVerifyBlockNum: source.verifyNum,
		}
		if err := w.lerCursors.SaveLERCursor(ctx, source.sourceID, destination, cursor, w.now()); err != nil {
			return fmt.Errorf("save autoclaim ler cursor for source %d destination %d: %w",
				source.sourceID, destination, err)
		}
	}
	return nil
}

func (w *L2ToLx) enabledDestinationNetworks(ctx context.Context) ([]uint32, error) {
	claimers, err := w.registry.Claimers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list autoclaim l2-to-lx bridge detector claimers: %w", err)
	}
	networks := make([]uint32, 0, len(claimers))
	for _, claimer := range claimers {
		if claimer == nil {
			return nil, fmt.Errorf("autoclaim l2-to-lx bridge detector registry returned nil claimer")
		}
		networks = append(networks, claimer.Target().DestinationNetwork)
	}
	return networks, nil
}

// nextFromBlock replicates the L1ToL2 window/overlap logic for the single block-window cursor.
func (w *L2ToLx) nextFromBlock(
	cursor *autoclaimtypes.BridgeCursor,
	cursorFound bool,
	lastProcessedBlock uint64,
) uint64 {
	if !cursorFound || cursor == nil {
		return w.startL1Block
	}
	if lastProcessedBlock <= cursor.ToBlock {
		return lastProcessedBlock + 1
	}
	nextBlock := cursor.ToBlock + 1
	if w.overlapBlocks == 0 {
		return nextBlock
	}
	if nextBlock <= w.overlapBlocks {
		return w.startL1Block
	}
	overlapped := nextBlock - w.overlapBlocks
	if overlapped < w.startL1Block {
		return w.startL1Block
	}
	return overlapped
}

func (w *L2ToLx) logErrorf(format string, args ...interface{}) {
	if w.log != nil {
		w.log.Errorf(format, args...)
	}
}

func (w *L2ToLx) logInfof(format string, args ...interface{}) {
	if w.log != nil {
		w.log.Infof(format, args...)
	}
}

// newestLERPerSource groups verified-batch rows by rollup id and keeps the newest LER per source.
// Rows are assumed ordered block_num ASC, block_pos ASC, so the last row seen per rollup is newest.
// Rows carrying a zero exit root are ignored (the upstream source does not emit them, but guard here).
func newestLERPerSource(rows []*l1infotreesync.VerifyBatches) map[uint32]sourceLER {
	latest := make(map[uint32]sourceLER, len(rows))
	for _, row := range rows {
		if row == nil || row.ExitRoot == (common.Hash{}) {
			continue
		}
		latest[row.RollupID] = sourceLER{
			ler:       row.ExitRoot,
			verifyNum: row.BlockNumber,
			sourceID:  row.RollupID,
		}
	}
	return latest
}

func orderedSourceLERs(latest map[uint32]sourceLER) []sourceLER {
	sources := make([]sourceLER, 0, len(latest))
	for _, source := range latest {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].sourceID < sources[j].sourceID
	})
	return sources
}

// excludeNetwork returns a copy of networks with excluded removed and duplicates collapsed.
func excludeNetwork(networks []uint32, excluded uint32) []uint32 {
	filtered := make([]uint32, 0, len(networks))
	seen := make(map[uint32]struct{}, len(networks))
	for _, network := range networks {
		if network == excluded {
			continue
		}
		if _, dup := seen[network]; dup {
			continue
		}
		seen[network] = struct{}{}
		filtered = append(filtered, network)
	}
	return filtered
}
