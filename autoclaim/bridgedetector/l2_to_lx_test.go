package bridgedetector

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
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
	var proof treetypes.Proof
	proof[0] = common.BigToHash(big.NewInt(int64(depositCount) + 7))
	return ClaimCandidate{
		Bridge: autoclaimtypes.BridgeExit{
			BlockNum:           1000 + uint64(depositCount),
			LeafType:           bridgesynctypes.LeafTypeAsset,
			OriginNetwork:      99,
			DestinationNetwork: destinationNetwork,
			Amount:             big.NewInt(int64(depositCount)),
			DepositCount:       depositCount,
		},
		LeafProof: proof,
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
	lerStore := newFakeLERStore()
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
	require.NotEqual(t, treetypes.Proof{}, req1.LeafProof)

	req2 := enqueuer.requests[autoclaimtypes.DeriveRequestKey(2, 3, 6)]
	require.Equal(t, uint32(2), req2.Bridge.SourceNetwork)
	require.Equal(t, ler2, req2.LER)
	require.Equal(t, uint64(30), req2.VerifyBlockNum)

	require.Equal(t, ler1, lerStore.cursors[1].LastLER)
	require.Equal(t, uint64(20), lerStore.cursors[1].LastVerifyBlockNum)
	require.Equal(t, ler2, lerStore.cursors[2].LastLER)
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
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), newFakeLERStore(), newFakeEnqueuer(),
		WithL2ToLxStartL1Block(40), WithL2ToLxBlockWindow(100),
	)

	_, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Len(t, fetcher.queries, 1)
	require.NotNil(t, fetcher.queries[0].FromLER)
	require.Equal(t, initialLER, *fetcher.queries[0].FromLER)
	require.Equal(t, newLER, fetcher.queries[0].ToLER)
}

func TestL2ToLxInitialCursorZeroLEROmitsFromLER(t *testing.T) {
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
	fetcher.setPage(fakeSrcURL1, 1, []ClaimCandidate{makeCandidate(5, 0)}, 1)
	claimer0 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: fakeClaimer0ID, DestinationNetwork: 0}}
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), newFakeLERStore(), newFakeEnqueuer(),
		WithL2ToLxStartL1Block(40), WithL2ToLxBlockWindow(100),
	)

	_, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Len(t, fetcher.queries, 1)
	require.Nil(t, fetcher.queries[0].FromLER, "a zero initial LER must omit from_ler (full history)")
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
	lerStore := newFakeLERStore()
	enqueuer := newFakeEnqueuer()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), lerStore, enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.SkippedSourceCount)
	require.Equal(t, 1, result.ProcessedSourceCount)
	require.Equal(t, 1, result.EnqueuedCount)

	_, ok := lerStore.cursors[1]
	require.False(t, ok, "skipped source 1 must not advance its LER cursor")
	require.Equal(t, ler2, lerStore.cursors[2].LastLER, "source 2 processed independently")
	require.True(t, result.CursorAdvanced)
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
	lerStore := newFakeLERStore()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), lerStore, newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err, "not-synced is retry-later, not a hard error")
	require.Equal(t, 1, result.SkippedSourceCount)
	require.Equal(t, 0, result.EnqueuedCount)
	_, ok := lerStore.cursors[1]
	require.False(t, ok)
	require.True(t, result.CursorAdvanced)
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
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), newFakeLERStore(), enqueuer,
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
	lerStore := newFakeLERStore()
	enqueuer := newFakeEnqueuer()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer0), newMemoryCursorStore(), lerStore, enqueuer,
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.AlreadyClaimedCount)
	require.Equal(t, 0, result.EnqueuedCount)
	require.Empty(t, enqueuer.order)
	require.Len(t, claimer0.claimChecks, 1)
	require.Equal(t, ler1, lerStore.cursors[1].LastLER, "an already-claimed candidate still fully processes the source")
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
	lerStore := newFakeLERStore()
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
		t, source, fetcher, newFakeRegistry(claimer0, claimer1), newMemoryCursorStore(), newFakeLERStore(), newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	_, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Len(t, fetcher.queries, 1)
	require.Equal(t, []uint32{0}, fetcher.queries[0].DestinationNetworkIDs)
}

func TestL2ToLxOnlyDestinationIsSourceAdvancesCursorWithoutFetch(t *testing.T) {
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
	lerStore := newFakeLERStore()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(claimer1), newMemoryCursorStore(), lerStore, newFakeEnqueuer(),
		WithL2ToLxBlockWindow(50),
	)

	result, err := detector.PollOnce(ctx)
	require.NoError(t, err)
	require.Empty(t, fetcher.queries, "no fetch when there is no destination other than the source")
	require.Equal(t, 1, result.ProcessedSourceCount)
	require.Equal(t, ler1, lerStore.cursors[1].LastLER)
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
	lerStore := newFakeLERStore()
	lerStore.cursors[1] = autoclaimtypes.LERCursor{SourceNetwork: 1, LastLER: ler1, LastVerifyBlockNum: 20}
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

func TestL2ToLxDisabled(t *testing.T) {
	ctx := context.Background()
	source := &fakeVerifiedBatchSource{lastProcessedBlock: 50}
	fetcher := newFakeFetcher()
	detector := newTestL2ToLxDetector(
		t, source, fetcher, newFakeRegistry(), newMemoryCursorStore(), newFakeLERStore(), newFakeEnqueuer(),
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
	lerStore := newFakeLERStore()
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

type fakeFetcher struct {
	urls       map[uint32]string
	urlErr     map[uint32]error
	pages      map[fetchKey][]ClaimCandidate
	pageCounts map[fetchKey]int
	pageErr    map[fetchKey]error
	queries    []ClaimCandidatesQuery
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		urls:       make(map[uint32]string),
		urlErr:     make(map[uint32]error),
		pages:      make(map[fetchKey][]ClaimCandidate),
		pageCounts: make(map[fetchKey]int),
		pageErr:    make(map[fetchKey]error),
	}
}

func (f *fakeFetcher) setPage(url string, page uint32, candidates []ClaimCandidate, count int) {
	key := fetchKey{url: url, page: page}
	f.pages[key] = candidates
	f.pageCounts[key] = count
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
	key := fetchKey{url: query.URL, page: query.PageNumber}
	if err, ok := f.pageErr[key]; ok {
		return nil, 0, err
	}
	return f.pages[key], f.pageCounts[key], nil
}

type fakeLERStore struct {
	cursors map[uint32]autoclaimtypes.LERCursor
	saveErr error
}

func newFakeLERStore() *fakeLERStore {
	return &fakeLERStore{cursors: make(map[uint32]autoclaimtypes.LERCursor)}
}

func (s *fakeLERStore) GetLERCursor(
	_ context.Context, sourceNetwork uint32,
) (*autoclaimtypes.LERCursor, bool, error) {
	cursor, ok := s.cursors[sourceNetwork]
	if !ok {
		return nil, false, nil
	}
	return &cursor, true, nil
}

func (s *fakeLERStore) SaveLERCursor(
	_ context.Context, sourceNetwork uint32, cursor autoclaimtypes.LERCursor, _ time.Time,
) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.cursors[sourceNetwork] = cursor
	return nil
}

type fakeEnqueuer struct {
	requests map[autoclaimtypes.RequestKey]autoclaimtypes.AutoClaimRequest
	order    []autoclaimtypes.AutoClaimRequest
	err      error
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
	if existing, ok := e.requests[request.Key]; ok {
		return &existing, false, nil
	}
	e.requests[request.Key] = request
	e.order = append(e.order, request)
	return &request, true, nil
}
