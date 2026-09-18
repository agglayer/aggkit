package bridgedetector

import (
	"context"
	"fmt"
	"sort"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

const (
	defaultCursorName  = "l1-to-l2"
	defaultBlockWindow = uint64(1000)
	defaultPollPeriod  = time.Second
	defaultStartBlock  = uint64(0)
)

// CursorStore persists bridge detector bridge-discovery cursors.
type CursorStore interface {
	GetBridgeCursor(ctx context.Context, name string) (*autoclaimtypes.BridgeCursor, bool, error)
	SaveBridgeCursor(ctx context.Context, name string, cursor autoclaimtypes.BridgeCursor, now time.Time) error
}

// Option configures an L1ToL2 bridge detector.
type Option func(*L1ToL2)

// WithCursorName configures the durable cursor name.
func WithCursorName(name string) Option {
	return func(w *L1ToL2) {
		if name != "" {
			w.cursorName = name
		}
	}
}

// WithBlockWindow configures the maximum block range queried in one poll.
func WithBlockWindow(blockWindow uint64) Option {
	return func(w *L1ToL2) {
		if blockWindow > 0 {
			w.blockWindow = blockWindow
		}
	}
}

// WithOverlapBlocks configures how many already-processed blocks to re-query when new blocks arrive.
func WithOverlapBlocks(overlapBlocks uint64) Option {
	return func(w *L1ToL2) {
		w.overlapBlocks = overlapBlocks
	}
}

// WithStartBlock configures the first block used when no durable cursor exists.
func WithStartBlock(startBlock uint64) Option {
	return func(w *L1ToL2) {
		w.startBlock = startBlock
	}
}

// WithPollPeriod configures how often Start polls bridge sync.
func WithPollPeriod(period time.Duration) Option {
	return func(w *L1ToL2) {
		if period > 0 {
			w.pollPeriod = period
		}
	}
}

// WithEnabled configures whether Start and PollOnce should perform work.
func WithEnabled(enabled bool) Option {
	return func(w *L1ToL2) {
		w.enabled = enabled
	}
}

// WithEtrogL1UpgradeBlock configures the L1 block where Etrog global indexes become active.
func WithEtrogL1UpgradeBlock(block uint64) Option {
	return func(w *L1ToL2) {
		w.etrogL1UpgradeBlock = block
	}
}

// WithNow configures the clock used for cursor timestamps.
func WithNow(now func() time.Time) Option {
	return func(w *L1ToL2) {
		if now != nil {
			w.now = now
		}
	}
}

// WithLogger configures optional background processing logs.
func WithLogger(log aggkitcommon.Logger) Option {
	return func(w *L1ToL2) {
		w.log = log
	}
}

// PollResult summarizes one bridge detector poll. Since each destination now advances in its own
// per-destination block window (issue #1651), FromBlock and ToBlock describe the union of every
// window queried this poll: FromBlock is the lowest window's fromBlock and ToBlock is the highest
// window's toBlock. CursorAdvanced is true when at least one window persisted its cursor.
type PollResult struct {
	FromBlock           uint64
	ToBlock             uint64
	LastProcessedBlock  uint64
	BridgeCount         int
	MatchedBridgeCount  int
	EnqueuedBridgeCount int
	IgnoredBridgeCount  int
	SkippedBridgeCount  int
	CursorAdvanced      bool
}

// L1ToL2 is the bridge detector that discovers L1-initiated bridge exits and routes them to destination claimers.
type L1ToL2 struct {
	bridgeSource        autoclaimtypes.BridgeSource
	cursorStore         CursorStore
	registry            autoclaimtypes.ClaimerRegistry
	cursorName          string
	blockWindow         uint64
	overlapBlocks       uint64
	startBlock          uint64
	pollPeriod          time.Duration
	enabled             bool
	etrogL1UpgradeBlock uint64
	now                 func() time.Time
	log                 aggkitcommon.Logger
}

type destinationCursorState struct {
	claimer     autoclaimtypes.Claimer
	cursorName  string
	cursor      *autoclaimtypes.BridgeCursor
	cursorFound bool
	fromBlock   uint64
	nextCursor  autoclaimtypes.BridgeCursor
}

// NewL1ToL2 creates an L1-to-L2 Auto Claim bridge detector.
func NewL1ToL2(
	bridgeSource autoclaimtypes.BridgeSource,
	cursorStore CursorStore,
	registry autoclaimtypes.ClaimerRegistry,
	options ...Option,
) (*L1ToL2, error) {
	if bridgeSource == nil {
		return nil, fmt.Errorf("autoclaim l1-to-l2 bridge detector bridge source is nil")
	}
	if cursorStore == nil {
		return nil, fmt.Errorf("autoclaim l1-to-l2 bridge detector cursor store is nil")
	}
	if registry == nil {
		return nil, fmt.Errorf("autoclaim l1-to-l2 bridge detector claimer registry is nil")
	}

	detector := &L1ToL2{
		bridgeSource:  bridgeSource,
		cursorStore:   cursorStore,
		registry:      registry,
		cursorName:    defaultCursorName,
		blockWindow:   defaultBlockWindow,
		overlapBlocks: 1,
		startBlock:    defaultStartBlock,
		pollPeriod:    defaultPollPeriod,
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

// Start polls bridge sync until ctx is cancelled.
func (w *L1ToL2) Start(ctx context.Context) {
	if !w.enabled {
		return
	}

	if _, err := w.PollOnce(ctx); err != nil {
		w.logErrorf("autoclaim l1-to-l2 bridge detector poll failed: %v", err)
	}

	ticker := time.NewTicker(w.pollPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.PollOnce(ctx); err != nil {
				w.logErrorf("autoclaim l1-to-l2 bridge detector poll failed: %v", err)
			}
		}
	}
}

// PollOnce processes at most one bridge-sync block window per distinct per-destination fromBlock
// (issue #1651). Destinations are grouped by their current fromBlock, and each group issues its own
// GetBridges query and persists its own cursor(s) independently of every other group: a laggard
// destination (e.g. a newly added claimer backfilling from its start block) never blocks an
// already-caught-up destination from advancing in the same poll, and one group's failure to save its
// cursor does not roll back or gate a different group's already-persisted cursor. A single map[RequestKey]
// deduplication set spans the whole poll (not per group), which stays correct because every RequestKey
// embeds its destination and each destination belongs to exactly one group.
func (w *L1ToL2) PollOnce(ctx context.Context) (*PollResult, error) {
	if !w.enabled {
		return &PollResult{}, nil
	}

	lastProcessedBlock, found, err := w.bridgeSource.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("get l1 bridge sync last processed block: %w", err)
	}
	if !found {
		return &PollResult{}, nil
	}

	states, err := w.destinationCursorStates(ctx, lastProcessedBlock)
	if err != nil {
		return nil, err
	}
	result := &PollResult{LastProcessedBlock: lastProcessedBlock}
	if len(states) == 0 {
		return result, nil
	}

	groups := groupStatesByFromBlock(states)
	seen := make(map[autoclaimtypes.RequestKey]struct{})

	var firstErr error
	firstWindow := true
	for _, fromBlock := range orderedFromBlocks(groups) {
		// A fromBlock beyond lastProcessedBlock means that group's destination(s) are already
		// caught up (nextFromBlock returned lastProcessedBlock+1): no query, no cursor write, and
		// no effect on any other group.
		if fromBlock > lastProcessedBlock {
			continue
		}
		groupStates := groups[fromBlock]
		toBlock := minUint64(lastProcessedBlock, fromBlock+w.blockWindow-1)
		if firstWindow {
			result.FromBlock = fromBlock
			firstWindow = false
		}
		result.ToBlock = toBlock

		for _, state := range groupStates {
			state.nextCursor = autoclaimtypes.BridgeCursor{
				FromBlock: state.fromBlock,
				ToBlock:   toBlock,
				BlockNum:  toBlock,
				BlockPos:  0,
			}
		}

		bridges, err := w.bridgeSource.GetBridges(ctx, fromBlock, toBlock)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("get l1 bridges from %d to %d: %w", fromBlock, toBlock, err)
			}
			continue
		}
		result.BridgeCount += len(bridges)

		// Every bridge exit returned by l1bridgesync was initiated on L1, so there is no
		// bridge-origin filter to apply here. The claim identity (and the request key) is keyed on
		// the source network, which for this detector is always L1 (network 0), not
		// exit.OriginNetwork (the bridged token's origin network, which can be any network for a
		// wrapped token). A bridge whose destination is not part of this group (e.g. its window
		// overlaps another group's) is ignored here and left for the group that owns it.
		groupFailed := false
		for _, bridge := range bridges {
			exit := autoclaimtypes.NewBridgeExitFromSyncWithEtrog(bridge, w.etrogL1UpgradeBlock)
			if err := w.processBridge(ctx, exit, groupStates, seen, result); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				groupFailed = true
				break
			}
		}
		if groupFailed {
			continue
		}

		if err := w.saveCursors(ctx, groupStates, result); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
	}

	return result, firstErr
}

// processBridge evaluates a single bridge exit and enqueues it for claiming when appropriate.
// states holds only the destinations belonging to the window group currently being processed, so a
// bridge whose destination is owned by a different group's window (e.g. two windows overlap) is
// tallied via the same unknown-destination path as a truly unknown destination: it is ignored here
// and left for the group that owns it to enqueue exactly once.
// It updates result counters and the seen deduplication map in place.
func (w *L1ToL2) processBridge(
	ctx context.Context,
	exit autoclaimtypes.BridgeExit,
	states map[uint32]*destinationCursorState,
	seen map[autoclaimtypes.RequestKey]struct{},
	result *PollResult,
) error {
	state, ok := states[exit.DestinationNetwork]
	if !ok {
		result.IgnoredBridgeCount++
		return nil
	}

	if bridgeFilteredByPosition(exit, state, result) {
		return nil
	}

	state.nextCursor = maxCursorPosition(state.nextCursor, exit.BlockNum, exit.BlockPos)

	// The source network is always L1 (network 0) for L1-initiated exits, independent of the
	// bridged token's origin network. This mirrors the key storage.EnqueueRequest derives from
	// exit.SourceNetwork, so the in-poll dedup and the persisted uniqueness stay consistent.
	key := autoclaimtypes.DeriveRequestKey(autoclaimtypes.L1OriginNetwork, exit.DestinationNetwork, exit.DepositCount)
	if _, dup := seen[key]; dup {
		result.SkippedBridgeCount++
		return nil
	}
	seen[key] = struct{}{}

	claimed, err := state.claimer.IsClaimed(ctx, exit)
	if err != nil {
		return fmt.Errorf("check l1 bridge %s target claim state: %w", key, err)
	}
	if claimed {
		result.IgnoredBridgeCount++
		return nil
	}

	result.MatchedBridgeCount++
	if err := state.claimer.Enqueue(ctx, exit); err != nil {
		return fmt.Errorf("enqueue l1 bridge %s to claimer %s: %w", key, state.claimer.Target().ID, err)
	}
	result.EnqueuedBridgeCount++
	return nil
}

// bridgeFilteredByPosition reports whether the exit falls outside the block range covered by
// state (before fromBlock or at-or-before the already-processed cursor position).
// When filtered, the appropriate result counter is incremented and true is returned.
func bridgeFilteredByPosition(exit autoclaimtypes.BridgeExit, state *destinationCursorState, result *PollResult) bool {
	if exit.BlockNum < state.fromBlock {
		result.SkippedBridgeCount++
		return true
	}
	if state.cursorFound && state.cursor != nil && atOrBeforeCursor(exit, *state.cursor) {
		result.SkippedBridgeCount++
		return true
	}
	return false
}

// saveCursors persists the advanced cursor for every destination state in states, which is scoped to
// a single window group by the caller. A destination whose cursor is saved here keeps that write even
// if a save for a different group fails later in the same poll: persistence is per-group, never rolled
// back or gated by another group's outcome.
func (w *L1ToL2) saveCursors(
	ctx context.Context,
	states map[uint32]*destinationCursorState,
	result *PollResult,
) error {
	for _, state := range orderedStates(states) {
		if err := w.cursorStore.SaveBridgeCursor(ctx, state.cursorName, state.nextCursor, w.now()); err != nil {
			return fmt.Errorf("save autoclaim l1-to-l2 bridge detector cursor %s: %w", state.cursorName, err)
		}
		result.CursorAdvanced = true
	}
	return nil
}

func (w *L1ToL2) destinationCursorStates(
	ctx context.Context,
	lastProcessedBlock uint64,
) (map[uint32]*destinationCursorState, error) {
	claimers, err := w.registry.Claimers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list autoclaim l1-to-l2 bridge detector claimers: %w", err)
	}
	states := make(map[uint32]*destinationCursorState, len(claimers))
	for _, runtimeClaimer := range claimers {
		if runtimeClaimer == nil {
			return nil, fmt.Errorf("autoclaim l1-to-l2 bridge detector registry returned nil claimer")
		}
		target := runtimeClaimer.Target()
		cursorName := w.cursorNameForDestination(target.DestinationNetwork)
		cursor, cursorFound, err := w.cursorStore.GetBridgeCursor(ctx, cursorName)
		if err != nil {
			return nil, fmt.Errorf("get autoclaim l1-to-l2 bridge detector cursor %s: %w", cursorName, err)
		}
		states[target.DestinationNetwork] = &destinationCursorState{
			claimer:     runtimeClaimer,
			cursorName:  cursorName,
			cursor:      cursor,
			cursorFound: cursorFound,
			fromBlock:   w.nextFromBlock(cursor, cursorFound, lastProcessedBlock),
		}
	}
	return states, nil
}

func (w *L1ToL2) cursorNameForDestination(destinationNetwork uint32) string {
	return fmt.Sprintf("%s:%d", w.cursorName, destinationNetwork)
}

// groupStatesByFromBlock partitions states into window groups keyed by their shared fromBlock, so
// each group can be queried and persisted through its own GetBridges call independently of every
// other group (issue #1651). In steady state, every destination shares one fromBlock and there is
// exactly one group.
func groupStatesByFromBlock(
	states map[uint32]*destinationCursorState,
) map[uint64]map[uint32]*destinationCursorState {
	groups := make(map[uint64]map[uint32]*destinationCursorState)
	for destination, state := range states {
		group, ok := groups[state.fromBlock]
		if !ok {
			group = make(map[uint32]*destinationCursorState)
			groups[state.fromBlock] = group
		}
		group[destination] = state
	}
	return groups
}

// orderedFromBlocks returns groups' fromBlock keys in ascending order, for deterministic processing.
func orderedFromBlocks(groups map[uint64]map[uint32]*destinationCursorState) []uint64 {
	fromBlocks := make([]uint64, 0, len(groups))
	for fromBlock := range groups {
		fromBlocks = append(fromBlocks, fromBlock)
	}
	sort.Slice(fromBlocks, func(i, j int) bool {
		return fromBlocks[i] < fromBlocks[j]
	})
	return fromBlocks
}

func orderedStates(states map[uint32]*destinationCursorState) []*destinationCursorState {
	destinations := make([]uint32, 0, len(states))
	for destination := range states {
		destinations = append(destinations, destination)
	}
	sort.Slice(destinations, func(i, j int) bool {
		return destinations[i] < destinations[j]
	})

	ordered := make([]*destinationCursorState, 0, len(destinations))
	for _, destination := range destinations {
		ordered = append(ordered, states[destination])
	}
	return ordered
}

func (w *L1ToL2) nextFromBlock(
	cursor *autoclaimtypes.BridgeCursor,
	cursorFound bool,
	lastProcessedBlock uint64,
) uint64 {
	if !cursorFound || cursor == nil {
		return w.startBlock
	}
	if lastProcessedBlock <= cursor.ToBlock {
		return lastProcessedBlock + 1
	}
	nextBlock := cursor.ToBlock + 1
	if w.overlapBlocks == 0 {
		return nextBlock
	}
	if nextBlock <= w.overlapBlocks {
		return w.startBlock
	}
	overlapped := nextBlock - w.overlapBlocks
	if overlapped < w.startBlock {
		return w.startBlock
	}
	return overlapped
}

func (w *L1ToL2) logErrorf(format string, args ...interface{}) {
	if w.log != nil {
		w.log.Errorf(format, args...)
	}
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func atOrBeforeCursor(exit autoclaimtypes.BridgeExit, cursor autoclaimtypes.BridgeCursor) bool {
	if exit.BlockNum < cursor.BlockNum {
		return true
	}
	return exit.BlockNum == cursor.BlockNum && exit.BlockPos <= cursor.BlockPos
}

func maxCursorPosition(
	cursor autoclaimtypes.BridgeCursor,
	blockNum uint64,
	blockPos uint64,
) autoclaimtypes.BridgeCursor {
	if blockNum > cursor.BlockNum || blockNum == cursor.BlockNum && blockPos > cursor.BlockPos {
		cursor.BlockNum = blockNum
		cursor.BlockPos = blockPos
	}
	return cursor
}
