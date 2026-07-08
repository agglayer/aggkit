package bridgedetector

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
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

// LERCursorStore persists the durable per-source-network LER discovery cursor.
type LERCursorStore interface {
	GetLERCursor(ctx context.Context, sourceNetwork uint32) (*autoclaimtypes.LERCursor, bool, error)
	SaveLERCursor(ctx context.Context, sourceNetwork uint32, cursor autoclaimtypes.LERCursor, now time.Time) error
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

// L2ToLxPollResult summarizes one L2-to-Lx bridge detector poll.
type L2ToLxPollResult struct {
	FromBlock          uint64
	ToBlock            uint64
	LastProcessedBlock uint64
	// SourceCount is the number of distinct source networks with a verify row in the window.
	SourceCount int
	// NewLERSourceCount is the number of sources whose newest LER differed from their cursor.
	NewLERSourceCount int
	// ProcessedSourceCount is the number of sources fully processed (LER cursor advanced) this poll.
	ProcessedSourceCount int
	// SkippedSourceCount is the number of sources skipped this poll (finder miss or not synced yet).
	SkippedSourceCount int
	// CandidateCount is the total number of claim candidates fetched across all processed sources.
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

// PollOnce processes at most one L1 block window of verified-batch rows.
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

	for _, source := range orderedSourceLERs(latestBySource) {
		processed, err := w.processSource(ctx, source, destinationNetworks, result)
		if err != nil {
			return result, err
		}
		if processed {
			result.ProcessedSourceCount++
		} else {
			result.SkippedSourceCount++
		}
	}

	// The block-window cursor advances on every non-erroring poll. Sources skipped this round (finder
	// miss or not synced yet) keep their per-source LER cursor at its previous value, so when their
	// next LER update is observed the fetch still uses from_ler = <old cursor>, which covers every
	// candidate missed in between. A hard error above returns before this point, leaving the cursor
	// unchanged so the whole window is retried.
	nextCursor := autoclaimtypes.BridgeCursor{
		FromBlock: fromBlock,
		ToBlock:   toBlock,
		BlockNum:  toBlock,
		BlockPos:  0,
	}
	if err := w.cursorStore.SaveBridgeCursor(ctx, w.cursorName, nextCursor, w.now()); err != nil {
		return result, fmt.Errorf("save autoclaim l2-to-lx bridge detector cursor %s: %w", w.cursorName, err)
	}
	result.CursorAdvanced = true

	return result, nil
}

// processSource evaluates one source network's newest LER: it resolves the source bridge service,
// fetches every claim-candidate page, routes each candidate to its destination claimer, and advances
// the source's LER cursor only after all pages have been enqueued. It returns (false, nil) when the
// source is skipped this round (already up to date, finder miss, or not synced yet) and (true, nil)
// when the source's LER cursor was advanced.
func (w *L2ToLx) processSource(
	ctx context.Context,
	source sourceLER,
	destinationNetworks []uint32,
	result *L2ToLxPollResult,
) (bool, error) {
	fromLER, hasNewLER, err := w.resolveFromLER(ctx, source)
	if err != nil {
		return false, err
	}
	if !hasNewLER {
		// The source's newest LER already matches its cursor: nothing new to process.
		return false, nil
	}
	result.NewLERSourceCount++

	destinationIDs := excludeNetwork(destinationNetworks, source.sourceID)
	if len(destinationIDs) == 0 {
		// No enabled destination claimer other than the source itself: nothing to claim, but the LER
		// is genuinely processed, so advance the cursor to avoid re-evaluating it every poll.
		return true, w.advanceLERCursor(ctx, source)
	}

	url, err := w.fetcher.GetURL(source.sourceID)
	if err != nil {
		w.logInfof("autoclaim l2-to-lx bridge detector: skip source %d (url not resolved): %v", source.sourceID, err)
		return false, nil
	}

	candidates, err := w.fetchAllCandidates(ctx, url, destinationIDs, fromLER, source.ler)
	if err != nil {
		if errors.Is(err, ErrCandidatesNotSynced) {
			w.logInfof("autoclaim l2-to-lx bridge detector: skip source %d (not synced yet)", source.sourceID)
			return false, nil
		}
		return false, fmt.Errorf("fetch claim candidates for source %d: %w", source.sourceID, err)
	}
	result.CandidateCount += len(candidates)

	if err := w.enqueueCandidates(ctx, source, candidates, result); err != nil {
		return false, err
	}

	if err := w.advanceLERCursor(ctx, source); err != nil {
		return false, err
	}
	return true, nil
}

// resolveFromLER reports whether the source has a new LER (different from its cursor) and, if so, the
// exclusive lower-bound LER to request candidates from (nil = full history). When the source has no
// cursor yet, the lower bound is derived from the configured StartL1Block.
func (w *L2ToLx) resolveFromLER(ctx context.Context, source sourceLER) (*common.Hash, bool, error) {
	cursor, found, err := w.lerCursors.GetLERCursor(ctx, source.sourceID)
	if err != nil {
		return nil, false, fmt.Errorf("get autoclaim ler cursor for source %d: %w", source.sourceID, err)
	}
	if found {
		if cursor.LastLER == source.ler {
			return nil, false, nil
		}
		fromLER := cursor.LastLER
		return &fromLER, true, nil
	}

	fromLER, err := w.initialFromLER(ctx, source.sourceID)
	if err != nil {
		return nil, false, err
	}
	return fromLER, true, nil
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

// fetchAllCandidates pages through the source bridge service's claim candidates until every candidate
// matching the query has been collected.
func (w *L2ToLx) fetchAllCandidates(
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

func (w *L2ToLx) advanceLERCursor(ctx context.Context, source sourceLER) error {
	cursor := autoclaimtypes.LERCursor{
		SourceNetwork:      source.sourceID,
		LastLER:            source.ler,
		LastVerifyBlockNum: source.verifyNum,
	}
	if err := w.lerCursors.SaveLERCursor(ctx, source.sourceID, cursor, w.now()); err != nil {
		return fmt.Errorf("save autoclaim ler cursor for source %d: %w", source.sourceID, err)
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
