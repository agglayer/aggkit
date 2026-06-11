package watchdog

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
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var testNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func TestPollOnceConstructsPollingWindows(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{lastProcessedBlock: 25, found: true}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10}}
	watchdog := newTestWatchdog(t, source, store, newFakeRegistry(claimer), WithStartBlock(5), WithBlockWindow(10))

	result, err := watchdog.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(5), result.FromBlock)
	require.Equal(t, uint64(14), result.ToBlock)
	require.Equal(t, []blockRange{{from: 5, to: 14}}, source.ranges)

	result, err = watchdog.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(14), result.FromBlock)
	require.Equal(t, uint64(23), result.ToBlock)
	require.Equal(t, []blockRange{{from: 5, to: 14}, {from: 14, to: 23}}, source.ranges)
}

func TestPollOncePersistsCursorAfterSuccess(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{lastProcessedBlock: 20, found: true}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10}}
	watchdog := newTestWatchdog(t, source, store, newFakeRegistry(claimer), WithBlockWindow(7))

	result, err := watchdog.PollOnce(ctx)
	require.NoError(t, err)
	require.True(t, result.CursorAdvanced)

	cursor, ok := store.cursors[watchdog.cursorNameForDestination(10)]
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
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10}}
	registry := newFakeRegistry(claimer)
	watchdog := newTestWatchdog(t, source, store, registry, WithStartBlock(100), WithBlockWindow(2))

	result, err := watchdog.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, result.EnqueuedBridgeCount)
	require.Equal(t, 1, result.SkippedBridgeCount)
	require.Len(t, claimer.enqueued, 1)

	result, err = watchdog.PollOnce(ctx)
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
				makeSyncBridge(3, 1, 10, 3, 0),
				makeSyncBridge(4, autoclaimtypes.L1OriginNetwork, 99, 4, 0),
			},
		},
	}
	store := newMemoryCursorStore()
	claimer10 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10}}
	claimer11 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-11", DestinationNetwork: 11}}
	registry := newFakeRegistry(claimer10, claimer11)
	watchdog := newTestWatchdog(t, source, store, registry, WithBlockWindow(13))

	result, err := watchdog.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, result.BridgeCount)
	require.Equal(t, 2, result.MatchedBridgeCount)
	require.Equal(t, 2, result.EnqueuedBridgeCount)
	require.Equal(t, 2, result.IgnoredBridgeCount)
	require.Len(t, claimer10.enqueued, 1)
	require.Len(t, claimer11.enqueued, 1)
	require.Equal(t, uint32(10), claimer10.enqueued[0].DestinationNetwork)
	require.Equal(t, uint32(11), claimer11.enqueued[0].DestinationNetwork)
}

func TestBridgeSyncErrorDoesNotAdvanceCursor(t *testing.T) {
	ctx := context.Background()
	sourceErr := errors.New("bridge sync unavailable")
	source := &fakeBridgeSource{lastProcessedBlock: 12, found: true, getBridgesErr: sourceErr}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10}}
	watchdog := newTestWatchdog(t, source, store, newFakeRegistry(claimer), WithBlockWindow(13))

	_, err := watchdog.PollOnce(ctx)
	require.ErrorIs(t, err, sourceErr)
	require.Empty(t, store.cursors)
}

func TestRestartFromPersistedCursor(t *testing.T) {
	ctx := context.Background()
	source := &fakeBridgeSource{lastProcessedBlock: 60, found: true}
	store := newMemoryCursorStore()
	claimer := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10}}
	watchdog := newTestWatchdog(t, source, store, newFakeRegistry(claimer), WithBlockWindow(20), WithOverlapBlocks(2))
	store.cursors[watchdog.cursorNameForDestination(10)] = autoclaimtypes.BridgeCursor{
		FromBlock: 40,
		ToBlock:   50,
		BlockNum:  50,
	}

	result, err := watchdog.PollOnce(ctx)
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
	claimer10 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10}}
	claimer11 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-11", DestinationNetwork: 11}}
	watchdog := newTestWatchdog(
		t,
		source,
		store,
		newFakeRegistry(claimer10, claimer11),
		WithStartBlock(5),
		WithBlockWindow(10),
		WithOverlapBlocks(2),
	)
	store.cursors[watchdog.cursorNameForDestination(10)] = autoclaimtypes.BridgeCursor{
		FromBlock: 40,
		ToBlock:   50,
		BlockNum:  50,
	}

	result, err := watchdog.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(5), result.FromBlock)
	require.Equal(t, uint64(14), result.ToBlock)
	require.Equal(t, []blockRange{{from: 5, to: 14}}, source.ranges)
	require.Len(t, claimer11.enqueued, 1)
	_, ok := store.cursors[watchdog.cursorNameForDestination(11)]
	require.True(t, ok)
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
	claimer10 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10}}
	claimer11 := &fakeClaimer{target: autoclaimtypes.ClaimerTarget{ID: "claimer-11", DestinationNetwork: 11}}
	watchdog := newTestWatchdog(t, source, store, newFakeRegistry(claimer10, claimer11), WithBlockWindow(11))

	result, err := watchdog.PollOnce(ctx)
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
			ID:                 "claimer-1",
			DestinationNetwork: autoclaimtypes.LegacyZkEVMRollupNetwork,
		},
	}
	watchdog := newTestWatchdog(
		t,
		source,
		store,
		newFakeRegistry(claimer),
		WithBlockWindow(11),
		WithEtrogL1UpgradeBlock(10),
	)

	result, err := watchdog.PollOnce(ctx)
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
		target:  autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10},
		claimed: true,
	}
	watchdog := newTestWatchdog(t, source, store, newFakeRegistry(claimer), WithBlockWindow(11))

	result, err := watchdog.PollOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.EnqueuedBridgeCount)
	require.Equal(t, 1, result.IgnoredBridgeCount)
	require.True(t, result.CursorAdvanced)
	require.Empty(t, claimer.enqueued)
	require.Len(t, claimer.claimChecks, 1)
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
		target: autoclaimtypes.ClaimerTarget{ID: "claimer-10", DestinationNetwork: 10},
		err:    enqueueErr,
	}
	watchdog := newTestWatchdog(t, source, store, newFakeRegistry(claimer), WithBlockWindow(11))

	_, err := watchdog.PollOnce(ctx)
	require.ErrorIs(t, err, enqueueErr)
	require.Empty(t, store.cursors)
}

func newTestWatchdog(
	t *testing.T,
	source autoclaimtypes.BridgeSource,
	store CursorStore,
	registry autoclaimtypes.ClaimerRegistry,
	options ...Option,
) *L1ToL2 {
	t.Helper()

	options = append([]Option{WithNow(func() time.Time { return testNow })}, options...)
	watchdog, err := NewL1ToL2(source, store, registry, options...)
	require.NoError(t, err)
	return watchdog
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
	key := autoclaimtypes.DeriveRequestKey(bridge.OriginNetwork, bridge.DestinationNetwork, bridge.DepositCount)
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
