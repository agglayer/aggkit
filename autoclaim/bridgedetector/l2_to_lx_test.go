package bridgedetector

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	autoclaimmetrics "github.com/agglayer/aggkit/autoclaim/metrics"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/bridgeservice"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	commonmocks "github.com/agglayer/aggkit/common/mocks"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	fakeClaimer0ID  = "claimer-0"
	fakeClaimer1ID  = "claimer-1"
	fakeClaimer10ID = "claimer-10"
	fakeClaimer11ID = "claimer-11"
	fakeSrcURL1     = "http://src1"
)

func lerHash(n int64) common.Hash {
	return common.BigToHash(big.NewInt(n))
}

func makeVerifyRow(rollupID uint32, ler common.Hash, blockNum uint64) *l1infotreesync.VerifyBatches {
	return &l1infotreesync.VerifyBatches{
		BlockNumber:   blockNum,
		BlockPosition: 0,
		RollupID:      rollupID,
		ExitRoot:      ler,
	}
}

func makeCandidate(depositCount, destinationNetwork uint32) ClaimCandidate {
	return ClaimCandidate{
		Bridge: autoclaimtypes.BridgeExit{
			BlockNum:           1000 + uint64(depositCount),
			LeafType:           bridgesynctypes.LeafTypeAsset,
			OriginNetwork:      99,
			DestinationNetwork: destinationNetwork,
			Amount:             big.NewInt(int64(depositCount)),
			DepositCount:       depositCount,
		},
	}
}

func TestL2ToLxNewLERDetectionMultipleSources(t *testing.T) {
	ctx := context.Background()
	ler1, ler2 := lerHash(11), lerHash(22)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {
				makeVerifyRow(1, lerHash(1), 10), // superseded by newer row below
				makeVerifyRow(1, ler1, 20),
				makeVerifyRow(2, ler2, 30),
			},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	fetcher.urls[2] = "http://src2"
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)
	fetcher.setPage("http://src2", 1, []ClaimCandidate{makeCandidate(6, 3)}, 1)

	claimer0 := &fakeClaimer{
		target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0, MaxRetries: 4},
	}
	claimer3 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-3", DestinationNetwork: 3}}
	lerStore := newFakePerPairLERStore()
	enqueuer := newFakeEnqueuer()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0, claimer3), newMemoryCursorStore(), lerStore, enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, result.SourceCount)
	require.Equal(t, 2, result.NewLERSourceCount)
	require.Equal(t, 2, result.ProcessedSourceCount)
	require.Equal(t, 0, result.SkippedSourceCount)
	require.Equal(t, 2, result.EnqueuedCount)
	require.True(t, result.CursorAdvanced)

	require.Len(t, enqueuer.order, 2)
	req1 := enqueuer.requests[autoclaimtypes.DeriveRequestKey(1, 0, 5)]
	require.Equal(t, uint32(1), req1.Bridge.SourceNetwork)
	require.Equal(t, ler1, req1.LER)
	require.Equal(t, uint64(20), req1.VerifyBlockNum)
	require.Equal(t, uint64(4), req1.MaxRetries)

	req2 := enqueuer.requests[autoclaimtypes.DeriveRequestKey(2, 3, 6)]
	require.Equal(t, uint32(2), req2.Bridge.SourceNetwork)
	require.Equal(t, ler2, req2.LER)
	require.Equal(t, uint64(30), req2.VerifyBlockNum)

	require.Equal(t, ler1, lerStore.cursors[pairKey(1, 0)].LastLER)
	require.Equal(t, uint64(20), lerStore.cursors[pairKey(1, 0)].LastVerifyBlockNum)
	require.Equal(t, ler1, lerStore.cursors[pairKey(1, 3)].LastLER)
	require.Equal(t, ler2, lerStore.cursors[pairKey(2, 3)].LastLER)
}

func TestL2ToLxInitialCursorFromStartL1Block(t *testing.T) {
	ctx := context.Background()
	newLER := lerHash(9)
	rer := lerHash(555)
	initialLER := lerHash(3)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 100,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 40, to: 100}: {makeVerifyRow(1, newLER, 60)},
		},
		latestLeaf:     &l1infotreesync.L1InfoTreeLeaf{BlockNumber: 40, RollupExitRoot: rer},
		localExitRoots: map[uint32]common.Hash{1: initialLER},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), newFakePerPairLERStore(), newFakeEnqueuer(),
		WithL2ToLxStartL1Block(40), WithL2ToLxBlockWindow(100),
	)

	_, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Len(t, fetcher.queries, 1)
	require.NotNil(t, fetcher.queries[0].FromLER)
	require.Equal(t, initialLER, *fetcher.queries[0].FromLER)
	require.Equal(t, newLER, fetcher.queries[0].ToLER)
}

func TestL2ToLxInitialCursorZeroLERExplicitGenesisOmitsFromLER(t *testing.T) {
	// StartL1Block=0 is explicit operator intent to scan full history; it must never reach
	// initialFromLER's LER lookup at all.
	ctx := context.Background()
	newLER := lerHash(9)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 100,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 99}: {makeVerifyRow(1, newLER, 60)},
		},
		latestLeaf:     &l1infotreesync.L1InfoTreeLeaf{BlockNumber: 40, RollupExitRoot: lerHash(555)},
		localExitRoots: map[uint32]common.Hash{1: {}}, // network had no LER yet at StartL1Block
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), newFakePerPairLERStore(), newFakeEnqueuer(),
		WithL2ToLxStartL1Block(0), WithL2ToLxBlockWindow(100),
	)

	_, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Len(t, fetcher.queries, 1)
	require.Nil(t, fetcher.queries[0].FromLER, "explicit StartL1Block=0 must omit from_ler (full history)")
}

func TestL2ToLxInitialCursorZeroLERWithNonZeroStartBlockRetriesInsteadOfFallingBack(t *testing.T) {
	// A non-zero, non-explicit-genesis StartL1Block that resolves to a zero LER used to silently
	// fall back to full history. It must now retry the source instead of fetching candidates.
	ctx := context.Background()
	newLER := lerHash(9)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 100,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 40, to: 100}: {makeVerifyRow(1, newLER, 60)},
		},
		latestLeaf:     &l1infotreesync.L1InfoTreeLeaf{BlockNumber: 40, RollupExitRoot: lerHash(555)},
		localExitRoots: map[uint32]common.Hash{1: {}}, // network had no LER yet at StartL1Block
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), newFakePerPairLERStore(), newFakeEnqueuer(),
		WithL2ToLxStartL1Block(40), WithL2ToLxBlockWindow(100),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err, "an unresolvable initial LER must not fail the whole poll")
	require.Empty(t, fetcher.queries, "must not silently fetch full history")
	require.Equal(t, 1, result.SkippedSourceCount)
	require.Equal(t, 0, result.ProcessedSourceCount)
}

func TestL2ToLxFinderMissSkipsSourceWithoutAdvancingCursor(t *testing.T) {
	ctx := context.Background()
	ler1, ler2 := lerHash(11), lerHash(22)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {
				makeVerifyRow(1, ler1, 20),
				makeVerifyRow(2, ler2, 30),
			},
		},
	}
	fetcher := newFakeFetcher()
	// Source 1 has no resolvable URL; source 2 does.
	fetcher.urls[2] = "http://src2"
	fetcher.setPage("http://src2", 1, []ClaimCandidate{makeCandidate(6, 0)}, 1)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	lerStore := newFakePerPairLERStore()
	enqueuer := newFakeEnqueuer()
	cursorStore := newMemoryCursorStore()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), cursorStore, lerStore, enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.SkippedSourceCount)
	require.Equal(t, 1, result.ProcessedSourceCount)
	require.Equal(t, 1, result.EnqueuedCount)

	_, ok := lerStore.cursors[pairKey(1, 0)]
	require.False(t, ok, "skipped source 1 must not advance its LER cursor")
	require.Equal(t, ler2, lerStore.cursors[pairKey(2, 0)].LastLER, "source 2 processed independently")
	require.True(t, result.CursorAdvanced)
	require.Equal(t, uint64(19), cursorStore.cursors[defaultL2ToLxCursorName].ToBlock,
		"block cursor must hold before the skipped source's verify row (block 20)")
}

func TestL2ToLxNotSyncedSkipsSourceWithoutAdvancingCursor(t *testing.T) {
	ctx := context.Background()
	ler1 := lerHash(11)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(1, ler1, 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, nil, 0)
	fetcher.pageErr[fetchKey{url: fakeSrcURL1, page: 1}] = ErrCandidatesNotSynced
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	lerStore := newFakePerPairLERStore()
	cursorStore := newMemoryCursorStore()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), cursorStore, lerStore, newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err, "not-synced is retry-later, not a hard error")
	require.Equal(t, 1, result.SkippedSourceCount)
	require.Equal(t, 0, result.EnqueuedCount)
	_, ok := lerStore.cursors[pairKey(1, 0)]
	require.False(t, ok)
	require.True(t, result.CursorAdvanced)
	require.Equal(t, uint64(19), cursorStore.cursors[defaultL2ToLxCursorName].ToBlock,
		"block cursor must hold before the skipped source's verify row (block 20)")
}

func TestL2ToLxRetriesSkippedSourceOnLaterPollWithoutNewLER(t *testing.T) {
	// A source skipped for a transient reason (here: finder miss) must be retried on a later poll
	// even if it never publishes another LER: the block-window cursor is held before the skipped
	// verify row, so the row is re-observed once the skip condition clears.
	ctx := context.Background()
	ler1 := lerHash(11)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}:  {makeVerifyRow(1, ler1, 20)},
			{from: 19, to: 50}: {makeVerifyRow(1, ler1, 20)},
		},
	}
	fetcher := newFakeFetcher() // no URL for source 1 yet
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	lerStore := newFakePerPairLERStore()
	enqueuer := newFakeEnqueuer()
	cursorStore := newMemoryCursorStore()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), cursorStore, lerStore, enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.SkippedSourceCount)
	require.Equal(t, uint64(19), cursorStore.cursors[defaultL2ToLxCursorName].ToBlock)

	// The source's bridge service URL becomes resolvable; the next poll re-observes the same
	// verify row and processes the source, with no new LER published in between.
	fetcher.urls[1] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)

	result, err = detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.ProcessedSourceCount)
	require.Equal(t, 1, result.EnqueuedCount)
	require.Equal(t, ler1, lerStore.cursors[pairKey(1, 0)].LastLER)
	require.Equal(t, uint64(50), cursorStore.cursors[defaultL2ToLxCursorName].ToBlock,
		"block cursor catches up once the skipped source is processed")
}

func TestL2ToLxRetrySkipAtWindowStartKeepsStoredCursor(t *testing.T) {
	// When the retried row sits at the very start of the window there is no forward progress to
	// record: the stored cursor must stay untouched (not move backward) and the poll must not error.
	ctx := context.Background()
	ler1 := lerHash(11)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 20, to: 50}: {makeVerifyRow(1, ler1, 20)},
		},
	}
	fetcher := newFakeFetcher() // no URL for source 1
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	cursorStore := newMemoryCursorStore()
	cursorStore.cursors[defaultL2ToLxCursorName] = autoclaimtypes.BridgeCursor{
		FromBlock: 0, ToBlock: 20, BlockNum: 20,
	}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), cursorStore, newFakePerPairLERStore(), newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50), WithL2ToLxOverlapBlocks(1),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.SkippedSourceCount)
	require.False(t, result.CursorAdvanced)
	require.Equal(t, uint64(20), cursorStore.cursors[defaultL2ToLxCursorName].ToBlock,
		"stored cursor stays put so the row at block 20 keeps being re-observed")
}

// disabledURLErr builds the error a real ServiceFetcher returns for a network the bridge service
// finder was configured to ignore, wrapped exactly the way ServiceFetcher.GetURL wraps it (see
// TestServiceFetcherGetURL): the permanent ErrSourceDisabled over the detector-local ErrURLNotFound
// over the finder's own error.
func disabledURLErr(sourceID uint32) error {
	finderErr := fmt.Errorf("%w: network %d", bridgeservicefinder.ErrNetworkDisabled, sourceID)
	return fmt.Errorf("%w: %w: source %d: %w", ErrSourceDisabled, ErrURLNotFound, sourceID, finderErr)
}

func TestL2ToLxDisabledSourceDoesNotHoldBlockCursor(t *testing.T) {
	// A source network excluded from bridge service resolution can never resolve a URL, so it must
	// count as "nothing to do", not as a retry: the block-window cursor is shared by every source and
	// holding it for this row would pin the detector on the same window forever.
	ctx := context.Background()
	const disabledSource uint32 = 7001
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(disabledSource, lerHash(11), 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urlErr[disabledSource] = disabledURLErr(disabledSource)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	lerStore := newFakePerPairLERStore()
	cursorStore := newMemoryCursorStore()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), cursorStore, lerStore, newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.ProcessedSourceCount)
	require.Equal(t, 1, result.SkippedSourceCount)
	require.True(t, result.CursorAdvanced, "a permanently disabled source must not block the window")
	require.Equal(t, uint64(49), cursorStore.cursors[defaultL2ToLxCursorName].ToBlock,
		"cursor must advance past the disabled source's verify row (block 20), not stop before it")
	_, ok := lerStore.cursors[pairKey(disabledSource, 0)]
	require.False(t, ok, "the disabled source's LER cursor stays untouched, so re-enabling it loses nothing")
}

func TestL2ToLxTransientURLErrorStillHoldsBlockCursor(t *testing.T) {
	// The companion of the test above: a URL error that is NOT a permanent exclusion (here, a plain
	// finder miss for a bridge service that has not announced itself yet) can still clear on its own,
	// so it must keep holding the window just before its verify row.
	ctx := context.Background()
	const transientSource uint32 = 7002
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(transientSource, lerHash(11), 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urlErr[transientSource] = fmt.Errorf("%w: source %d: %w",
		ErrURLNotFound, transientSource, bridgeservicefinder.ErrURLNotFound)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	cursorStore := newMemoryCursorStore()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), cursorStore, newFakePerPairLERStore(), newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.SkippedSourceCount)
	require.True(t, result.CursorAdvanced)
	require.Equal(t, uint64(19), cursorStore.cursors[defaultL2ToLxCursorName].ToBlock,
		"a retryable url miss must still hold the window before its verify row (block 20)")
}

func TestL2ToLxDisabledSourceDoesNotStarveHealthySource(t *testing.T) {
	// Mixed window: a disabled source's row comes first, a healthy source's row later. The healthy
	// source must be processed and the window must advance past both.
	ctx := context.Background()
	const (
		disabledSource uint32 = 7001
		healthySource  uint32 = 7003
	)
	healthyLER := lerHash(22)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {
				makeVerifyRow(disabledSource, lerHash(11), 20),
				makeVerifyRow(healthySource, healthyLER, 30),
			},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urlErr[disabledSource] = disabledURLErr(disabledSource)
	fetcher.urls[healthySource] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	lerStore := newFakePerPairLERStore()
	enqueuer := newFakeEnqueuer()
	cursorStore := newMemoryCursorStore()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), cursorStore, lerStore, enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.ProcessedSourceCount)
	require.Equal(t, 1, result.EnqueuedCount)
	require.Len(t, enqueuer.order, 1, "the healthy source's candidate must actually be enqueued")
	require.Equal(t, healthyLER, lerStore.cursors[pairKey(healthySource, 0)].LastLER)
	require.True(t, result.CursorAdvanced)
	require.Equal(t, uint64(49), cursorStore.cursors[defaultL2ToLxCursorName].ToBlock)
}

func TestL2ToLxDisabledSourceAtWindowStartDoesNotPinCursor(t *testing.T) {
	// The production regression: in a deployment whose ignore list covers most registered rollups,
	// the earliest verify row in the window belonged to an ignored network. Treating it as a retry
	// held the shared cursor at or before the start of the window, so PollOnce returned without
	// saving anything and re-scanned the identical window on every poll, indefinitely -- and no
	// L2-origin claim request was ever created for ANY source.
	ctx := context.Background()
	const (
		disabledSource uint32 = 7001
		healthySource  uint32 = 7003
	)
	healthyLER := lerHash(22)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 20, to: 50}: {
				makeVerifyRow(disabledSource, lerHash(11), 20),
				makeVerifyRow(healthySource, healthyLER, 40),
			},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urlErr[disabledSource] = disabledURLErr(disabledSource)
	fetcher.urls[healthySource] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	lerStore := newFakePerPairLERStore()
	enqueuer := newFakeEnqueuer()
	cursorStore := newMemoryCursorStore()
	cursorStore.cursors[defaultL2ToLxCursorName] = autoclaimtypes.BridgeCursor{
		FromBlock: 0, ToBlock: 20, BlockNum: 20,
	}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), cursorStore, lerStore, enqueuer,
		WithL2ToLxBlockWindow(50), WithL2ToLxOverlapBlocks(1),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.True(t, result.CursorAdvanced,
		"the ignored row at the window start must not send PollOnce down the return-without-saving path")
	require.Equal(t, uint64(50), cursorStore.cursors[defaultL2ToLxCursorName].ToBlock,
		"the window must move on instead of being re-scanned forever")
	require.Equal(t, 1, result.EnqueuedCount, "the healthy source behind the ignored row must be reached")
	require.Equal(t, healthyLER, lerStore.cursors[pairKey(healthySource, 0)].LastLER)
}

func TestL2ToLxDisabledSourceIsReportedOncePerSource(t *testing.T) {
	// The exclusion never clears, and the detector polls for the lifetime of the process, so the
	// report belongs at most once per source rather than on every poll.
	ctx := context.Background()
	const disabledSource uint32 = 7001
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}:  {makeVerifyRow(disabledSource, lerHash(11), 20)},
			{from: 48, to: 50}: {makeVerifyRow(disabledSource, lerHash(11), 50)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urlErr[disabledSource] = disabledURLErr(disabledSource)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	logger := commonmocks.NewLogger(t)
	logger.EXPECT().Infof(
		mock.MatchedBy(func(format string) bool { return strings.Contains(format, "is disabled") }),
		disabledSource,
	).Once()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), newFakePerPairLERStore(),
		newFakeEnqueuer(), WithL2ToLxBlockWindow(50), WithL2ToLxOverlapBlocks(1), WithL2ToLxLogger(logger),
	)

	for range 2 {
		_, err := detector.PollOnce(ctx)
		require.NoError(t, err)
	}
	// logger.AssertExpectations runs on cleanup: a second Infof call would fail the test as
	// unexpected, and zero calls would fail the Once() expectation.
}

func TestL2ToLxBatchesDestinationNetworkIDs(t *testing.T) {
	// The bridge service rejects claim-candidates requests with more than bridgeservice.MaxNetworkIDs
	// destination IDs, so the detector must partition the destination filter into batches.
	ctx := context.Background()
	ler1 := lerHash(11)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(1, ler1, 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, nil, 0)
	// Seven destinations besides source 1: 0 and 2..7.
	claimers := []*fakeClaimer{
		{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}},
	}
	for destination := uint32(2); destination <= 7; destination++ {
		claimers = append(claimers, &fakeClaimer{
			target: autoclaimtypes.ClaimerTarget{ID: fmt.Sprintf("claimer-%d", destination), DestinationNetwork: destination},
		})
	}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimers...), newMemoryCursorStore(), newFakePerPairLERStore(), newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	_, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Len(t, fetcher.queries, 2, "seven destinations must be split into two batches")
	require.Equal(t, []uint32{0, 2, 3, 4, 5}, fetcher.queries[0].DestinationNetworkIDs)
	require.Equal(t, []uint32{6, 7}, fetcher.queries[1].DestinationNetworkIDs)
	for _, query := range fetcher.queries {
		require.LessOrEqual(t, len(query.DestinationNetworkIDs), bridgeservice.MaxNetworkIDs)
	}
}

// stubBatchFetcher returns a fixed set of candidates keyed by the exact destination-ID batch it is
// queried with, regardless of from_ler/page. It exists specifically to prove fetchAllCandidates
// merges every batch's results: TestL2ToLxBatchesDestinationNetworkIDs above only ever stubs an empty
// page shared by every batch (via fakeFetcher.setPage, keyed by (url, page) alone), so it cannot tell
// the two batches' results apart and cannot catch "all = batch" silently dropping every batch but the
// last.
type stubBatchFetcher struct {
	url        string
	candidates map[string][]ClaimCandidate // keyed by fmt.Sprint(query.DestinationNetworkIDs)
}

func (f *stubBatchFetcher) GetURL(uint32) (string, error) { return f.url, nil }

func (f *stubBatchFetcher) GetClaimCandidates(
	_ context.Context, query ClaimCandidatesQuery,
) ([]ClaimCandidate, int, error) {
	candidates := f.candidates[fmt.Sprint(query.DestinationNetworkIDs)]
	return candidates, len(candidates), nil
}

// TestL2ToLxFetchAllCandidatesMergesAcrossBatches proves fetchAllCandidates's cross-batch merge:
// with more destinations than bridgeservice.MaxNetworkIDs, the query is split into several batches
// (TestL2ToLxBatchesDestinationNetworkIDs already pins that split), and the final result must contain
// every batch's candidates, not just the last batch's. Changing "all = append(all, batch...)" to
// "all = batch" leaves every earlier batch's candidates behind while the rest of the suite stays
// green, since no other test gives two batches distinguishable non-empty results.
func TestL2ToLxFetchAllCandidatesMergesAcrossBatches(t *testing.T) {
	ctx := context.Background()
	destinationIDs := []uint32{0, 1, 2, 3, 4, 5, 6} // 7 IDs: batches of 5 and 2 (MaxNetworkIDs = 5)
	batch1 := destinationIDs[0:5]
	batch2 := destinationIDs[5:7]
	candidateFromBatch1 := makeCandidate(1, 0)
	candidateFromBatch2 := makeCandidate(2, 6)

	fetcher := &stubBatchFetcher{
		url: fakeSrcURL1,
		candidates: map[string][]ClaimCandidate{
			fmt.Sprint(batch1): {candidateFromBatch1},
			fmt.Sprint(batch2): {candidateFromBatch2},
		},
	}
	detector := newTestL2ToLxDetector(
		t, &fakeVerifiedBatchSource{}, fetcher, newFakeRegistry(), newMemoryCursorStore(),
		newFakePerPairLERStore(), newFakeEnqueuer(),
	)

	all, err := detector.fetchAllCandidates(ctx, fakeSrcURL1, destinationIDs, nil, common.Hash{})
	require.NoError(t, err)
	require.ElementsMatch(t, []ClaimCandidate{candidateFromBatch1, candidateFromBatch2}, all,
		"fetchAllCandidates must merge every batch's candidates, not keep only the last batch's")
}

func TestL2ToLxInitialCursorLeafNotFoundExplicitGenesisOmitsFromLER(t *testing.T) {
	// StartL1Block=0 is explicit operator intent to scan full history; it must never reach
	// initialFromLER's leaf lookup at all.
	ctx := context.Background()
	newLER := lerHash(9)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 100,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 99}: {makeVerifyRow(1, newLER, 60)},
		},
		latestLeafErr: l1infotreesync.ErrNotFound,
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), newFakePerPairLERStore(), newFakeEnqueuer(),
		WithL2ToLxStartL1Block(0), WithL2ToLxBlockWindow(100),
	)

	_, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Len(t, fetcher.queries, 1)
	require.Nil(t, fetcher.queries[0].FromLER, "explicit StartL1Block=0 must omit from_ler (full history)")
}

func TestL2ToLxInitialCursorLeafNotFoundWithNonZeroStartBlockRetriesInsteadOfFallingBack(t *testing.T) {
	// A non-zero, non-explicit-genesis StartL1Block that predates the first L1 info tree leaf used
	// to silently fall back to full history. It must now retry the source instead.
	ctx := context.Background()
	newLER := lerHash(9)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 100,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 40, to: 100}: {makeVerifyRow(1, newLER, 60)},
		},
		latestLeafErr: l1infotreesync.ErrNotFound,
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), newFakePerPairLERStore(), newFakeEnqueuer(),
		WithL2ToLxStartL1Block(40), WithL2ToLxBlockWindow(100),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err, "an unresolvable initial LER must not fail the whole poll")
	require.Empty(t, fetcher.queries, "must not silently fetch full history")
	require.Equal(t, 1, result.SkippedSourceCount)
	require.Equal(t, 0, result.ProcessedSourceCount)
}

func TestL2ToLxPaginationAndDedup(t *testing.T) {
	ctx := context.Background()
	ler1 := lerHash(11)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(1, ler1, 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	// Total count is 3; page 1 has two candidates (one duplicated), page 2 has the third.
	// Pagination is 1-based to match the bridge service /claim-candidates endpoint.
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0), makeCandidate(5, 0)}, 3)
	fetcher.setPage(fakeSrcURL1, 2, []ClaimCandidate{makeCandidate(6, 0)}, 3)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	enqueuer := newFakeEnqueuer()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), newFakePerPairLERStore(), enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Len(t, fetcher.queries, 2, "must page until all candidates fetched")
	require.Equal(t, uint32(1), fetcher.queries[0].PageNumber, "pagination must be 1-based")
	require.Equal(t, uint32(2), fetcher.queries[1].PageNumber)
	require.Equal(t, 3, result.CandidateCount)
	require.Equal(t, 2, result.EnqueuedCount, "duplicate deposit-count candidate is deduped by the enqueuer")
	require.Len(t, enqueuer.order, 2)
}

func TestL2ToLxAlreadyClaimedSkip(t *testing.T) {
	ctx := context.Background()
	ler1 := lerHash(11)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(1, ler1, 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)
	claimer0 := &fakeClaimer{
		target:  autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0},
		claimed: true,
	}
	lerStore := newFakePerPairLERStore()
	enqueuer := newFakeEnqueuer()
	log := &capturingLogger{}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), lerStore, enqueuer,
		WithL2ToLxBlockWindow(50), WithL2ToLxLogger(log),
	)

	prometheus.Init()
	autoclaimmetrics.Register()
	vec, ok := prometheus.CounterVec("detector_skipped_already_claimed_total")
	require.True(t, ok)
	skipped := vec.WithLabelValues(string(autoclaimmetrics.DetectorL2ToLx), "0")
	before := testutil.ToFloat64(skipped)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.AlreadyClaimedCount)
	require.Equal(t, before+1, testutil.ToFloat64(skipped))
	require.Contains(t, log.debugs,
		"autoclaim l2_to_lx bridge detector: skipped bridge 1:0:5 (destination 0, global index unknown): "+
			"already claimed on target")
	require.Empty(t, log.warns)
	require.Equal(t, 0, result.EnqueuedCount)
	require.Empty(t, enqueuer.order)
	require.Len(t, claimer0.claimChecks, 1)
	require.Equal(t, ler1, lerStore.cursors[pairKey(1, 0)].LastLER,
		"an already-claimed candidate still fully processes the source")
}

func TestL2ToLxLERCursorAdvancedOnlyAfterFullSuccess(t *testing.T) {
	ctx := context.Background()
	enqueueErr := errors.New("enqueue exploded")
	ler1 := lerHash(11)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(1, ler1, 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	lerStore := newFakePerPairLERStore()
	enqueuer := newFakeEnqueuer()
	enqueuer.err = enqueueErr
	cursorStore := newMemoryCursorStore()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), cursorStore, lerStore, enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	_, err := detector.PollOnce(ctx)
	require.ErrorIs(t, err, enqueueErr)
	require.Empty(t, lerStore.cursors, "LER cursor must not advance on enqueue failure")
	require.Empty(t, cursorStore.cursors, "block cursor must not advance on hard failure")
}

func TestL2ToLxDestinationListExcludesSource(t *testing.T) {
	ctx := context.Background()
	ler1 := lerHash(11)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(1, ler1, 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)
	// Claimers for destinations 0 and 1; source is 1, so 1 must be excluded from the query.
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	claimer1 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer1ID, DestinationNetwork: 1}}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0, claimer1), newMemoryCursorStore(), newFakePerPairLERStore(), newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	_, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Len(t, fetcher.queries, 1)
	require.Equal(t, []uint32{0}, fetcher.queries[0].DestinationNetworkIDs)
}

// TestL2ToLxOnlyDestinationIsSourceSkipsWithoutCursorWrite covers the behaviour change of issue
// #1651: with LER cursors keyed by (source, destination), a source whose only enabled claimer is
// the source itself has no pair to record, so there is nothing to fetch and
// nothing to store. It is simply reported as up to date (skipped) and re-evaluated next poll, which
// costs not even a cursor read. The block-window cursor still advances: this is not a retry.
func TestL2ToLxOnlyDestinationIsSourceSkipsWithoutCursorWrite(t *testing.T) {
	ctx := context.Background()
	ler1 := lerHash(11)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(1, ler1, 20)},
		},
	}
	fetcher := newFakeFetcher()
	// Only claimer is destination 1, which equals the single source 1: nothing to claim.
	claimer1 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer1ID, DestinationNetwork: 1}}
	lerStore := newFakePerPairLERStore()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer1), newMemoryCursorStore(), lerStore, newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Empty(t, fetcher.queries, "no fetch when there is no destination other than the source")
	require.Equal(t, 0, result.ProcessedSourceCount)
	require.Equal(t, 1, result.SkippedSourceCount)
	require.Equal(t, 0, result.NewLERSourceCount)
	require.Empty(t, lerStore.cursors, "there is no (source, destination) pair to write a cursor for")
	require.Empty(t, lerStore.seeded, "a source with no destination pair must not consume its legacy row")
	require.True(t, result.CursorAdvanced, "an up-to-date source is not a retry: the window still advances")
}

func TestL2ToLxSkipsAlreadyProcessedLER(t *testing.T) {
	ctx := context.Background()
	ler1 := lerHash(11)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(1, ler1, 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[1] = fakeSrcURL1
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	lerStore := newFakePerPairLERStore()
	lerStore.set(1, 0, autoclaimtypes.LERCursor{
		SourceNetwork: 1, DestinationNetwork: 0, LastLER: ler1, LastVerifyBlockNum: 20,
	})
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), lerStore, newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.SourceCount)
	require.Equal(t, 0, result.NewLERSourceCount, "the source's newest LER already matches its cursor")
	require.Empty(t, fetcher.queries)
}

// --- issue #1651: per-destination LER cursor ---
//
// TestL2ToLx_DestinationAddedLaterBackfillsItsOwnHistory guards the permanent-loss bug issue #1651
// fixes: with LER cursors keyed by (source, destination), a destination added after the source's
// other destinations have already advanced gets its own baseline query instead of inheriting an
// already-advanced shared cursor, so a pre-existing bridge to it is still fetched and enqueued.
func TestL2ToLx_DestinationAddedLaterBackfillsItsOwnHistory(t *testing.T) {
	ctx := context.Background()
	const (
		sourceID uint32 = 1
		destA    uint32 = 10 // established destination: already advanced to lerX
		destB    uint32 = 11 // newly added destination: no history fetched yet
	)
	lerX := lerHash(20) // today's single per-source cursor value (really "destination A's progress")
	lerY := lerHash(30) // newest LER observed this poll

	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(sourceID, lerY, 40)},
		},
	}

	oldCandidateB := makeCandidate(1, destB) // predates lerX: only reachable via B's own baseline fetch
	newCandidateA := makeCandidate(2, destA) // reachable from lerX onward

	fetcher := newFakeFetcher()
	fetcher.urls[sourceID] = fakeSrcURL1
	// A query anchored at the established from_ler=lerX only returns candidates from lerX onward (the
	// real bridge service would never return the older bridge to B for such a query).
	fetcher.setPageForLER(fakeSrcURL1, &lerX, 1, []ClaimCandidate{newCandidateA}, 1)
	// A query anchored at destination B's own baseline (nil = full history) returns B's full history,
	// including the bridge that predates lerX.
	fetcher.setPageForLER(fakeSrcURL1, nil, 1, []ClaimCandidate{oldCandidateB}, 1)

	claimerA := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: destA}}
	claimerB := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: destB}}

	lerStore := newFakePerPairLERStore()
	// Destination A is established at lerX; destination B was added later and has no cursor row of
	// its own, which is exactly the shape the pre-0003 per-source cursor collapsed everything into.
	lerStore.set(sourceID, destA, autoclaimtypes.LERCursor{
		SourceNetwork: sourceID, DestinationNetwork: destA, LastLER: lerX, LastVerifyBlockNum: 20,
	})
	enqueuer := newFakeEnqueuer()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimerA, claimerB), newMemoryCursorStore(), lerStore, enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	_, err := detector.PollOnce(ctx)
	require.NoError(t, err)

	require.Len(t, fetcher.queries, 2,
		"destination 11 (added after the shared cursor advanced) must get its own query with its "+
			"own baseline from_ler, grouped separately from destination 10's advanced from_ler")
	for _, query := range fetcher.queries {
		if len(query.DestinationNetworkIDs) == 1 && query.DestinationNetworkIDs[0] == destA {
			require.NotNil(t, query.FromLER, "destination 10's query must keep its own advanced from_ler")
			require.Equal(t, lerX, *query.FromLER)
		}
	}

	key := autoclaimtypes.DeriveRequestKey(sourceID, destB, oldCandidateB.Bridge.DepositCount)
	_, enqueued := enqueuer.requests[key]
	require.True(t, enqueued,
		"the pre-existing bridge to destination 11 must be backfilled from its own baseline, not "+
			"permanently lost (issue #1651)")
}

// pairKey is the (source, destination) key fakePerPairLERStore stores its cursors under.
func pairKey(sourceNetwork, destinationNetwork uint32) [2]uint32 {
	return [2]uint32{sourceNetwork, destinationNetwork}
}

// fakePerPairLERStore is a test-only implementation of the per-(source, destination) LERCursorStore
// declared in l2_to_lx.go: one cursor row per pair, so two destinations of the same source can sit at
// two independent LERs.
type fakePerPairLERStore struct {
	cursors  map[[2]uint32]autoclaimtypes.LERCursor
	saveErr  error
	seeded   []uint32
	seedFrom *autoclaimtypes.LERCursor
	seedErr  error
}

func newFakePerPairLERStore() *fakePerPairLERStore {
	return &fakePerPairLERStore{cursors: make(map[[2]uint32]autoclaimtypes.LERCursor)}
}

func (s *fakePerPairLERStore) set(sourceNetwork, destinationNetwork uint32, cursor autoclaimtypes.LERCursor) {
	s.cursors[pairKey(sourceNetwork, destinationNetwork)] = cursor
}

func (s *fakePerPairLERStore) GetLERCursor(
	_ context.Context, sourceNetwork, destinationNetwork uint32,
) (*autoclaimtypes.LERCursor, bool, error) {
	cursor, ok := s.cursors[pairKey(sourceNetwork, destinationNetwork)]
	if !ok {
		return nil, false, nil
	}
	return &cursor, true, nil
}

func (s *fakePerPairLERStore) SaveLERCursor(
	_ context.Context, sourceNetwork, destinationNetwork uint32, cursor autoclaimtypes.LERCursor, _ time.Time,
) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.cursors[pairKey(sourceNetwork, destinationNetwork)] = cursor
	return nil
}

// SeedLERCursorsFromLegacy mirrors the storage implementation: with a
// parked legacy cursor it inserts one row per destination without overwriting an existing pair, then
// forgets the legacy row so it seeds exactly once. seeded records the destination sets it was called
// with, so tests can assert the call site.
func (s *fakePerPairLERStore) SeedLERCursorsFromLegacy(
	_ context.Context, sourceNetwork uint32, destinationNetworks []uint32, _ time.Time,
) (bool, error) {
	s.seeded = append(s.seeded, destinationNetworks...)
	if s.seedErr != nil {
		return false, s.seedErr
	}
	if s.seedFrom == nil {
		return false, nil
	}
	for _, destination := range destinationNetworks {
		key := pairKey(sourceNetwork, destination)
		if _, ok := s.cursors[key]; ok {
			continue
		}
		s.cursors[key] = autoclaimtypes.LERCursor{
			SourceNetwork:      sourceNetwork,
			DestinationNetwork: destination,
			LastLER:            s.seedFrom.LastLER,
			LastVerifyBlockNum: s.seedFrom.LastVerifyBlockNum,
		}
	}
	s.seedFrom = nil
	return true, nil
}

// TestL2ToLx_EstablishedDestinationDoesNotRefetchHistory is the converse guard of
// TestL2ToLx_DestinationAddedLaterBackfillsItsOwnHistory: two destinations already established at
// DIFFERENT per-pair LERs must each resolve their own advanced from_ler, never nil/full-history, even
// though a newer LER now needs to be fetched for both. This relies on resolveFromLER's per-pair
// signature (source, destinationIDs) -> ([]pendingDestination, error), which resolves each
// destination's from_ler independently against its own stored cursor.
func TestL2ToLx_EstablishedDestinationDoesNotRefetchHistory(t *testing.T) {
	ctx := context.Background()
	const (
		sourceID uint32 = 1
		destA    uint32 = 10
		destB    uint32 = 11
	)
	lerXA := lerHash(20) // destination A's own established cursor
	lerXB := lerHash(21) // destination B's own established cursor (deliberately different from A's)
	lerY := lerHash(30)  // newest LER observed this poll

	lerStore := newFakePerPairLERStore()
	lerStore.set(sourceID, destA, autoclaimtypes.LERCursor{
		SourceNetwork: sourceID, DestinationNetwork: destA, LastLER: lerXA, LastVerifyBlockNum: 20,
	})
	lerStore.set(sourceID, destB, autoclaimtypes.LERCursor{
		SourceNetwork: sourceID, DestinationNetwork: destB, LastLER: lerXB, LastVerifyBlockNum: 21,
	})

	detector := newTestL2ToLxDetector(
		t, &fakeVerifiedBatchSource{lastProcessedBlock: 50}, newFakeFetcher(),
		newFakeRegistry(), newMemoryCursorStore(), lerStore, newFakeEnqueuer(),
	)

	source := sourceLER{sourceID: sourceID, ler: lerY, verifyNum: 40}
	pending, err := detector.resolveFromLER(ctx, source, []uint32{destA, destB})
	require.NoError(t, err)
	require.Len(t, pending, 2)

	byDest := make(map[uint32]*common.Hash, len(pending))
	for _, p := range pending {
		byDest[p.destination] = p.fromLER
	}
	require.NotNil(t, byDest[destA])
	require.Equal(t, lerXA, *byDest[destA],
		"destination 10 must resolve from its own established cursor, never full history")
	require.NotNil(t, byDest[destB])
	require.Equal(t, lerXB, *byDest[destB],
		"destination 11 must resolve from its own established cursor, never full history")
}

// TestL2ToLx_PerPairCursorAdvancesIndependently: one destination's fetch group failing with
// ErrCandidatesNotSynced must not advance that pair's LER cursor, and must not prevent the other,
// successfully fetched pair's cursor from advancing. This calls advanceLERCursors directly with only
// the succeeded group's destinations, the way processLERGroups does per group -- it does not exercise
// processLERGroups' own loop (whether a failing group's "continue" ever aborts a later group); see
// TestL2ToLxProcessLERGroups_NotSyncedGroupDoesNotBlockLaterGroups and
// TestL2ToLxProcessLERGroups_EnqueueFailureDoesNotBlockLaterGroups below for that.
func TestL2ToLx_PerPairCursorAdvancesIndependently(t *testing.T) {
	ctx := context.Background()
	const (
		sourceID uint32 = 1
		destOK   uint32 = 10
		destFail uint32 = 11
	)
	ler := lerHash(30)
	lerStore := newFakePerPairLERStore()
	detector := newTestL2ToLxDetector(
		t, &fakeVerifiedBatchSource{lastProcessedBlock: 50}, newFakeFetcher(),
		newFakeRegistry(), newMemoryCursorStore(), lerStore, newFakeEnqueuer(),
	)

	source := sourceLER{sourceID: sourceID, ler: ler, verifyNum: 40}
	// Only destOK's group succeeded this poll; destFail's group hit ErrCandidatesNotSynced upstream in
	// processSource and must be excluded from this call so its pair cursor is left untouched.
	err := detector.advanceLERCursors(ctx, source, []uint32{destOK})
	require.NoError(t, err)

	okCursor, found, err := lerStore.GetLERCursor(ctx, sourceID, destOK)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, ler, okCursor.LastLER)

	_, found, err = lerStore.GetLERCursor(ctx, sourceID, destFail)
	require.NoError(t, err)
	require.False(t, found, "the failed destination's pair cursor must not advance")
}

// TestL2ToLxProcessLERGroups_NotSyncedGroupDoesNotBlockLaterGroups exercises processLERGroups' actual
// loop (via PollOnce), unlike TestL2ToLx_PerPairCursorAdvancesIndependently above, which calls
// advanceLERCursors directly and so never runs the loop that decides whether one group's failure
// aborts the rest. This is processLERGroups' headline guarantee (see its doc comment): a group is
// fully independent, so a failing group must never stop a later group in the same poll from being
// fetched, enqueued and persisted. Here destA's group (ascending order, so visited first) hits
// ErrCandidatesNotSynced; destB's group must still run to completion. If the loop's "continue" after
// that failure were ever changed to "break", destB's group would never run at all.
func TestL2ToLxProcessLERGroups_NotSyncedGroupDoesNotBlockLaterGroups(t *testing.T) {
	ctx := context.Background()
	const (
		sourceID uint32 = 1
		destA    uint32 = 10 // established at lerX; its group will report "not synced yet"
		destB    uint32 = 11 // no cursor yet; its group must still be fetched/enqueued/persisted
	)
	lerX := lerHash(20)
	lerY := lerHash(30)
	candidateB := makeCandidate(7, destB)

	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(sourceID, lerY, 40)},
		},
	}

	fetcher := newFakeFetcher()
	fetcher.urls[sourceID] = fakeSrcURL1
	fetcher.setPageErrForLER(fakeSrcURL1, &lerX, 1, ErrCandidatesNotSynced)
	fetcher.setPageForLER(fakeSrcURL1, nil, 1, []ClaimCandidate{candidateB}, 1)

	lerStore := newFakePerPairLERStore()
	lerStore.set(sourceID, destA, autoclaimtypes.LERCursor{
		SourceNetwork: sourceID, DestinationNetwork: destA, LastLER: lerX, LastVerifyBlockNum: 20,
	})

	claimerA := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: destA}}
	claimerB := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: destB}}
	enqueuer := newFakeEnqueuer()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimerA, claimerB), newMemoryCursorStore(), lerStore, enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err, "ErrCandidatesNotSynced is a retry signal, not a hard error")
	require.Equal(t, 1, result.SkippedSourceCount)
	require.Equal(t, 0, result.ProcessedSourceCount)

	keyB := autoclaimtypes.DeriveRequestKey(sourceID, destB, candidateB.Bridge.DepositCount)
	_, enqueuedB := enqueuer.requests[keyB]
	require.True(t, enqueuedB,
		"destB's group must still be fetched and enqueued even though destA's group (visited first) "+
			"failed with ErrCandidatesNotSynced")

	cursorB, found, err := lerStore.GetLERCursor(ctx, sourceID, destB)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, lerY, cursorB.LastLER, "destB's group must have advanced its own pair cursor")

	cursorA, found, err := lerStore.GetLERCursor(ctx, sourceID, destA)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, lerX, cursorA.LastLER, "destA's failed group must not advance its pair cursor")
}

// TestL2ToLxProcessLERGroups_EnqueueFailureDoesNotBlockLaterGroups is
// TestL2ToLxProcessLERGroups_NotSyncedGroupDoesNotBlockLaterGroups's counterpart for the enqueue-error
// continue: destA's group fetches successfully but fails to enqueue; destB's later group must still
// run to completion in the same poll, even though the poll as a whole ultimately reports destA's
// enqueue error.
func TestL2ToLxProcessLERGroups_EnqueueFailureDoesNotBlockLaterGroups(t *testing.T) {
	ctx := context.Background()
	const (
		sourceID uint32 = 1
		destA    uint32 = 10 // established at lerX; its group's enqueue will fail
		destB    uint32 = 11 // no cursor yet; its group must still be fetched/enqueued/persisted
	)
	lerX := lerHash(20)
	lerY := lerHash(30)
	candidateA := makeCandidate(5, destA)
	candidateB := makeCandidate(7, destB)
	enqueueErr := errors.New("enqueue exploded for destA")

	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(sourceID, lerY, 40)},
		},
	}

	fetcher := newFakeFetcher()
	fetcher.urls[sourceID] = fakeSrcURL1
	fetcher.setPageForLER(fakeSrcURL1, &lerX, 1, []ClaimCandidate{candidateA}, 1)
	fetcher.setPageForLER(fakeSrcURL1, nil, 1, []ClaimCandidate{candidateB}, 1)

	lerStore := newFakePerPairLERStore()
	lerStore.set(sourceID, destA, autoclaimtypes.LERCursor{
		SourceNetwork: sourceID, DestinationNetwork: destA, LastLER: lerX, LastVerifyBlockNum: 20,
	})

	claimerA := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: destA}}
	claimerB := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: destB}}
	enqueuer := newFakeEnqueuer()
	enqueuer.errForDestination = map[uint32]error{destA: enqueueErr}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimerA, claimerB), newMemoryCursorStore(), lerStore, enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	_, err := detector.PollOnce(ctx)
	require.ErrorIs(t, err, enqueueErr)

	keyB := autoclaimtypes.DeriveRequestKey(sourceID, destB, candidateB.Bridge.DepositCount)
	_, enqueuedB := enqueuer.requests[keyB]
	require.True(t, enqueuedB,
		"destB's group must still be fetched, enqueued and persisted even though destA's group "+
			"(visited first) failed to enqueue")

	cursorB, found, err := lerStore.GetLERCursor(ctx, sourceID, destB)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, lerY, cursorB.LastLER, "destB's group must have advanced its own pair cursor")

	cursorA, found, err := lerStore.GetLERCursor(ctx, sourceID, destA)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, lerX, cursorA.LastLER, "destA's failed group must not advance its pair cursor")
}

// TestL2ToLxSeedsLegacyCursorBeforeResolvingPairs covers the autoclaim0003 upgrade path: on the first
// poll after the autoclaim0003 upgrade, the parked pre-0003 per-source cursor is fanned out to the
// destinations configured at that moment — before any pair is resolved — so those destinations
// resume from the parked LER instead of re-scanning their whole history.
func TestL2ToLxSeedsLegacyCursorBeforeResolvingPairs(t *testing.T) {
	ctx := context.Background()
	const (
		sourceID uint32 = 1
		destA    uint32 = 10
		destB    uint32 = 11
	)
	lerX, lerY := lerHash(20), lerHash(30)

	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(sourceID, lerY, 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[sourceID] = fakeSrcURL1
	fetcher.setPageForLER(fakeSrcURL1, &lerX, 1, nil, 0)

	lerStore := newFakePerPairLERStore()
	lerStore.seedFrom = &autoclaimtypes.LERCursor{SourceNetwork: sourceID, LastLER: lerX, LastVerifyBlockNum: 5}

	claimerA := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: destA}}
	claimerB := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: destB}}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimerA, claimerB), newMemoryCursorStore(), lerStore, newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, []uint32{destA, destB}, lerStore.seeded,
		"the legacy row is seeded with exactly the destination set the detector is about to query")
	require.Len(t, fetcher.queries, 1, "both seeded pairs share the parked LER, so they form one group")
	require.NotNil(t, fetcher.queries[0].FromLER)
	require.Equal(t, lerX, *fetcher.queries[0].FromLER,
		"seeded destinations resume from the parked LER instead of re-scanning their history")
	require.Equal(t, []uint32{destA, destB}, fetcher.queries[0].DestinationNetworkIDs)
	require.Equal(t, 1, result.ProcessedSourceCount)
	require.Equal(t, lerY, lerStore.cursors[pairKey(sourceID, destA)].LastLER)
	require.Equal(t, lerY, lerStore.cursors[pairKey(sourceID, destB)].LastLER)
}

// TestL2ToLxSeedErrorPropagatesFromPollOnce proves processSource does not swallow a
// SeedLERCursorsFromLegacy failure: a storage error there must reach PollOnce's caller, not be
// treated as "nothing to seed" or otherwise absorbed. fakePerPairLERStore.seedErr already exists for
// exactly this, but nothing set it until now, so the propagation path was untested.
func TestL2ToLxSeedErrorPropagatesFromPollOnce(t *testing.T) {
	ctx := context.Background()
	const (
		sourceID uint32 = 1
		destA    uint32 = 10
	)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(sourceID, lerHash(30), 20)},
		},
	}
	seedErr := errors.New("seed storage exploded")
	lerStore := newFakePerPairLERStore()
	lerStore.seedErr = seedErr

	claimerA := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: destA}}
	detector := newTestL2ToLxDetector(
		t, source, newFakeFetcher(), newFakeRegistry(claimerA), newMemoryCursorStore(), lerStore, newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	_, err := detector.PollOnce(ctx)
	require.ErrorIs(t, err, seedErr, "a SeedLERCursorsFromLegacy failure must propagate out of PollOnce")
}

// TestL2ToLxPollIssuesBoundedFetcherCalls pins the request-cost contract of the per-pair design:
// grouping pending destinations by from_ler fans out at most one query per
// distinct from_ler, so a source's worst case in one poll is one query per enabled destination
// claimer (times the pages each needs) — bounded by the claimer count, never unbounded. Here every
// destination sits at a different LER, which is the maximum possible fan-out.
func TestL2ToLxPollIssuesBoundedFetcherCalls(t *testing.T) {
	ctx := context.Background()
	const sourceID uint32 = 1
	lerY := lerHash(30)
	destinations := []uint32{0, 2, 3, 4}

	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: 50,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}: {makeVerifyRow(sourceID, lerY, 20)},
		},
	}
	fetcher := newFakeFetcher()
	fetcher.urls[sourceID] = fakeSrcURL1

	lerStore := newFakePerPairLERStore()
	claimers := make([]*fakeClaimer, 0, len(destinations))
	for i, destination := range destinations {
		claimers = append(claimers, &fakeClaimer{
			target: autoclaimtypes.ClaimerTarget{
				ID: fmt.Sprintf("claimer-%d", destination), DestinationNetwork: destination,
			},
		})
		// A distinct established LER per destination, so no two pairs can share a fetch group.
		lerStore.set(sourceID, destination, autoclaimtypes.LERCursor{
			SourceNetwork:      sourceID,
			DestinationNetwork: destination,
			LastLER:            lerHash(int64(100 + i)),
			LastVerifyBlockNum: 10,
		})
	}

	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimers...), newMemoryCursorStore(), lerStore, newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	_, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.LessOrEqual(t, len(fetcher.queries), len(destinations),
		"one poll must never issue more queries for a source than it has destination claimers")
	require.Len(t, fetcher.queries, len(destinations), "worst case is exactly one fetch group per destination")

	queriedFromLERs := make(map[string]struct{}, len(fetcher.queries))
	for _, query := range fetcher.queries {
		require.Len(t, query.DestinationNetworkIDs, 1, "destinations at distinct LERs cannot share a query")
		require.LessOrEqual(t, len(query.DestinationNetworkIDs), bridgeservice.MaxNetworkIDs)
		require.NotNil(t, query.FromLER)
		queriedFromLERs[query.FromLER.Hex()] = struct{}{}
	}
	require.Len(t, queriedFromLERs, len(destinations), "each destination is queried from its own LER")
}

func TestL2ToLxDisabled(t *testing.T) {
	ctx := context.Background()
	source := &fakeVerifiedBatchSource{lastProcessedBlock: 50}
	fetcher := newFakeFetcher()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(), newMemoryCursorStore(), newFakePerPairLERStore(), newFakeEnqueuer(),
		WithL2ToLxEnabled(false),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(0), result.LastProcessedBlock)
	require.Empty(t, source.ranges)
}

func TestNewL2ToLxNilArgs(t *testing.T) {
	source := &fakeVerifiedBatchSource{}
	fetcher := newFakeFetcher()
	registry := newFakeRegistry()
	cursorStore := newMemoryCursorStore()
	lerStore := newFakePerPairLERStore()
	enqueuer := newFakeEnqueuer()

	_, err := NewL2ToLx(nil, fetcher, registry, cursorStore, lerStore, enqueuer)
	require.ErrorContains(t, err, "verified batch source is nil")
	_, err = NewL2ToLx(source, nil, registry, cursorStore, lerStore, enqueuer)
	require.ErrorContains(t, err, "claim candidates fetcher is nil")
	_, err = NewL2ToLx(source, fetcher, nil, cursorStore, lerStore, enqueuer)
	require.ErrorContains(t, err, "claimer registry is nil")
	_, err = NewL2ToLx(source, fetcher, registry, nil, lerStore, enqueuer)
	require.ErrorContains(t, err, "cursor store is nil")
	_, err = NewL2ToLx(source, fetcher, registry, cursorStore, nil, enqueuer)
	require.ErrorContains(t, err, "ler cursor store is nil")
	_, err = NewL2ToLx(source, fetcher, registry, cursorStore, lerStore, nil)
	require.ErrorContains(t, err, "request enqueuer is nil")
}

func newTestL2ToLxDetector(
	t *testing.T,
	source VerifiedBatchSource,
	fetcher ClaimCandidatesFetcher,
	registry autoclaimtypes.ClaimerRegistry,
	cursorStore CursorStore,
	lerCursors LERCursorStore,
	enqueuer RequestEnqueuer,
	options ...L2ToLxOption,
) *L2ToLx {
	t.Helper()
	options = append([]L2ToLxOption{WithL2ToLxNow(func() time.Time { return testNow })}, options...)
	detector, err := NewL2ToLx(source, fetcher, registry, cursorStore, lerCursors, enqueuer, options...)
	require.NoError(t, err)
	return detector
}

// --- fakes ---

type fakeVerifiedBatchSource struct {
	lastProcessedBlock uint64
	lastProcessedErr   error
	rowsByRange        map[blockRange][]*l1infotreesync.VerifyBatches
	rangesErr          error
	ranges             []blockRange
	latestLeaf         *l1infotreesync.L1InfoTreeLeaf
	latestLeafErr      error
	localExitRoots     map[uint32]common.Hash
	localExitRootErr   error
}

func (s *fakeVerifiedBatchSource) GetLastProcessedBlock(_ context.Context) (uint64, error) {
	return s.lastProcessedBlock, s.lastProcessedErr
}

func (s *fakeVerifiedBatchSource) GetVerifiedBatchesInBlockRange(
	fromBlock, toBlock uint64,
) ([]*l1infotreesync.VerifyBatches, error) {
	s.ranges = append(s.ranges, blockRange{from: fromBlock, to: toBlock})
	if s.rangesErr != nil {
		return nil, s.rangesErr
	}
	return s.rowsByRange[blockRange{from: fromBlock, to: toBlock}], nil
}

func (s *fakeVerifiedBatchSource) GetLatestL1InfoLeafUntilBlock(
	_ context.Context, _ uint64,
) (*l1infotreesync.L1InfoTreeLeaf, error) {
	if s.latestLeafErr != nil {
		return nil, s.latestLeafErr
	}
	return s.latestLeaf, nil
}

func (s *fakeVerifiedBatchSource) GetLocalExitRoot(
	_ context.Context, networkID uint32, _ common.Hash,
) (common.Hash, error) {
	if s.localExitRootErr != nil {
		return common.Hash{}, s.localExitRootErr
	}
	return s.localExitRoots[networkID], nil
}

type fetchKey struct {
	url  string
	page uint32
}

// lerFetchKey additionally keys a stubbed page by the from_ler the query was made with, so a test can
// simulate a real bridge service's exclusive-lower-bound filtering (a query anchored at a later LER
// must not see candidates that predate it). Used by setPageForLER; setPage (unkeyed by LER) remains the
// default for tests that don't care about from_ler-dependent results.
type lerFetchKey struct {
	url    string
	lerKey string // "" for a nil (full-history) from_ler, else the hash's hex string.
	page   uint32
}

func fromLERKey(fromLER *common.Hash) string {
	if fromLER == nil {
		return ""
	}
	return fromLER.Hex()
}

type fakeFetcher struct {
	urls            map[uint32]string
	urlErr          map[uint32]error
	pages           map[fetchKey][]ClaimCandidate
	pageCounts      map[fetchKey]int
	pageErr         map[fetchKey]error
	pagesByLER      map[lerFetchKey][]ClaimCandidate
	pageCountsByLER map[lerFetchKey]int
	pageErrByLER    map[lerFetchKey]error
	queries         []ClaimCandidatesQuery
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		urls:            make(map[uint32]string),
		urlErr:          make(map[uint32]error),
		pages:           make(map[fetchKey][]ClaimCandidate),
		pageCounts:      make(map[fetchKey]int),
		pageErr:         make(map[fetchKey]error),
		pagesByLER:      make(map[lerFetchKey][]ClaimCandidate),
		pageCountsByLER: make(map[lerFetchKey]int),
		pageErrByLER:    make(map[lerFetchKey]error),
	}
}

func (f *fakeFetcher) setPage(url string, page uint32, candidates []ClaimCandidate, count int) {
	key := fetchKey{url: url, page: page}
	f.pages[key] = candidates
	f.pageCounts[key] = count
}

// setPageForLER stubs a page keyed additionally by the from_ler the query is expected to carry,
// simulating a bridge service that only returns candidates at or after fromLER (nil = full history).
func (f *fakeFetcher) setPageForLER(
	url string, fromLER *common.Hash, page uint32, candidates []ClaimCandidate, count int,
) {
	key := lerFetchKey{url: url, lerKey: fromLERKey(fromLER), page: page}
	f.pagesByLER[key] = candidates
	f.pageCountsByLER[key] = count
}

// setPageErrForLER stubs a page error keyed by the from_ler the query is expected to carry, so a test
// with several concurrent from_ler groups against the same URL/page can make exactly one group fail
// (e.g. with ErrCandidatesNotSynced) while the others succeed -- plain setPage/pageErr cannot express
// this, since it keys only on (url, page) and every group's first page shares that key.
func (f *fakeFetcher) setPageErrForLER(url string, fromLER *common.Hash, page uint32, err error) {
	key := lerFetchKey{url: url, lerKey: fromLERKey(fromLER), page: page}
	f.pageErrByLER[key] = err
}

func (f *fakeFetcher) GetURL(sourceNetwork uint32) (string, error) {
	if err, ok := f.urlErr[sourceNetwork]; ok {
		return "", err
	}
	url, ok := f.urls[sourceNetwork]
	if !ok {
		return "", ErrURLNotFound
	}
	return url, nil
}

func (f *fakeFetcher) GetClaimCandidates(
	_ context.Context, query ClaimCandidatesQuery,
) ([]ClaimCandidate, int, error) {
	f.queries = append(f.queries, query)
	lerKey := lerFetchKey{url: query.URL, lerKey: fromLERKey(query.FromLER), page: query.PageNumber}
	if err, ok := f.pageErrByLER[lerKey]; ok {
		return nil, 0, err
	}
	key := fetchKey{url: query.URL, page: query.PageNumber}
	if err, ok := f.pageErr[key]; ok {
		return nil, 0, err
	}
	if candidates, ok := f.pagesByLER[lerKey]; ok {
		return candidates, f.pageCountsByLER[lerKey], nil
	}
	return f.pages[key], f.pageCounts[key], nil
}

type fakeEnqueuer struct {
	requests map[autoclaimtypes.RequestKey]autoclaimtypes.AutoClaimRequest
	order    []autoclaimtypes.AutoClaimRequest
	err      error
	// errForDestination fails EnqueueRequest only for the given destination network, letting a test
	// with several concurrent from_ler groups make exactly one group's enqueue fail while the others
	// succeed -- err (above) fails every call unconditionally instead.
	errForDestination map[uint32]error
}

func newFakeEnqueuer() *fakeEnqueuer {
	return &fakeEnqueuer{requests: make(map[autoclaimtypes.RequestKey]autoclaimtypes.AutoClaimRequest)}
}

func (e *fakeEnqueuer) EnqueueRequest(
	_ context.Context, request autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.AutoClaimRequest, bool, error) {
	if e.err != nil {
		return nil, false, e.err
	}
	if err, ok := e.errForDestination[request.Bridge.DestinationNetwork]; ok {
		return nil, false, err
	}
	if existing, ok := e.requests[request.Key]; ok {
		return &existing, false, nil
	}
	e.requests[request.Key] = request
	e.order = append(e.order, request)
	return &request, true, nil
}

// --- S23: per-destination isolation and back-off ---

const (
	isoSource   uint32 = 1
	isoFailing  uint32 = 10
	isoHealthy  uint32 = 11
	isoClaimed  uint32 = 12
	isoVerify          = uint64(40)
	isoFirstTo         = isoVerify - 1 // block cursor held just before the source's verify row
	isoLastBlck        = uint64(50)
)

// destinationFilteringFetcher wraps fakeFetcher and, like the real bridge service, only returns the
// candidates whose destination is in the query's DestinationNetworkIDs (with a matching count).
type destinationFilteringFetcher struct {
	*fakeFetcher
}

func (f *destinationFilteringFetcher) GetClaimCandidates(
	ctx context.Context, query ClaimCandidatesQuery,
) ([]ClaimCandidate, int, error) {
	candidates, _, err := f.fakeFetcher.GetClaimCandidates(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	wanted := make(map[uint32]struct{}, len(query.DestinationNetworkIDs))
	for _, destination := range query.DestinationNetworkIDs {
		wanted[destination] = struct{}{}
	}
	filtered := make([]ClaimCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := wanted[candidate.Bridge.DestinationNetwork]; ok {
			filtered = append(filtered, candidate)
		}
	}
	return filtered, len(filtered), nil
}

type isolationFixture struct {
	detector    *L2ToLx
	fetcher     *destinationFilteringFetcher
	registry    *fakeRegistry
	failing     *fakeClaimer
	healthy     *fakeClaimer
	claimed     *fakeClaimer
	lerStore    *fakePerPairLERStore
	cursorStore *memoryCursorStore
	enqueuer    *fakeEnqueuer
	log         *capturingLogger
	clock       *time.Time
	lerX, lerY  common.Hash
	// published maps a source LER to every candidate the source published up to (and including) it.
	published map[common.Hash][]ClaimCandidate
}

// newIsolationFixture builds one source whose three destination pairs share from_ler = lerX and whose
// newest LER lerY (verify row at block 40) carries two candidates for the failing destination, one for
// the healthy destination and one for a destination where the bridge is already claimed.
func newIsolationFixture(t *testing.T, failure error) *isolationFixture {
	t.Helper()
	f := &isolationFixture{
		failing: &fakeClaimer{
			target:   autoclaimtypes.ClaimerTarget{ID: fakeClaimer10ID, DestinationNetwork: isoFailing},
			claimErr: failure,
		},
		healthy:  &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer11ID, DestinationNetwork: isoHealthy}},
		claimed:  &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-12", DestinationNetwork: isoClaimed}, claimed: true},
		lerStore: newFakePerPairLERStore(),
		enqueuer: newFakeEnqueuer(),
		log:      &capturingLogger{},
		lerX:     lerHash(20),
		lerY:     lerHash(30),
	}
	clock := testNow
	f.clock = &clock
	f.registry = newFakeRegistry(f.failing, f.healthy, f.claimed)
	f.cursorStore = newMemoryCursorStore()

	candidates := []ClaimCandidate{
		makeCandidate(5, isoFailing), makeCandidate(6, isoFailing),
		makeCandidate(7, isoHealthy), makeCandidate(8, isoClaimed),
	}
	f.published = map[common.Hash][]ClaimCandidate{f.lerX: nil, f.lerY: candidates}
	f.fetcher = &destinationFilteringFetcher{fakeFetcher: newFakeFetcher()}
	f.fetcher.urls[isoSource] = fakeSrcURL1
	f.fetcher.setPageForLER(fakeSrcURL1, &f.lerX, 1, candidates, len(candidates))

	for _, destination := range []uint32{isoFailing, isoHealthy, isoClaimed} {
		f.lerStore.set(isoSource, destination, autoclaimtypes.LERCursor{
			SourceNetwork: isoSource, DestinationNetwork: destination, LastLER: f.lerX, LastVerifyBlockNum: 5,
		})
	}

	row := makeVerifyRow(isoSource, f.lerY, isoVerify)
	source := &fakeVerifiedBatchSource{
		lastProcessedBlock: isoLastBlck,
		rowsByRange: map[blockRange][]*l1infotreesync.VerifyBatches{
			{from: 0, to: 49}:                   {row},
			{from: isoFirstTo, to: isoLastBlck}: {row}, // re-observed window (overlap 1) while the cursor is held
		},
	}
	f.detector = newTestL2ToLxDetector(t, source, f.fetcher, f.registry, f.cursorStore, f.lerStore, f.enqueuer,
		WithL2ToLxBlockWindow(50), WithL2ToLxLogger(f.log), WithL2ToLxNow(func() time.Time { return *f.clock }))
	f.detector.backoff = newDestinationBackoff(time.Second, 2*time.Minute, 0)
	return f
}

func (f *isolationFixture) pairLER(t *testing.T, destination uint32) common.Hash {
	t.Helper()
	cursor, found, err := f.lerStore.GetLERCursor(context.Background(), isoSource, destination)
	require.NoError(t, err)
	require.True(t, found)
	return cursor.LastLER
}

func (f *isolationFixture) blockCursorTo(t *testing.T) uint64 {
	t.Helper()
	cursor, ok := f.cursorStore.cursors[defaultL2ToLxCursorName]
	require.True(t, ok)
	return cursor.ToBlock
}

// requireLERCursorInvariant asserts the S23 safety invariant: for every persisted LER pair cursor, every
// candidate the source published up to that cursor's LER for that destination was enqueued or is already
// claimed on the destination. A pair cursor therefore never moves past an un-enqueued candidate.
func requireLERCursorInvariant(t *testing.T, f *isolationFixture) {
	t.Helper()
	require.NotEmpty(t, f.lerStore.cursors)
	for key, cursor := range f.lerStore.cursors {
		sourceID, destination := key[0], key[1]
		upTo, known := f.published[cursor.LastLER]
		require.True(t, known, "pair (%d, %d) cursor points at an unknown LER %s", sourceID, destination, cursor.LastLER)
		for _, candidate := range upTo {
			if candidate.Bridge.DestinationNetwork != destination {
				continue
			}
			requestKey := autoclaimtypes.DeriveRequestKey(sourceID, destination, candidate.Bridge.DepositCount)
			_, enqueued := f.enqueuer.requests[requestKey]
			claimer := f.registry.claimers[destination]
			alreadyClaimed := claimer != nil && claimer.claimed
			require.True(t, enqueued || alreadyClaimed,
				"pair (%d, %d) cursor at %s is past candidate %s, which was neither enqueued nor already claimed",
				sourceID, destination, cursor.LastLER, requestKey)
		}
	}
}

func TestL2ToLx_FailingDestinationIsIsolated(t *testing.T) {
	ctx := context.Background()
	claimErr := errors.New("destination rpc down")
	f := newIsolationFixture(t, claimErr)

	result, err := f.detector.PollOnce(ctx)
	require.ErrorIs(t, err, claimErr)
	require.ErrorContains(t, err, fmt.Sprintf("destination %d:", isoFailing))
	require.True(t, result.CursorAdvanced, "a per-destination failure still saves the (held) block cursor")
	require.Equal(t, []uint32{isoFailing}, result.FailedDestinations)
	require.Empty(t, result.BackedOffDestinations)
	require.Equal(t, 1, result.DeferredCandidateCount, "the failing destination's second candidate is deferred")
	require.Len(t, f.failing.claimChecks, 1, "no IsClaimed call after the destination failed")
	require.Equal(t, 1, result.SkippedSourceCount)
	require.Equal(t, 0, result.ProcessedSourceCount)

	// One query for the shared from_ler group.
	require.Len(t, f.fetcher.queries, 1)
	require.Equal(t, []uint32{isoFailing, isoHealthy, isoClaimed}, f.fetcher.queries[0].DestinationNetworkIDs)

	// The healthy destination's candidate is enqueued and its pair advances; the already-claimed pair
	// advances too; the failing pair stays.
	_, enqueued := f.enqueuer.requests[autoclaimtypes.DeriveRequestKey(isoSource, isoHealthy, 7)]
	require.True(t, enqueued)
	require.Len(t, f.enqueuer.order, 1)
	require.Equal(t, f.lerY, f.pairLER(t, isoHealthy))
	require.Equal(t, f.lerY, f.pairLER(t, isoClaimed))
	require.Equal(t, f.lerX, f.pairLER(t, isoFailing))

	// The block cursor is held just before the source's verify row.
	require.Equal(t, isoFirstTo, f.blockCursorTo(t))

	require.Len(t, f.log.warns, 1)
	require.Contains(t, f.log.warns[0], "autoclaim l2-to-lx bridge detector: detection for destination 10 "+
		"paused for 1s after error (attempt 1)")
	require.Equal(t, 1, f.detector.backoff.stalledCount())
	requireLERCursorInvariant(t, f)
}

// TestL2ToLx_DestinationFailingAfterSomeCandidatesEnqueued covers a destination whose claim-state check fails
// on its second candidate: the first one is enqueued, the pair cursor stays, and once the destination
// recovers the first is re-enqueued idempotently before the cursor advances.
func TestL2ToLx_DestinationFailingAfterSomeCandidatesEnqueued(t *testing.T) {
	ctx := context.Background()
	f := newIsolationFixture(t, nil)
	failure := errors.New("destination rpc down")
	f.failing.claimErr, f.failing.claimErrFrom = failure, 2

	result, err := f.detector.PollOnce(ctx)
	require.ErrorIs(t, err, failure)
	require.Equal(t, []uint32{isoFailing}, result.FailedDestinations)
	_, enqueued := f.enqueuer.requests[autoclaimtypes.DeriveRequestKey(isoSource, isoFailing, 5)]
	require.True(t, enqueued, "the first candidate was enqueued before the failure")
	_, enqueued = f.enqueuer.requests[autoclaimtypes.DeriveRequestKey(isoSource, isoFailing, 6)]
	require.False(t, enqueued)
	require.Equal(t, f.lerX, f.pairLER(t, isoFailing), "the pair cursor does not move")
	require.Equal(t, isoFirstTo, f.blockCursorTo(t))
	requireLERCursorInvariant(t, f)

	f.failing.claimErr = nil
	*f.clock = testNow.Add(time.Second)
	_, err = f.detector.PollOnce(ctx)
	require.NoError(t, err)
	for _, deposit := range []uint32{5, 6} {
		_, enqueued = f.enqueuer.requests[autoclaimtypes.DeriveRequestKey(isoSource, isoFailing, deposit)]
		require.True(t, enqueued)
	}
	require.Len(t, f.enqueuer.order, 3, "failing deposits 5 and 6 plus the healthy one, each enqueued once")
	require.Equal(t, f.lerY, f.pairLER(t, isoFailing))
	requireLERCursorInvariant(t, f)
}

// TestL2ToLx_SoftFailureRecordedWhenGroupAlsoHitsHardError covers a fetch group where one destination's
// claim-state check fails (soft) and a later enqueue fails with a hard error: the soft failure must still
// open the destination's back-off window.
func TestL2ToLx_SoftFailureRecordedWhenGroupAlsoHitsHardError(t *testing.T) {
	ctx := context.Background()
	soft := errors.New("destination rpc down")
	hard := errors.New("local db down")
	f := newIsolationFixture(t, soft)
	f.enqueuer.errForDestination = map[uint32]error{isoHealthy: hard}

	result, err := f.detector.PollOnce(ctx)
	require.ErrorIs(t, err, hard)
	require.Equal(t, []uint32{isoFailing}, result.FailedDestinations)
	require.Equal(t, 1, f.detector.backoff.stalledCount(), "the soft failure opens a back-off window")
	require.Len(t, f.log.warns, 1)
	require.Contains(t, f.log.warns[0], "detection for destination 10 paused for 1s")
	require.Equal(t, f.lerX, f.pairLER(t, isoFailing))
	require.Equal(t, f.lerX, f.pairLER(t, isoHealthy), "nothing advances in the group that hit the hard error")
	requireLERCursorInvariant(t, f)
}

func TestL2ToLx_BackedOffDestinationHoldsBlockCursor(t *testing.T) {
	ctx := context.Background()
	f := newIsolationFixture(t, errors.New("boom"))

	_, err := f.detector.PollOnce(ctx)
	require.Error(t, err)
	queries := len(f.fetcher.queries)

	// Same clock, then just before the window ends: the destination is backed off, nothing is fetched,
	// and the source still holds the block cursor at the verify row.
	for _, at := range []time.Time{testNow, testNow.Add(time.Second - time.Nanosecond)} {
		*f.clock = at
		result, err := f.detector.PollOnce(ctx)
		require.NoError(t, err)
		require.Equal(t, []uint32{isoFailing}, result.BackedOffDestinations)
		require.Empty(t, result.FailedDestinations)
		require.Equal(t, 1, result.NewLERSourceCount)
		require.Equal(t, 1, result.SkippedSourceCount)
		require.Equal(t, 0, result.ProcessedSourceCount)
		require.Len(t, f.fetcher.queries, queries, "a backed-off pair is not fetched")
		require.Len(t, f.failing.claimChecks, 1, "no IsClaimed call while backed off")
		require.Equal(t, isoFirstTo, f.blockCursorTo(t), "the block cursor stays held at the verify row")
		require.Equal(t, f.lerX, f.pairLER(t, isoFailing))
		requireLERCursorInvariant(t, f)
	}
	require.Len(t, f.log.warns, 1, "no Warn while backed off")
	var backoffDebugs []string
	for _, line := range f.log.debugs {
		if strings.Contains(line, "backed off until") {
			backoffDebugs = append(backoffDebugs, line)
		}
	}
	require.Len(t, backoffDebugs, 2)
	require.Contains(t, backoffDebugs[0],
		"autoclaim l2-to-lx bridge detector: skipping destination 10, backed off until")
}

func TestL2ToLx_FailedDestinationRetriedAloneAfterWindow(t *testing.T) {
	ctx := context.Background()
	f := newIsolationFixture(t, errors.New("boom"))

	_, err := f.detector.PollOnce(ctx)
	require.Error(t, err)
	queries := len(f.fetcher.queries)

	f.failing.claimErr = nil
	*f.clock = testNow.Add(time.Second)
	result, err := f.detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Empty(t, result.BackedOffDestinations)
	require.Empty(t, result.FailedDestinations)
	require.Equal(t, 1, result.ProcessedSourceCount)

	// Only the failed destination's own group is re-fetched, from its unchanged pair cursor.
	require.Len(t, f.fetcher.queries, queries+1)
	retry := f.fetcher.queries[queries]
	require.Equal(t, []uint32{isoFailing}, retry.DestinationNetworkIDs)
	require.NotNil(t, retry.FromLER)
	require.Equal(t, f.lerX, *retry.FromLER)

	for _, deposit := range []uint32{5, 6} {
		_, enqueued := f.enqueuer.requests[autoclaimtypes.DeriveRequestKey(isoSource, isoFailing, deposit)]
		require.True(t, enqueued)
	}
	require.Equal(t, f.lerY, f.pairLER(t, isoFailing))
	require.Equal(t, isoLastBlck, f.blockCursorTo(t), "the block cursor moves on once the destination recovered")
	require.Equal(t, 0, f.detector.backoff.stalledCount())
	requireLERCursorInvariant(t, f)
}

func TestL2ToLx_BackoffWindowsGrowAndSuccessResets(t *testing.T) {
	ctx := context.Background()
	f := newIsolationFixture(t, errors.New("boom"))

	for i, window := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		result, err := f.detector.PollOnce(ctx)
		require.Error(t, err)
		require.Equal(t, []uint32{isoFailing}, result.FailedDestinations)
		require.Len(t, f.log.warns, i+1)
		require.Contains(t, f.log.warns[i], "paused for "+window.String())
		require.Contains(t, f.log.warns[i], fmt.Sprintf("after error (attempt %d)", i+1))
		require.Equal(t, f.clock.Add(window), f.detector.backoff.skipUntil(isoFailing))
		require.Equal(t, isoFirstTo, f.blockCursorTo(t))
		requireLERCursorInvariant(t, f)
		*f.clock = f.clock.Add(window)
	}

	f.failing.claimErr = nil
	result, err := f.detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Empty(t, result.FailedDestinations)
	require.Equal(t, 0, f.detector.backoff.stalledCount(), "success resets the back-off")
	require.Len(t, f.log.warns, 3)
	requireLERCursorInvariant(t, f)

	// A new failure starts again from the initial window.
	window, attempt := f.detector.backoff.recordFailure(isoFailing, errors.New("again"), *f.clock)
	require.Equal(t, time.Second, window)
	require.Equal(t, 1, attempt)
}

func TestL2ToLx_BackoffRateLimitedUsesHTTP429Wording(t *testing.T) {
	ctx := context.Background()
	f := newIsolationFixture(t, rpc.HTTPError{StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests"})

	_, err := f.detector.PollOnce(ctx)
	require.Error(t, err)
	require.Len(t, f.log.warns, 1)
	require.Contains(t, f.log.warns[0], "autoclaim l2-to-lx bridge detector: detection for destination 10 "+
		"paused for 1s after rate limiting (HTTP 429, attempt 1)")
}

func TestL2ToLx_BackoffNotUpdatedWhenContextCancelled(t *testing.T) {
	f := newIsolationFixture(t, errors.New("boom"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := f.detector.PollOnce(ctx)
	require.Error(t, err)
	require.Equal(t, []uint32{isoFailing}, result.FailedDestinations)
	require.Equal(t, 0, f.detector.backoff.stalledCount())
	require.Empty(t, f.log.warns)
	require.Equal(t, f.lerX, f.pairLER(t, isoFailing))
	requireLERCursorInvariant(t, f)
}

func TestL2ToLx_BackoffMetrics(t *testing.T) {
	prometheus.Init()
	autoclaimmetrics.Register()
	ctx := context.Background()
	f := newIsolationFixture(t, errors.New("boom"))

	gauge, ok := prometheus.Gauge("l2_to_lx_stalled_destinations") // the wrapper keys series by bare name
	require.True(t, ok)
	vec, ok := prometheus.CounterVec("detector_destination_errors_total")
	require.True(t, ok)
	errCounter := vec.WithLabelValues(string(autoclaimmetrics.DetectorL2ToLx), "10")
	before := testutil.ToFloat64(errCounter) // counters are process-global: assert deltas

	steps := []struct {
		name       string
		recover    bool
		advance    time.Duration
		wantErr    bool
		wantGauge  float64
		wantErrInc float64
	}{
		{name: "failure opens a window", wantErr: true, wantGauge: 1, wantErrInc: 1},
		{name: "poll inside the window adds nothing", wantGauge: 1, wantErrInc: 1},
		{name: "recovery clears the gauge", recover: true, advance: time.Second, wantGauge: 0, wantErrInc: 1},
	}
	for _, step := range steps {
		if step.recover {
			f.failing.claimErr = nil
		}
		*f.clock = f.clock.Add(step.advance)
		_, err := f.detector.PollOnce(ctx)
		if step.wantErr {
			require.Error(t, err, step.name)
		} else {
			require.NoError(t, err, step.name)
		}
		require.Equal(t, step.wantGauge, testutil.ToFloat64(gauge), step.name)
		require.Equal(t, before+step.wantErrInc, testutil.ToFloat64(errCounter), step.name)
	}
}
