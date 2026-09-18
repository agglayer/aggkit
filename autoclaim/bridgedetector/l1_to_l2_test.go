package bridgedetector

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"testing"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func TestPollOnceConstructsPollingWindows(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{lastProcessedBlock: 25, found: true}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	detector := newTestDetector(t, source, store, newFakeRegistry(claimer), WithStartBlock(5), WithBlockWindow(10))

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(5), result.FromBlock)
	require.Equal(t, uint64(14), result.ToBlock)
	require.Equal(t, []blockRange{{from: 5, to: 14}}, source.ranges)

	result, err = detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(14), result.FromBlock)
	require.Equal(t, uint64(23), result.ToBlock)
	require.Equal(t, []blockRange{{from: 5, to: 14}, {from: 14, to: 23}}, source.ranges)
}

func TestPollOncePersistsCursorAfterSuccess(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{lastProcessedBlock: 20, found: true}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	detector := newTestDetector(t, source, store, newFakeRegistry(claimer), WithBlockWindow(7))

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.True(t, result.CursorAdvanced)

	cursor, ok := store.cursors[detector.cursorNameForDestination(10)]
	require.True(t, ok)
	require.Equal(t, uint64(0), cursor.FromBlock)
	require.Equal(t, uint64(6), cursor.ToBlock)
	require.Equal(t, uint64(6), cursor.BlockNum)
}

func TestDuplicateBridgeOverlapDoesNotCreateDuplicateEnqueue(t *testing.T) {
	ctx := context.Background()
	bridge := makeSyncBridge(1, autoclaimtypes.L1OriginNetwork, 10, 100, 0)
	source := &fakeBridgeSource{
		lastProcessedBlock: 102,
		found:              true,
		bridgesByRange: map[blockRange][]bridgesync.Bridge{
			{from: 100, to: 101}: {bridge, bridge},
			{from: 101, to: 102}: {bridge},
		},
	}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	registry := newFakeRegistry(claimer)
	detector := newTestDetector(t, source, store, registry, WithStartBlock(100), WithBlockWindow(2))

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.EnqueuedBridgeCount)
	require.Equal(t, 1, result.SkippedBridgeCount)
	require.Len(t, claimer.enqueued, 1)

	result, err = detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.EnqueuedBridgeCount)
	require.Equal(t, 1, result.SkippedBridgeCount)
	require.Len(t, claimer.enqueued, 1, "claimer idempotency prevents overlap from creating another request")
}

func TestDestinationFilteringAndUnknownDestinations(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{
		lastProcessedBlock: 12,
		found:              true,
		bridgesByRange: map[blockRange][]bridgesync.Bridge{
			{from: 0, to: 12}: {
				makeSyncBridge(1, autoclaimtypes.L1OriginNetwork, 10, 1, 0),
				makeSyncBridge(2, autoclaimtypes.L1OriginNetwork, 11, 2, 0),
				// Token origin network 1 (an L2-origin token bridged from L1): still processed,
				// because every l1bridgesync exit is L1-initiated regardless of the token origin.
				makeSyncBridge(3, 1, 10, 3, 0),
				makeSyncBridge(4, autoclaimtypes.L1OriginNetwork, 99, 4, 0),
			},
		},
	}
	store := newMemoryCursorStore()
	claimer10 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	claimer11 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: 11}}
	registry := newFakeRegistry(claimer10, claimer11)
	detector := newTestDetector(t, source, store, registry, WithBlockWindow(13))

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, result.BridgeCount)
	require.Equal(t, 3, result.MatchedBridgeCount)
	require.Equal(t, 3, result.EnqueuedBridgeCount)
	require.Equal(t, 1, result.IgnoredBridgeCount, "only the bridge to unknown destination 99 is ignored")
	require.Len(t, claimer10.enqueued, 2)
	require.Len(t, claimer11.enqueued, 1)
	require.Equal(t, uint32(10), claimer10.enqueued[0].DestinationNetwork)
	require.Equal(t, uint32(10), claimer10.enqueued[1].DestinationNetwork)
	require.Equal(t, uint32(11), claimer11.enqueued[0].DestinationNetwork)
}

func TestBridgeSyncErrorDoesNotAdvanceCursor(t *testing.T) {
	ctx := context.Background()
	sourceErr := errors.New("bridge sync unavailable")
	source := &fakeBridgeSource{lastProcessedBlock: 12, found: true, getBridgesErr: sourceErr}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	detector := newTestDetector(t, source, store, newFakeRegistry(claimer), WithBlockWindow(13))

	_, err := detector.PollOnce(ctx)
	require.ErrorIs(t, err, sourceErr)
	require.Empty(t, store.cursors)
}

func TestRestartFromPersistedCursor(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{lastProcessedBlock: 60, found: true}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	detector := newTestDetector(t, source, store, newFakeRegistry(claimer), WithBlockWindow(20), WithOverlapBlocks(2))
	store.cursors[detector.cursorNameForDestination(10)] = autoclaimtypes.BridgeCursor{
		FromBlock: 40,
		ToBlock:   50,
		BlockNum:  50,
	}

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(49), result.FromBlock)
	require.Equal(t, uint64(60), result.ToBlock)
	require.Equal(t, []blockRange{{from: 49, to: 60}}, source.ranges)
}

func TestNewDestinationStartsFromConfiguredStartBlock(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{
		lastProcessedBlock: 60,
		found:              true,
		bridgesByRange: map[blockRange][]bridgesync.Bridge{
			{from: 5, to: 14}: {makeSyncBridge(1, autoclaimtypes.L1OriginNetwork, 11, 6, 0)},
		},
	}
	store := newMemoryCursorStore()
	claimer10 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	claimer11 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: 11}}
	detector := newTestDetector(
		t,
		source,
		store,
		newFakeRegistry(claimer10, claimer11),
		WithStartBlock(5),
		WithBlockWindow(10),
		WithOverlapBlocks(2),
	)
	store.cursors[detector.cursorNameForDestination(10)] = autoclaimtypes.BridgeCursor{
		FromBlock: 40,
		ToBlock:   50,
		BlockNum:  50,
	}

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(5), result.FromBlock)
	require.Equal(t, uint64(58), result.ToBlock)
	require.Equal(t, []blockRange{{from: 5, to: 14}, {from: 49, to: 58}}, source.ranges)
	require.Len(t, claimer11.enqueued, 1)
	_, ok := store.cursors[detector.cursorNameForDestination(11)]
	require.True(t, ok)

	// Destination 10 (the established claimer, frozen at fromBlock 49 before the fix) now gets its
	// own window and advances instead of being silently skipped by the new destination's backfill.
	destination10Cursor, ok := store.cursors[detector.cursorNameForDestination(10)]
	require.True(t, ok, "destination 10 must advance in its own window instead of being frozen")
	require.Equal(t, uint64(58), destination10Cursor.ToBlock)
}

func TestEnqueueCallsGoToCorrectClaimer(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{
		lastProcessedBlock: 10,
		found:              true,
		bridgesByRange: map[blockRange][]bridgesync.Bridge{
			{from: 0, to: 10}: {
				makeSyncBridge(1, autoclaimtypes.L1OriginNetwork, 10, 1, 0),
				makeSyncBridge(2, autoclaimtypes.L1OriginNetwork, 11, 2, 0),
			},
		},
	}
	store := newMemoryCursorStore()
	claimer10 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	claimer11 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: 11}}
	detector := newTestDetector(t, source, store, newFakeRegistry(claimer10, claimer11), WithBlockWindow(11))

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, result.EnqueuedBridgeCount)
	require.Len(t, claimer10.enqueued, 1)
	require.Len(t, claimer11.enqueued, 1)
	require.Equal(t, uint32(1), claimer10.enqueued[0].DepositCount)
	require.Equal(t, uint32(2), claimer11.enqueued[0].DepositCount)
}

func TestPollOnceMarksPreEtrogBridgeBeforeConfiguredUpgradeBlock(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{
		lastProcessedBlock: 10,
		found:              true,
		bridgesByRange: map[blockRange][]bridgesync.Bridge{
			{from: 0, to: 10}: {
				makeSyncBridge(
					7,
					autoclaimtypes.L1OriginNetwork,
					autoclaimtypes.LegacyZkEVMRollupNetwork,
					10,
					0,
				),
			},
		},
	}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{
		target: autoclaimtypes.ClaimerTarget{
			ID:                 fakeClaimer1ID,
			DestinationNetwork: autoclaimtypes.LegacyZkEVMRollupNetwork,
		},
	}
	detector := newTestDetector(
		t,
		source,
		store,
		newFakeRegistry(claimer),
		WithBlockWindow(11),
		WithEtrogL1UpgradeBlock(10),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.EnqueuedBridgeCount)
	require.Len(t, claimer.enqueued, 1)
	require.True(t, claimer.enqueued[0].PreEtrog)
	require.Equal(t, uint64(7), claimer.enqueued[0].GlobalIndex.Uint64())
}

func TestPollOnceIgnoresAlreadyClaimedBridgeBeforeEnqueue(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{
		lastProcessedBlock: 10,
		found:              true,
		bridgesByRange: map[blockRange][]bridgesync.Bridge{
			{from: 0, to: 10}: {makeSyncBridge(1, autoclaimtypes.L1OriginNetwork, 10, 1, 0)},
		},
	}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{
		target:  autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10},
		claimed: true,
	}
	detector := newTestDetector(t, source, store, newFakeRegistry(claimer), WithBlockWindow(11))

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.EnqueuedBridgeCount)
	require.Equal(t, 1, result.IgnoredBridgeCount)
	require.True(t, result.CursorAdvanced)
	require.Empty(t, claimer.enqueued)
	require.Len(t, claimer.claimChecks, 1)
}

func TestNewL1ToL2NilArgs(t *testing.T) {
	source := &fakeBridgeSource{lastProcessedBlock: 10, found: true}
	store := newMemoryCursorStore()
	registry := newFakeRegistry()

	_, err := NewL1ToL2(nil, store, registry)
	require.ErrorContains(t, err, "bridge source is nil")

	_, err = NewL1ToL2(source, nil, registry)
	require.ErrorContains(t, err, "cursor store is nil")

	_, err = NewL1ToL2(source, store, nil)
	require.ErrorContains(t, err, "claimer registry is nil")
}

func TestWithCursorNameOption(t *testing.T) {
	source := &fakeBridgeSource{lastProcessedBlock: 10, found: true}
	store := newMemoryCursorStore()
	registry := newFakeRegistry()

	w, err := NewL1ToL2(source, store, registry, WithCursorName("custom-cursor"))
	require.NoError(t, err)
	require.Equal(t, "custom-cursor", w.cursorName)

	// Empty string should be ignored, keeping the default.
	w2, err := NewL1ToL2(source, store, registry, WithCursorName(""))
	require.NoError(t, err)
	require.Equal(t, defaultCursorName, w2.cursorName)
}

func TestWithPollPeriodOption(t *testing.T) {
	source := &fakeBridgeSource{lastProcessedBlock: 10, found: true}
	store := newMemoryCursorStore()
	registry := newFakeRegistry()

	w, err := NewL1ToL2(source, store, registry, WithPollPeriod(5*time.Second))
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, w.pollPeriod)

	// Zero or negative period should be ignored, keeping the default.
	w2, err := NewL1ToL2(source, store, registry, WithPollPeriod(0))
	require.NoError(t, err)
	require.Equal(t, defaultPollPeriod, w2.pollPeriod)
}

func TestWithEnabledOption(t *testing.T) {
	source := &fakeBridgeSource{lastProcessedBlock: 10, found: true}
	store := newMemoryCursorStore()
	registry := newFakeRegistry()

	w, err := NewL1ToL2(source, store, registry, WithEnabled(false))
	require.NoError(t, err)
	require.False(t, w.enabled)
}

func TestWithLoggerOption(t *testing.T) {
	source := &fakeBridgeSource{lastProcessedBlock: 10, found: true}
	store := newMemoryCursorStore()
	registry := newFakeRegistry()

	var log aggkitcommon.Logger
	w, err := NewL1ToL2(source, store, registry, WithLogger(log))
	require.NoError(t, err)
	require.Nil(t, w.log)
}

func TestPollOnceDisabledBridgeDetector(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{lastProcessedBlock: 10, found: true}
	store := newMemoryCursorStore()
	w, err := NewL1ToL2(source, store, newFakeRegistry(), WithEnabled(false))
	require.NoError(t, err)

	result, err := w.PollOnce(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, uint64(0), result.LastProcessedBlock)
	require.Empty(t, source.ranges)
}

func TestPollOnceGetLastBlockError(t *testing.T) {
	ctx := context.Background()
	sourceErr := errors.New("bridge sync unavailable")
	source := &fakeBridgeSource{lastProcessedErr: sourceErr}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	w := newTestDetector(t, source, store, newFakeRegistry(claimer))

	_, err := w.PollOnce(ctx)
	require.ErrorIs(t, err, sourceErr)
}

func TestPollOnceNotFoundReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{lastProcessedBlock: 0, found: false}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	w := newTestDetector(t, source, store, newFakeRegistry(claimer))

	result, err := w.PollOnce(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Empty(t, source.ranges)
}

func TestClaimerErrorDoesNotAdvanceCursor(t *testing.T) {
	ctx := context.Background()
	enqueueErr := errors.New("enqueue failed")
	source := &fakeBridgeSource{
		lastProcessedBlock: 10,
		found:              true,
		bridgesByRange: map[blockRange][]bridgesync.Bridge{
			{from: 0, to: 10}: {makeSyncBridge(1, autoclaimtypes.L1OriginNetwork, 10, 1, 0)},
		},
	}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{
		target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10},
		err:    enqueueErr,
	}
	detector := newTestDetector(t, source, store, newFakeRegistry(claimer), WithBlockWindow(11))

	_, err := detector.PollOnce(ctx)
	require.ErrorIs(t, err, enqueueErr)
	require.Empty(t, store.cursors)
}

// TestL1ToL2DedupsByL1SourceNotTokenOrigin proves the L1→L2 request key (and the in-poll dedup)
// depends only on the source network (always L1 / network 0) and not on the bridged token's origin
// network. Two exits to the same destination with the same deposit count are the same claim identity
// regardless of token origin, so the detector must dedup them to a single enqueue. Before the fix
// (which keyed the dedup on exit.OriginNetwork) these two exits produced two distinct keys and were
// both enqueued.
func TestL1ToL2DedupsByL1SourceNotTokenOrigin(t *testing.T) {
	ctx := context.Background()
	// Same destination + deposit count, different token origin networks.
	a := makeSyncBridge(5, 7, 10, 100, 0)
	b := makeSyncBridge(5, 8, 10, 100, 1)
	source := &fakeBridgeSource{
		lastProcessedBlock: 100,
		found:              true,
		bridgesByRange: map[blockRange][]bridgesync.Bridge{
			{from: 0, to: 100}: {a, b},
		},
	}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	detector := newTestDetector(t, source, store, newFakeRegistry(claimer), WithBlockWindow(101))

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.EnqueuedBridgeCount, "both exits share the L1-source claim identity 0:10:5")
	require.Equal(t, 1, result.SkippedBridgeCount, "the second exit is deduped by the L1-source key")
	require.Len(t, claimer.enqueued, 1)
}

// TestL1ToL2_NewClaimerDoesNotStallEstablishedClaimers proves that adding a claimer for a brand-new
// destination does not stop an already-caught-up destination from making progress in the same poll.
// Before the per-destination-window fix (issue #1651), the whole poll collapsed to a single shared
// block window anchored at the lowest fromBlock (the new destination's), leaving every established
// destination ineligible and cursor-frozen for the duration of the new destination's backfill.
func TestL1ToL2_NewClaimerDoesNotStallEstablishedClaimers(t *testing.T) {
	ctx := context.Background()
	established := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	newDestination := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: 11}}
	source := &fakeBridgeSource{lastProcessedBlock: 5_500, found: true}
	store := newMemoryCursorStore()
	detector := newTestDetector(
		t,
		source,
		store,
		newFakeRegistry(established, newDestination),
		WithStartBlock(0),
		WithBlockWindow(1000),
	)
	store.cursors[detector.cursorNameForDestination(10)] = autoclaimtypes.BridgeCursor{
		FromBlock: 4_000,
		ToBlock:   5_000,
		BlockNum:  5_000,
	}

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.True(t, result.CursorAdvanced)

	establishedCursor, ok := store.cursors[detector.cursorNameForDestination(10)]
	require.True(t, ok, "the established destination's cursor must not be dropped by the new destination's backfill")
	require.Greater(
		t,
		establishedCursor.ToBlock,
		uint64(5_000),
		"the established destination must advance past its prior cursor in the same poll the "+
			"new destination backfills from its start block",
	)

	newCursor, ok := store.cursors[detector.cursorNameForDestination(11)]
	require.True(t, ok)
	require.Greater(
		t,
		newCursor.ToBlock,
		uint64(0),
		"the new destination must make progress from its configured start block",
	)
}

// TestL1ToL2_NewClaimerBackfillsFromStartBlock runs a bounded loop of polls and asserts that the new
// destination eventually reaches the chain head while the established destination advances on the very
// first poll (not only once the new destination's backfill catches up to it), never regresses, and never
// skips a block between two consecutive persisted windows.
func TestL1ToL2_NewClaimerBackfillsFromStartBlock(t *testing.T) {
	ctx := context.Background()
	const lastProcessedBlock = uint64(50_500)
	const blockWindow = uint64(1000)
	const initialEstablishedToBlock = uint64(50_000)

	established := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	newDestination := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: 11}}
	source := &fakeBridgeSource{lastProcessedBlock: lastProcessedBlock, found: true}
	store := newMemoryCursorStore()
	detector := newTestDetector(
		t,
		source,
		store,
		newFakeRegistry(established, newDestination),
		WithStartBlock(0),
		WithBlockWindow(blockWindow),
		WithOverlapBlocks(0),
	)
	establishedCursorName := detector.cursorNameForDestination(10)
	newCursorName := detector.cursorNameForDestination(11)
	store.cursors[establishedCursorName] = autoclaimtypes.BridgeCursor{
		FromBlock: 49_001,
		ToBlock:   initialEstablishedToBlock,
		BlockNum:  initialEstablishedToBlock,
	}

	lastEstablishedToBlock := initialEstablishedToBlock
	firstAdvancePoll := -1
	reachedHead := false
	for poll := 0; poll < 60 && !reachedHead; poll++ {
		_, err := detector.PollOnce(ctx)
		require.NoError(t, err)

		if cursor, ok := store.cursors[establishedCursorName]; ok {
			require.GreaterOrEqual(t, cursor.ToBlock, lastEstablishedToBlock, "established cursor must never regress")
			if cursor.ToBlock != lastEstablishedToBlock {
				if firstAdvancePoll == -1 {
					firstAdvancePoll = poll
				}
				require.Equal(
					t,
					lastEstablishedToBlock+1,
					cursor.FromBlock,
					"established cursor must not skip any blocks between two persisted windows",
				)
				lastEstablishedToBlock = cursor.ToBlock
			}
		}

		if cursor, ok := store.cursors[newCursorName]; ok && cursor.ToBlock >= lastProcessedBlock {
			reachedHead = true
		}
	}

	require.True(t, reachedHead, "the new destination must fully backfill to the head within the bounded loop")
	require.Equal(
		t,
		0,
		firstAdvancePoll,
		"the established destination must advance on the first poll, not wait for the new "+
			"destination's backfill to catch up to it",
	)
	require.Equal(t, lastProcessedBlock, lastEstablishedToBlock, "the established destination must reach the head")
}

// TestL1ToL2_PerDestinationCursorsAreIndependent proves that a SaveBridgeCursor failure for one
// destination does not corrupt or skip the cursor of another destination whose block window is fully
// independent. The established destination (which sorts first by destination network id) must keep its
// correctly advanced cursor even though the newly added destination's save fails afterward.
func TestL1ToL2_PerDestinationCursorsAreIndependent(t *testing.T) {
	ctx := context.Background()
	saveErr := errors.New("save cursor for destination 11 failed")

	established := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	failing := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: 11}}
	source := &fakeBridgeSource{lastProcessedBlock: 5_500, found: true}
	store := newFailingCursorStore()
	detector := newTestDetector(
		t,
		source,
		store,
		newFakeRegistry(established, failing),
		WithStartBlock(0),
		WithBlockWindow(1000),
	)
	store.cursors[detector.cursorNameForDestination(10)] = autoclaimtypes.BridgeCursor{
		FromBlock: 4_001,
		ToBlock:   5_000,
		BlockNum:  5_000,
	}
	store.failFor[detector.cursorNameForDestination(11)] = saveErr

	_, err := detector.PollOnce(ctx)
	require.ErrorIs(t, err, saveErr)

	establishedCursor, ok := store.cursors[detector.cursorNameForDestination(10)]
	require.True(t, ok, "an unrelated destination's cursor must not be skipped because another destination's save failed")
	require.Equal(
		t,
		uint64(5_500),
		establishedCursor.ToBlock,
		"an unrelated destination's cursor must not be corrupted or left stale by another destination's save failure",
	)

	_, ok = store.cursors[detector.cursorNameForDestination(11)]
	require.False(t, ok, "the failing destination's cursor must not be partially persisted")
}

// TestL1ToL2_IgnoresBridgeOutsideOwnWindowWithoutDoubleEnqueue guards against the double-enqueue hazard
// called out by the per-destination-window design: when two destinations' block windows overlap, a
// bridge fetched by a window that does not own its destination must be ignored by that window (exactly
// like an unknown destination is ignored today) and must still be enqueued exactly once by the window
// that does own it, never twice.
func TestL1ToL2_IgnoresBridgeOutsideOwnWindowWithoutDoubleEnqueue(t *testing.T) {
	ctx := context.Background()
	// Destination A's window is [0,9] and destination B's window is [5,14]: they overlap on [5,9].
	// A real bridgesync query would return this bridge for both overlapping ranges.
	overlapping := makeSyncBridge(1, autoclaimtypes.L1OriginNetwork, 11, 7, 0)
	source := &fakeBridgeSource{
		lastProcessedBlock: 14,
		found:              true,
		bridgesByRange: map[blockRange][]bridgesync.Bridge{
			{from: 0, to: 9}:  {overlapping},
			{from: 5, to: 14}: {overlapping},
		},
	}
	store := newMemoryCursorStore()
	claimerA := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: 10}}
	claimerB := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: 11}}
	detector := newTestDetector(
		t,
		source,
		store,
		newFakeRegistry(claimerA, claimerB),
		WithStartBlock(0),
		WithBlockWindow(10),
		WithOverlapBlocks(0),
	)
	store.cursors[detector.cursorNameForDestination(11)] = autoclaimtypes.BridgeCursor{
		FromBlock: 0,
		ToBlock:   4,
		BlockNum:  4,
	}

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.EnqueuedBridgeCount, "the overlapping bridge must be enqueued exactly once")
	require.Equal(
		t,
		1,
		result.IgnoredBridgeCount,
		"a bridge fetched by a window that does not own its destination must be tallied as ignored, "+
			"exactly like an unknown destination is today",
	)
	require.Empty(t, claimerA.enqueued, "the bridge does not belong to destination A's window")
	require.Len(t, claimerB.enqueued, 1, "destination B must receive exactly one enqueue, not one per overlapping window")
}

func newTestDetector(
	t *testing.T,
	source autoclaimtypes.BridgeSource,
	store CursorStore,
	registry autoclaimtypes.ClaimerRegistry,
	options ...Option,
) *L1ToL2 {
	t.Helper()

	options = append([]Option{WithNow(func() time.Time { return testNow })}, options...)
	detector, err := NewL1ToL2(source, store, registry, options...)
	require.NoError(t, err)
	return detector
}

func makeSyncBridge(
	depositCount uint32,
	originNetwork uint32,
	destinationNetwork uint32,
	blockNum uint64,
	blockPos uint64,
) bridgesync.Bridge {
	return bridgesync.Bridge{
		BlockNum:           blockNum,
		BlockPos:           blockPos,
		TxHash:             common.BigToHash(big.NewInt(int64(depositCount))),
		BlockTimestamp:     1000 + uint64(depositCount),
		LeafType:           uint8(bridgesynctypes.LeafTypeAsset),
		OriginNetwork:      originNetwork,
		OriginAddress:      common.HexToAddress("0x1000000000000000000000000000000000000001"),
		DestinationNetwork: destinationNetwork,
		DestinationAddress: common.HexToAddress("0x2000000000000000000000000000000000000002"),
		Amount:             big.NewInt(1000 + int64(depositCount)),
		Metadata:           []byte{byte(depositCount)},
		DepositCount:       depositCount,
		TxnSender:          common.HexToAddress("0x3000000000000000000000000000000000000003"),
		ToAddress:          common.HexToAddress("0x4000000000000000000000000000000000000004"),
		Source:             bridgesync.BridgeSourceForwardLET,
	}
}

type blockRange struct {
	from uint64
	to   uint64
}

type fakeBridgeSource struct {
	lastProcessedBlock uint64
	found              bool
	lastProcessedErr   error
	getBridgesErr      error
	bridgesByRange     map[blockRange][]bridgesync.Bridge
	ranges             []blockRange
}

func (s *fakeBridgeSource) GetLastProcessedBlock(_ context.Context) (uint64, bool, error) {
	if s.lastProcessedErr != nil {
		return 0, false, s.lastProcessedErr
	}
	return s.lastProcessedBlock, s.found, nil
}

func (s *fakeBridgeSource) GetBridges(_ context.Context, fromBlock, toBlock uint64) ([]bridgesync.Bridge, error) {
	s.ranges = append(s.ranges, blockRange{from: fromBlock, to: toBlock})
	if s.getBridgesErr != nil {
		return nil, s.getBridgesErr
	}
	return append([]bridgesync.Bridge(nil), s.bridgesByRange[blockRange{from: fromBlock, to: toBlock}]...), nil
}

type memoryCursorStore struct {
	cursors map[string]autoclaimtypes.BridgeCursor
}

func newMemoryCursorStore() *memoryCursorStore {
	return &memoryCursorStore{cursors: make(map[string]autoclaimtypes.BridgeCursor)}
}

func (s *memoryCursorStore) GetBridgeCursor(
	_ context.Context,
	name string,
) (*autoclaimtypes.BridgeCursor, bool, error) {
	cursor, ok := s.cursors[name]
	if !ok {
		return nil, false, nil
	}
	return &cursor, true, nil
}

func (s *memoryCursorStore) SaveBridgeCursor(
	_ context.Context,
	name string,
	cursor autoclaimtypes.BridgeCursor,
	_ time.Time,
) error {
	s.cursors[name] = cursor
	return nil
}

// failingCursorStore wraps memoryCursorStore and lets a test force SaveBridgeCursor to fail for a
// specific cursor name, to prove that one destination's save failure does not affect another's.
type failingCursorStore struct {
	*memoryCursorStore
	failFor map[string]error
}

func newFailingCursorStore() *failingCursorStore {
	return &failingCursorStore{
		memoryCursorStore: newMemoryCursorStore(),
		failFor:           make(map[string]error),
	}
}

func (s *failingCursorStore) SaveBridgeCursor(
	ctx context.Context,
	name string,
	cursor autoclaimtypes.BridgeCursor,
	now time.Time,
) error {
	if err, ok := s.failFor[name]; ok {
		return err
	}
	return s.memoryCursorStore.SaveBridgeCursor(ctx, name, cursor, now)
}

type fakeRegistry struct {
	claimers map[uint32]*fakeClaimer
	err      error
}

func newFakeRegistry(claimers ...*fakeClaimer) *fakeRegistry {
	registry := &fakeRegistry{claimers: make(map[uint32]*fakeClaimer, len(claimers))}
	for _, claimer := range claimers {
		registry.claimers[claimer.target.DestinationNetwork] = claimer
	}
	return registry
}

func (r *fakeRegistry) ClaimerForDestination(
	_ context.Context,
	destinationNetwork uint32,
) (autoclaimtypes.Claimer, bool, error) {
	if r.err != nil {
		return nil, false, r.err
	}
	claimer, ok := r.claimers[destinationNetwork]
	return claimer, ok, nil
}

func (r *fakeRegistry) Claimers(_ context.Context) ([]autoclaimtypes.Claimer, error) {
	if r.err != nil {
		return nil, r.err
	}
	destinations := make([]uint32, 0, len(r.claimers))
	for destination := range r.claimers {
		destinations = append(destinations, destination)
	}
	sort.Slice(destinations, func(i, j int) bool {
		return destinations[i] < destinations[j]
	})
	claimers := make([]autoclaimtypes.Claimer, 0, len(destinations))
	for _, destination := range destinations {
		claimers = append(claimers, r.claimers[destination])
	}
	return claimers, nil
}

type fakeClaimer struct {
	target      autoclaimtypes.ClaimerTarget
	err         error
	claimErr    error
	claimed     bool
	claimChecks []autoclaimtypes.BridgeExit
	enqueued    []autoclaimtypes.BridgeExit
	seen        map[autoclaimtypes.RequestKey]struct{}
}

func (c *fakeClaimer) Target() autoclaimtypes.ClaimerTarget {
	return c.target
}

func (c *fakeClaimer) IsClaimed(_ context.Context, bridge autoclaimtypes.BridgeExit) (bool, error) {
	c.claimChecks = append(c.claimChecks, bridge)
	if c.claimErr != nil {
		return false, c.claimErr
	}
	return c.claimed, nil
}

func (c *fakeClaimer) Enqueue(_ context.Context, bridge autoclaimtypes.BridgeExit) error {
	if c.err != nil {
		return c.err
	}
	if c.seen == nil {
		c.seen = make(map[autoclaimtypes.RequestKey]struct{})
	}
	key := autoclaimtypes.DeriveRequestKey(bridge.SourceNetwork, bridge.DestinationNetwork, bridge.DepositCount)
	if _, ok := c.seen[key]; ok {
		return nil
	}
	c.seen[key] = struct{}{}
	c.enqueued = append(c.enqueued, bridge)
	return nil
}

func (c *fakeClaimer) Advance(_ context.Context, key autoclaimtypes.RequestKey) error {
	return fmt.Errorf("unexpected advance for %s", key)
}
