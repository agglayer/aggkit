package bridgetracker

import (
	"context"
	"sync"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// newTestActivityEngine wires an ActivityEngine over a fresh ActivityCache built from scanner/
// claims, mirroring newTestEngine's role for the tracker's own Engine
func newTestActivityEngine(
	t *testing.T, cfg ActivityEngineConfig, scanner ActivityBridgeScanner, claims ActivityClaimChecker,
) (*ActivityEngine, *ActivityCache) {
	t.Helper()

	cache := newTestActivityCache(scanner, claims)
	engine, err := NewActivityEngine(cfg, log.WithFields("module", "activity_engine_test"), cache)
	require.NoError(t, err)
	return engine, cache
}

// TestActivityEngineNewValidation pins that NewActivityEngine requires a non-nil store
func TestActivityEngineNewValidation(t *testing.T) {
	_, err := NewActivityEngine(ActivityEngineConfig{}, log.WithFields("module", "activity_engine_test"), nil)
	require.ErrorContains(t, err, "ActivitySupervisedStore")
}

// TestActivityEngineRefreshOneResolvesImmediately pins that refreshOne (the handler for a signal
// off the store's trigger channel, see ActivityEngine.Start) refreshes the given address right
// away, the same way one iteration of tick would, without needing a poll round over every
// supervised address
func TestActivityEngineRefreshOneResolvesImmediately(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false}}
	engine, cache := newTestActivityEngine(t, ActivityEngineConfig{}, scanner, claims)
	mustRegisterActivity(t, cache, testFromAddress, 0)

	engine.refreshOne(t.Context(), testFromAddress)

	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1, "refreshOne must refresh the address, not just be a no-op")
}

// TestActivityEngineRefreshOneIgnoresUnknownAddress pins that a signal for an address no longer
// supervised (e.g. pruned in the meantime) is silently ignored, never panics
func TestActivityEngineRefreshOneIgnoresUnknownAddress(t *testing.T) {
	engine, _ := newTestActivityEngine(t, ActivityEngineConfig{}, &fakeActivityScanner{}, &fakeActivityClaims{})

	require.NotPanics(t, func() {
		engine.refreshOne(t.Context(), testFromAddress)
	})
}

// TestActivityEngineStartResolvesTriggeredAddressBeforeNextPoll pins the end-to-end wiring
// ActivityEngine.Start sets up over an ActivityTriggerable store: PollInterval is set far in the
// future, so if the trigger channel were not being watched RegisterAndAwait would time out with
// nothing cached
func TestActivityEngineStartResolvesTriggeredAddressBeforeNextPoll(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	// two entries: Start's own immediate tick (harmless — nothing is registered yet at that
	// point) can race with this test's own RegisterAndAwait call below, occasionally causing a
	// second, concurrent refresh of the same freshly registered address
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}
	engine, cache := newTestActivityEngine(t, ActivityEngineConfig{PollInterval: time.Hour}, scanner, claims)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	engine.Start(ctx)

	mustRegisterActivity(t, cache, testFromAddress, time.Second)
	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 1,
		"the engine must refresh a freshly registered address via the trigger channel, not wait out PollInterval")
}

// TestActivityEngineTickRefreshesAllRegisteredAddresses verifies one tick refreshes every
// currently supervised address, not just the most recently registered one
func TestActivityEngineTickRefreshesAllRegisteredAddresses(t *testing.T) {
	other := common.HexToAddress("0x2222222222222222222222222222222222222222")
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{isClaimed: []bool{false, false}}
	engine, cache := newTestActivityEngine(t, ActivityEngineConfig{}, scanner, claims)

	mustRegisterActivity(t, cache, testFromAddress, 0)
	mustRegisterActivity(t, cache, other, 0)

	engine.tick(t.Context())

	for _, addr := range []common.Address{testFromAddress, other} {
		entries, _, err := cache.GetActivity(t.Context(), addr, false, types.ActivityFilterAll)
		require.NoError(t, err)
		require.Len(t, entries, 1, "tick must refresh %s", addr)
	}
}

// hookedActivityScanner is a minimal domain.ActivityBridgeScanner that runs hook synchronously
// inside every BridgesFrom call, letting a test observe/gate concurrent refreshes — mirrors
// fakeSources.findBridgeHook's role for the tracker's own TestEngineTickBoundsConcurrentResolutions
type hookedActivityScanner struct {
	hook func()
}

func (h *hookedActivityScanner) BridgesFrom(
	context.Context, common.Address, map[string]domain.KnownBridge,
) ([]*domain.ScannedBridge, []string, []domain.ActivityWarning, error) {
	h.hook()
	return nil, nil, nil, nil
}

// TestActivityEngineTickBoundsConcurrentRefreshes verifies tick never refreshes more than
// MaxConcurrentRefreshes addresses at once, parking the rest until a slot frees up
func TestActivityEngineTickBoundsConcurrentRefreshes(t *testing.T) {
	t.Parallel()

	const (
		maxConcurrent = 2
		activeCount   = 5
	)

	var (
		mu          sync.Mutex
		inFlight    int
		maxObserved int
	)
	release := make(chan struct{})
	scanner := &hookedActivityScanner{hook: func() {
		mu.Lock()
		inFlight++
		if inFlight > maxObserved {
			maxObserved = inFlight
		}
		mu.Unlock()

		<-release

		mu.Lock()
		inFlight--
		mu.Unlock()
	}}

	cache := newTestActivityCache(scanner, &fakeActivityClaims{})
	engine, err := NewActivityEngine(
		ActivityEngineConfig{MaxConcurrentRefreshes: maxConcurrent},
		log.WithFields("module", "activity_engine_test"), cache)
	require.NoError(t, err)

	for i := range activeCount {
		var addr common.Address
		addr[19] = byte(i + 1)
		mustRegisterActivity(t, cache, addr, 0)
	}

	done := make(chan struct{})
	go func() {
		engine.tick(t.Context())
		close(done)
	}()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return inFlight == maxConcurrent
	}, time.Second, time.Millisecond, "exactly maxConcurrent refreshes should be in flight, the rest parked")

	// give any bound violation a chance to show up before asserting on maxObserved
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	observed := maxObserved
	mu.Unlock()
	require.Equal(t, maxConcurrent, observed, "no more than MaxConcurrentRefreshes addresses refresh at once")

	close(release)
	<-done
}

// TestActivityEnginePruneIdleCalledEachTick verifies tick forgets addresses idle past
// IdleTimeout, proven indirectly by observing isClaimed() being asked again for a bridge that
// had already settled — which would not happen if its cached state had survived
func TestActivityEnginePruneIdleCalledEachTick(t *testing.T) {
	claim := &bridgeservicetypes.ClaimResponse{TxHash: "0xclaimtx"}
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	claims := &fakeActivityClaims{
		isClaimed: []bool{true, true},
		claimInfo: []*bridgeservicetypes.ClaimResponse{claim, claim},
	}
	cache := newTestActivityCache(scanner, claims)
	engine, err := NewActivityEngine(
		ActivityEngineConfig{IdleTimeout: time.Minute}, log.WithFields("module", "activity_engine_test"), cache)
	require.NoError(t, err)

	now := time.Now()
	cache.now = func() time.Time { return now }
	engine.now = func() time.Time { return now }

	mustRegisterActivity(t, cache, testFromAddress, 0)
	engine.tick(t.Context())
	entries, _, err := cache.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Equal(t, claim, entries[0].Claim)
	require.Equal(t, 1, claims.isClaimedCalls, "settled after the first tick")

	now = now.Add(2 * time.Minute) // past IdleTimeout

	engine.tick(t.Context())
	addrs, err := cache.GetActiveAddresses()
	require.NoError(t, err)
	require.Empty(t, addrs, "the idle address must be forgotten by tick's own PruneIdle call")
}

// TestActivityEngineTriggerNotStarvedBySlowTick verifies a freshly registered address's trigger
// is serviced — its own refresh actually started — while a slow tick over another address is
// still stuck mid-scan, reproducing PR #1856 review comment 4060973063 ("the trigger fast-path
// starves under load, and one request now schedules ~60 background scans"). Before decoupling
// tick from Start's select loop (see startTick/refreshOne), a tick that outlives PollInterval
// would starve the trigger channel for its whole duration, since tick ran inline in the very
// same goroutine that reads triggers — so RegisterAndAwait would time out and Execute would
// 503 for every new address until that one slow tick finally returned.
func TestActivityEngineTriggerNotStarvedBySlowTick(t *testing.T) {
	other := common.HexToAddress("0x3333333333333333333333333333333333333333")
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	scanner := &fakeActivityScanner{hook: func(int) { entered <- struct{}{}; <-release }}
	cache := newTestActivityCache(scanner, &fakeActivityClaims{})

	// PollInterval far in the future: only Start's own immediate startTick call is in play
	engine, err := NewActivityEngine(
		ActivityEngineConfig{PollInterval: time.Hour}, log.WithFields("module", "activity_engine_test"), cache)
	require.NoError(t, err)

	// other is already supervised before Start, so the immediate tick picks it up and blocks
	// inside the scanner hook for the rest of this test. Drain the trigger its own registration
	// just signaled: Start hasn't run yet, so nothing consumed it, and otherwise Start's select
	// loop would additionally refresh `other` via that leftover trigger concurrently with tick's
	// own refresh of it — a benign but real race on this fake scanner's unsynchronized bookkeeping
	// fields that has nothing to do with what this test is actually about
	mustRegisterActivity(t, cache, other, 0)
	<-cache.Triggers()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	engine.Start(ctx)

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Start's initial tick never reached the scanner")
	}

	// testFromAddress's own trigger must still be read and its refresh started promptly — timeout
	// <= 0 so RegisterAndAwait itself only signals the trigger and returns, without waiting on
	// the refresh it caused (which, sharing the same hooked scanner, would also block on release)
	require.False(t, mustRegisterActivity(t, cache, testFromAddress, 0))

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("testFromAddress's triggered refresh never reached the scanner — starved behind the slow tick")
	}

	close(release)
}
