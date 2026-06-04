package watchdog

import (
	"context"
	"fmt"
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

// CursorStore persists watchdog bridge-discovery cursors.
type CursorStore interface {
	GetBridgeCursor(ctx context.Context, name string) (*autoclaimtypes.BridgeCursor, bool, error)
	SaveBridgeCursor(ctx context.Context, name string, cursor autoclaimtypes.BridgeCursor, now time.Time) error
}

// ClaimAnchorSelector chooses the destination-injected L1 info tree leaf to anchor a claim request.
type ClaimAnchorSelector interface {
	SelectL1InfoTreeIndex(ctx context.Context, exit autoclaimtypes.BridgeExit) (uint32, bool, error)
}

// Option configures an L1ToL2 watchdog.
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

// WithClaimAnchorSelector configures the selector used to wait for destination-injected GERs.
func WithClaimAnchorSelector(selector ClaimAnchorSelector) Option {
	return func(w *L1ToL2) {
		w.claimAnchorSelector = selector
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

// PollResult summarizes one watchdog poll.
type PollResult struct {
	FromBlock           uint64
	ToBlock             uint64
	LastProcessedBlock  uint64
	BridgeCount         int
	MatchedBridgeCount  int
	EnqueuedBridgeCount int
	IgnoredBridgeCount  int
	SkippedBridgeCount  int
	PendingBridgeCount  int
	CursorAdvanced      bool
}

// L1ToL2 discovers L1-origin bridge exits and routes them to destination claimers.
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
	claimAnchorSelector ClaimAnchorSelector
	now                 func() time.Time
	log                 aggkitcommon.Logger
}

// NewL1ToL2 creates an L1-to-L2 Auto Claim watchdog.
func NewL1ToL2(
	bridgeSource autoclaimtypes.BridgeSource,
	cursorStore CursorStore,
	registry autoclaimtypes.ClaimerRegistry,
	options ...Option,
) (*L1ToL2, error) {
	if bridgeSource == nil {
		return nil, fmt.Errorf("autoclaim l1-to-l2 watchdog bridge source is nil")
	}
	if cursorStore == nil {
		return nil, fmt.Errorf("autoclaim l1-to-l2 watchdog cursor store is nil")
	}
	if registry == nil {
		return nil, fmt.Errorf("autoclaim l1-to-l2 watchdog claimer registry is nil")
	}

	watchdog := &L1ToL2{
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
		option(watchdog)
	}

	return watchdog, nil
}

// Start polls bridge sync until ctx is cancelled.
func (w *L1ToL2) Start(ctx context.Context) {
	if !w.enabled {
		return
	}

	if _, err := w.PollOnce(ctx); err != nil {
		w.logErrorf("autoclaim l1-to-l2 watchdog poll failed: %v", err)
	}

	ticker := time.NewTicker(w.pollPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.PollOnce(ctx); err != nil {
				w.logErrorf("autoclaim l1-to-l2 watchdog poll failed: %v", err)
			}
		}
	}
}

// PollOnce processes at most one bridge-sync block window.
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

	cursor, cursorFound, err := w.cursorStore.GetBridgeCursor(ctx, w.cursorName)
	if err != nil {
		return nil, fmt.Errorf("get autoclaim l1-to-l2 cursor: %w", err)
	}

	fromBlock := w.nextFromBlock(cursor, cursorFound, lastProcessedBlock)
	result := &PollResult{FromBlock: fromBlock, LastProcessedBlock: lastProcessedBlock}
	if fromBlock > lastProcessedBlock {
		return result, nil
	}

	toBlock := minUint64(lastProcessedBlock, fromBlock+w.blockWindow-1)
	result.ToBlock = toBlock

	bridges, err := w.bridgeSource.GetBridges(ctx, fromBlock, toBlock)
	if err != nil {
		return result, fmt.Errorf("get l1 bridges from %d to %d: %w", fromBlock, toBlock, err)
	}
	result.BridgeCount = len(bridges)

	seen := make(map[autoclaimtypes.RequestKey]struct{}, len(bridges))
	holdCursor := false
	nextCursor := autoclaimtypes.BridgeCursor{
		FromBlock: fromBlock,
		ToBlock:   toBlock,
		BlockNum:  toBlock,
		BlockPos:  0,
	}
	for _, bridge := range bridges {
		exit := autoclaimtypes.NewBridgeExitFromSyncWithEtrog(bridge, w.etrogL1UpgradeBlock)
		nextCursor = maxCursorPosition(nextCursor, exit.BlockNum, exit.BlockPos)
		if cursorFound && cursor != nil && atOrBeforeCursor(exit, *cursor) {
			result.SkippedBridgeCount++
			continue
		}
		if exit.OriginNetwork != autoclaimtypes.L1OriginNetwork {
			result.IgnoredBridgeCount++
			continue
		}

		key := autoclaimtypes.DeriveRequestKey(
			exit.OriginNetwork,
			exit.DestinationNetwork,
			exit.DepositCount,
		)
		if _, ok := seen[key]; ok {
			result.SkippedBridgeCount++
			continue
		}
		seen[key] = struct{}{}

		claimer, ok, err := w.registry.ClaimerForDestination(ctx, exit.DestinationNetwork)
		if err != nil {
			return result, fmt.Errorf("resolve claimer for destination %d: %w", exit.DestinationNetwork, err)
		}
		if !ok {
			result.IgnoredBridgeCount++
			continue
		}

		result.MatchedBridgeCount++
		if w.claimAnchorSelector != nil {
			l1InfoTreeIndex, ready, err := w.claimAnchorSelector.SelectL1InfoTreeIndex(ctx, exit)
			if err != nil {
				return result, fmt.Errorf("select L1 info tree index for l1 bridge %s: %w", key, err)
			}
			if !ready {
				result.PendingBridgeCount++
				holdCursor = true
				break
			}
			exit.L1InfoTreeIndex = &l1InfoTreeIndex
		}
		if err := claimer.Enqueue(ctx, exit); err != nil {
			return result, fmt.Errorf("enqueue l1 bridge %s to claimer %s: %w", key, claimer.Target().ID, err)
		}
		result.EnqueuedBridgeCount++
	}

	if holdCursor {
		return result, nil
	}

	if err := w.cursorStore.SaveBridgeCursor(ctx, w.cursorName, nextCursor, w.now()); err != nil {
		return result, fmt.Errorf("save autoclaim l1-to-l2 cursor: %w", err)
	}
	result.CursorAdvanced = true

	return result, nil
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
