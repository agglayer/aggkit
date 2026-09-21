package bridgetracker

import (
	"context"
	"errors"
	"sync"
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
)

// ActivityEngineConfig holds the activity engine tunables. Zero values take the defaults above
type ActivityEngineConfig struct {
	// PollInterval is the period between refresh rounds over the supervised addresses
	PollInterval time.Duration
	// MaxConcurrentRefreshes bounds how many addresses tick refreshes at once: each refresh is
	// a full multi-network scan (see domain.ActivitySupervisedStore.RefreshAddress), so, unlike
	// the tracker's per-step resolutions, running every supervised address at once could spawn
	// far more outbound bridge-service/RPC calls than the configured concurrency limits on the
	// scanner side expect. A value <= 0 falls back to DefaultActivityEngineMaxConcurrentRefreshes
	MaxConcurrentRefreshes int
	// IdleTimeout is how long an unaccessed address is kept supervised before being forgotten
	// (see domain.ActivitySupervisedStore.PruneIdle). A value <= 0 falls back to
	// DefaultActivityEngineIdleTimeout
	IdleTimeout time.Duration
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
}

// NewActivityEngine returns an activity engine over the given store
func NewActivityEngine(
	cfg ActivityEngineConfig, logger aggkitcommon.Logger, store domain.ActivitySupervisedStore,
) (*ActivityEngine, error) {
	if store == nil {
		return nil, errors.New("activity engine requires an ActivitySupervisedStore")
	}
	return &ActivityEngine{logger: logger, cfg: cfg.withDefaults(), store: store, now: time.Now}, nil
}

// Start launches the refresh loop; it stops when ctx is cancelled. Besides the regular poll
// cadence, it also watches the store's ActivityTriggerable channel (if implemented) to refresh
// a freshly registered address right away instead of leaving it for the next tick — see
// resolveTriggered and domain.ActivitySupervisedStore.RegisterAndAwait, which is what a caller
// actually waits on
func (e *ActivityEngine) Start(ctx context.Context) {
	var triggers <-chan common.Address
	if triggerable, ok := e.store.(domain.ActivityTriggerable); ok {
		triggers = triggerable.Triggers()
	}

	go func() {
		ticker := time.NewTicker(e.cfg.PollInterval)
		defer ticker.Stop()

		e.tick(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.tick(ctx)
			case addr := <-triggers:
				e.resolveTriggered(ctx, addr)
			}
		}
	}()
}

// resolveTriggered refreshes a single freshly registered address immediately, outside of the
// regular poll cadence, so a caller blocked in RegisterAndAwait does not have to wait out a
// full PollInterval for its first real data. Errors are logged, never fatal to the loop
func (e *ActivityEngine) resolveTriggered(ctx context.Context, fromAddress common.Address) {
	if err := e.store.RefreshAddress(ctx, fromAddress); err != nil {
		e.logger.Warnf("failed to refresh triggered activity address %s: %v", fromAddress, err)
	}
}

// tick forgets the addresses whose idle timeout has elapsed (see ActivityEngineConfig.
// IdleTimeout) before enumerating who's left, then refreshes every remaining supervised address
// concurrently, since each is independent and may hit a different set of networks, up to
// ActivityEngineConfig.MaxConcurrentRefreshes at once. Pruning first matters most right after an
// upgrade from a store whose idle sweep used to be a no-op (see #1822): without it, every
// long-idle address accumulated under the old behavior would trigger a full multi-network scan
// on this first tick before being deleted anyway
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

	sem := make(chan struct{}, e.cfg.MaxConcurrentRefreshes)
	var wg sync.WaitGroup
	wg.Add(len(addrs))
	for _, addr := range addrs {
		sem <- struct{}{}
		go func(addr common.Address) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			if err := e.store.RefreshAddress(ctx, addr); err != nil {
				e.logger.Warnf("failed to refresh activity address %s: %v", addr, err)
			}
		}(addr)
	}
	wg.Wait()
}
