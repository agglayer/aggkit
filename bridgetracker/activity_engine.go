package bridgetracker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

const (
	// DefaultActivityEnginePollInterval is the default period between activity refresh rounds
	DefaultActivityEnginePollInterval = 30 * time.Second
	// DefaultActivityEngineMaxConcurrentRefreshes is the default
	// ActivityEngineConfig.MaxConcurrentRefreshes: how many addresses tick refreshes at once
	DefaultActivityEngineMaxConcurrentRefreshes = 10
	// DefaultActivityEngineIdleTimeout is the default ActivityEngineConfig.IdleTimeout
	DefaultActivityEngineIdleTimeout = 30 * time.Minute
	// DefaultActivityEngineRefreshTimeout is the default ActivityEngineConfig.RefreshTimeout
	DefaultActivityEngineRefreshTimeout = 2 * time.Minute
)

// ActivityEngineConfig holds the activity engine tunables. Zero values take the defaults above
type ActivityEngineConfig struct {
	// PollInterval is the period between refresh rounds over the supervised addresses
	PollInterval time.Duration
	// MaxConcurrentRefreshes bounds how many addresses are refreshed at once in total — both
	// tick's regular sweep and the trigger fast-path share this same bound (see refreshOne) —
	// since each refresh is a full multi-network scan (see domain.ActivitySupervisedStore.
	// RefreshAddress), so, unlike the tracker's per-step resolutions, running every supervised
	// address at once could spawn far more outbound bridge-service/RPC calls than the configured
	// concurrency limits on the scanner side expect. A value <= 0 falls back to
	// DefaultActivityEngineMaxConcurrentRefreshes
	MaxConcurrentRefreshes int
	// IdleTimeout is how long an unaccessed address is kept supervised before being forgotten
	// (see domain.ActivitySupervisedStore.PruneIdle). A value <= 0 falls back to
	// DefaultActivityEngineIdleTimeout
	IdleTimeout time.Duration
	// RefreshTimeout bounds a single RefreshAddress call (see refreshOne), mirroring
	// EngineConfig.ResolveTimeout's reasoning for the tracker's own per-step resolutions: without
	// it, one hung network scan or RPC client can occupy a concurrency slot indefinitely, and —
	// because tick waits for every refresh it started before returning — wedge the whole loop
	// behind it (see tick's own doc). A value <= 0 falls back to
	// DefaultActivityEngineRefreshTimeout
	RefreshTimeout time.Duration
}

// withDefaults returns cfg with every zero-value tunable replaced by its default
func (cfg ActivityEngineConfig) withDefaults() ActivityEngineConfig {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultActivityEnginePollInterval
	}
	if cfg.MaxConcurrentRefreshes <= 0 {
		cfg.MaxConcurrentRefreshes = DefaultActivityEngineMaxConcurrentRefreshes
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultActivityEngineIdleTimeout
	}
	if cfg.RefreshTimeout <= 0 {
		cfg.RefreshTimeout = DefaultActivityEngineRefreshTimeout
	}
	return cfg
}

// ActivityEngine is the background counterpart of the activity endpoint: it watches the
// supervised from_addresses (see domain.ActivitySupervisedStore) and periodically refreshes
// each one's cache through the store itself (which owns the scanner/claims/tracker driven
// ports) — mirroring Engine's role for tracked tx IDs, but over from_addresses instead of
// (network, tx hash) pairs and with no per-step resolution: one refresh is one full scan
type ActivityEngine struct {
	logger aggkitcommon.Logger
	cfg    ActivityEngineConfig
	store  domain.ActivitySupervisedStore
	// now is the clock PruneIdle's cutoff is computed from, injectable for tests
	now func() time.Time

	// sem bounds the number of concurrent RefreshAddress calls in flight at once, shared by
	// both tick's regular sweep and the trigger fast-path (see refreshOne) — sized
	// cfg.MaxConcurrentRefreshes, so a flood of freshly registered addresses cannot run more
	// concurrent refreshes in total than a tick on its own would
	sem chan struct{}
	// ticking is true while a tick is in flight; guards against Start's loop stacking overlapping
	// ticks (see startTick)
	ticking atomic.Bool
}

// NewActivityEngine returns an activity engine over the given store
func NewActivityEngine(
	cfg ActivityEngineConfig, logger aggkitcommon.Logger, store domain.ActivitySupervisedStore,
) (*ActivityEngine, error) {
	if store == nil {
		return nil, errors.New("activity engine requires an ActivitySupervisedStore")
	}
	cfg = cfg.withDefaults()
	return &ActivityEngine{
		logger: logger, cfg: cfg, store: store, now: time.Now,
		sem: make(chan struct{}, cfg.MaxConcurrentRefreshes),
	}, nil
}

// Start launches the refresh loop; it stops when ctx is cancelled. Besides the regular poll
// cadence, it also watches the store's ActivityTriggerable channel (if implemented) to refresh
// a freshly registered address right away instead of leaving it for the next tick — see
// refreshOne and domain.ActivitySupervisedStore.RegisterAndAwait, which is what a caller
// actually waits on.
//
// Both ticks and triggered refreshes run in their own goroutine (see startTick, refreshOne)
// rather than inline in this select loop: tick alone can take far longer than one PollInterval
// once enough addresses are supervised (each refresh is a full multi-network scan), and running
// it inline here would stop this loop from reading triggers for that whole time — starving every
// freshly registered address's fast path behind whatever tick happened to be running, so
// RegisterAndAwait always times out and Execute always 503s for as long as that lasts
func (e *ActivityEngine) Start(ctx context.Context) {
	var triggers <-chan common.Address
	if triggerable, ok := e.store.(domain.ActivityTriggerable); ok {
		triggers = triggerable.Triggers()
	}

	go func() {
		ticker := time.NewTicker(e.cfg.PollInterval)
		defer ticker.Stop()

		e.startTick(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.startTick(ctx)
			case addr := <-triggers:
				go e.refreshOne(ctx, addr)
			}
		}
	}()
}

// startTick launches tick in its own goroutine, skipping this poll instead of stacking an
// overlapping tick if the previous one is still running (see tick's own doc for why one can run
// long) — this is what lets Start's select loop keep reading triggers while a tick is in
// progress instead of blocking on it inline
func (e *ActivityEngine) startTick(ctx context.Context) {
	if !e.ticking.CompareAndSwap(false, true) {
		e.logger.Warnf("activity engine: skipping poll tick, the previous one is still refreshing")
		return
	}
	go func() {
		defer e.ticking.Store(false)
		e.tick(ctx)
	}()
}

// refreshOne acquires a slot in the shared refresh concurrency bound (see
// ActivityEngineConfig.MaxConcurrentRefreshes) and runs one refresh for addr, bounded by
// ActivityEngineConfig.RefreshTimeout so one hung network scan or RPC client cannot occupy that
// slot indefinitely. Shared by both tick's regular sweep and the trigger fast-path. Errors are
// logged, never fatal to the loop
func (e *ActivityEngine) refreshOne(ctx context.Context, fromAddress common.Address) {
	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-e.sem }()

	if ctx.Err() != nil {
		return
	}
	refreshCtx, cancel := context.WithTimeout(ctx, e.cfg.RefreshTimeout)
	defer cancel()
	if err := e.store.RefreshAddress(refreshCtx, fromAddress); err != nil {
		e.logger.Warnf("failed to refresh activity address %s: %v", fromAddress, err)
	}
}

// tick forgets the addresses whose idle timeout has elapsed (see ActivityEngineConfig.
// IdleTimeout) before enumerating who's left, then refreshes every remaining supervised address
// concurrently, since each is independent and may hit a different set of networks — bounded by
// the same shared semaphore refreshOne uses for the trigger fast-path (see
// ActivityEngineConfig.MaxConcurrentRefreshes). Pruning first matters most right after an
// upgrade from a store whose idle sweep used to be a no-op (see #1822): without it, every
// long-idle address accumulated under the old behavior would trigger a full multi-network scan
// on this first tick before being deleted anyway.
//
// Called from its own goroutine (see startTick): the addresses supervised at the moment tick
// started can take arbitrarily long to all finish refreshing (again, see RefreshTimeout), so
// nothing about running this inline may ever assume it completes within one PollInterval
func (e *ActivityEngine) tick(ctx context.Context) {
	pruned, err := e.store.PruneIdle(e.now().Add(-e.cfg.IdleTimeout))
	if err != nil {
		e.logger.Warnf("failed to prune idle activity addresses: %v", err)
	} else if pruned > 0 {
		e.logger.Infof("forgot %d idle activity addresses past the %s idle timeout", pruned, e.cfg.IdleTimeout)
	}
	if ctx.Err() != nil {
		return
	}

	addrs, err := e.store.GetActiveAddresses()
	if err != nil {
		e.logger.Warnf("failed to list active activity addresses: %v", err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(len(addrs))
	for _, addr := range addrs {
		go func(addr common.Address) {
			defer wg.Done()
			e.refreshOne(ctx, addr)
		}(addr)
	}
	wg.Wait()
}
