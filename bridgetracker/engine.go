package bridgetracker

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

const (
	// DefaultEnginePollInterval is the default period between engine resolution rounds
	DefaultEnginePollInterval = 10 * time.Second
	// DefaultEngineResolveTimeout is the default per-bridge budget for one resolution
	DefaultEngineResolveTimeout = 30 * time.Second
	// DefaultEngineUnresolvedTimeout is the default time a supervised tx is given to resolve
	// (FindBridge succeeding) before it is marked as terminally failed, regardless of why it
	// hasn't resolved (the tx may not be mined yet, or a source keeps failing transiently)
	DefaultEngineUnresolvedTimeout = 30 * time.Second
	// DefaultEngineRetentionPeriod is the default time a terminal bridge (Finished, or failed
	// to ever resolve) stays queryable before being forgotten. Clients polling or subscribed
	// observe the terminal TrackingStatus during this window; once forgotten, a new request
	// for the same tx re-registers it and tracking restarts from scratch — the retry path for
	// a tx the tracker gave up on
	DefaultEngineRetentionPeriod = 10 * time.Minute
)

// EngineConfig holds the tracking engine tunables. Zero values take the defaults above
type EngineConfig struct {
	// PollInterval is the period between resolution rounds over the supervised list
	PollInterval time.Duration
	// ResolveTimeout bounds the source calls of a single bridge resolution
	ResolveTimeout time.Duration
	// UnresolvedTimeout is how long a supervised tx is given, since first seen unresolved, to
	// have FindBridge succeed before it is marked terminally failed (TrackingStatus becomes
	// Error, with an exhausted ErrorStep explaining why)
	UnresolvedTimeout time.Duration
	// RetentionPeriod is how long a terminal bridge stays queryable before being forgotten
	// (see DefaultEngineRetentionPeriod)
	RetentionPeriod time.Duration
}

// withDefaults returns cfg with every zero-value tunable replaced by its default
func (cfg EngineConfig) withDefaults() EngineConfig {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultEnginePollInterval
	}
	if cfg.ResolveTimeout <= 0 {
		cfg.ResolveTimeout = DefaultEngineResolveTimeout
	}
	if cfg.UnresolvedTimeout <= 0 {
		cfg.UnresolvedTimeout = DefaultEngineUnresolvedTimeout
	}
	if cfg.RetentionPeriod <= 0 {
		cfg.RetentionPeriod = DefaultEngineRetentionPeriod
	}
	return cfg
}

// EngineSources groups the driven ports the engine resolves bridge facts through. It
// implements domain.BridgeFacts directly (see the methods below): every fact method already
// takes the bridge it is about, so there is nothing left for a dedicated adapter to bind
type EngineSources struct {
	Bridges      BridgeEventSource
	Certificates CertificateSource
	GERs         GERSource
	LERs         LERSource
	Claims       ClaimSource
}

// OriginGER implements domain.BridgeFacts
func (s EngineSources) OriginGER(ctx context.Context, bridge *BridgeInfo) (*types.GERData, error) {
	return s.GERs.OriginGER(ctx, bridge)
}

// OriginLER implements domain.BridgeFacts
func (s EngineSources) OriginLER(ctx context.Context, bridge *BridgeInfo) (*types.LERUpdateResult, error) {
	return s.LERs.OriginLER(ctx, bridge)
}

// Certificate implements domain.BridgeFacts
func (s EngineSources) Certificate(ctx context.Context, bridge *BridgeInfo) (*types.CertificateData, error) {
	return s.Certificates.CertificateFor(ctx, bridge)
}

// InjectedGER implements domain.BridgeFacts
func (s EngineSources) InjectedGER(ctx context.Context, bridge *BridgeInfo) (*types.GERData, error) {
	return s.GERs.InjectedGER(ctx, bridge)
}

// ClaimFor implements domain.BridgeFacts
func (s EngineSources) ClaimFor(ctx context.Context, bridge *BridgeInfo) (*types.ClaimResult, error) {
	return s.Claims.ClaimFor(ctx, bridge)
}

// Engine is the tracking engine: it watches the supervised list, resolves the status of
// every active bridge through the fact sources and stores each change (which the registry
// fans out to REST polls and WebSocket subscribers). All engine-private resolution state
// (resolved bridge facts, not-found counter, last published status/steps) lives in the store
// itself, as part of each bridge's TrackingData
type Engine struct {
	logger  aggkitcommon.Logger
	cfg     EngineConfig
	store   SupervisedStore
	sources EngineSources
	// now is the clock, injectable for tests
	now func() time.Time
}

// NewEngine returns a tracking engine over the given store and fact sources
func NewEngine(
	cfg EngineConfig,
	logger aggkitcommon.Logger,
	store SupervisedStore,
	sources EngineSources,
) (*Engine, error) {
	switch {
	case store == nil:
		return nil, errors.New("engine requires a SupervisedStore")
	case sources.Bridges == nil:
		return nil, errors.New("engine requires a BridgeEventSource")
	case sources.Certificates == nil:
		return nil, errors.New("engine requires a CertificateSource")
	case sources.GERs == nil:
		return nil, errors.New("engine requires a GERSource")
	case sources.LERs == nil:
		return nil, errors.New("engine requires a LERSource")
	case sources.Claims == nil:
		return nil, errors.New("engine requires a ClaimSource")
	}

	return &Engine{
		logger:  logger,
		cfg:     cfg.withDefaults(),
		store:   store,
		sources: sources,
		now:     time.Now,
	}, nil
}

// Start launches the resolution loop; it stops when ctx is cancelled
func (e *Engine) Start(ctx context.Context) {
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
			}
		}
	}()
}

// tick runs one resolution round over the supervised list, then forgets the terminal
// entries whose retention has elapsed (see EngineConfig.RetentionPeriod)
func (e *Engine) tick(ctx context.Context) {
	active, err := e.store.GetTrackerActives(nil)
	if err != nil {
		e.logger.Warnf("failed to list active bridges: %v", err)
		return
	}

	for _, tracking := range active {
		// TODO: In the future it must be done in parallel
		if ctx.Err() != nil {
			return
		}
		// errors are already logged/persisted inside resolveBridgeStep; one bridge failing to
		// resolve must not stop the tick for the rest of the active list
		_ = e.resolveBridgeStep(ctx, tracking)
	}

	pruned, err := e.store.PruneTerminal(e.now().Add(-e.cfg.RetentionPeriod))
	if err != nil {
		e.logger.Warnf("failed to prune terminal bridges: %v", err)
		return
	}
	if pruned > 0 {
		e.logger.Infof("forgot %d terminal bridges past the %s retention", pruned, e.cfg.RetentionPeriod)
	}
}

// resolveBridgeStep resolves one step of progress for a bridge, incrementally: FindBridge
// only if its facts are not yet known (once resolved they are persisted and skipped on later
// ticks), then domain.ResolveSteps for whichever step is currently unmet — milestones already
// persisted as done are not re-queried, only the pending ones are (it naturally stops at the
// first unmet one). A source failure is persisted as a step-level error instead of
// only being logged — see persistStepError. On success, all mutations for the tick are merged
// into a single tx, committed through exactly one UpdateTrackingBridgeTx call (preceded by
// the step writes, if any changed) so subscribers see one consistent, fully-merged snapshot
// instead of one partial notification per field
func (e *Engine) resolveBridgeStep(ctx context.Context, tracking *domain.TrackingData) error {
	ctx, cancel := context.WithTimeout(ctx, e.cfg.ResolveTimeout)
	defer cancel()

	id := tracking.ID()

	resolved, err := e.resolveBridgeTx(ctx, tracking)
	if err != nil {
		e.logger.Debugf("resolving bridge %s fails", id)
		return err
	}
	lastSteps := resolved.AllSteps()

	stepped, err := e.computeAllSteps(ctx, resolved)
	if err != nil {
		e.persistStepError(id, err, stepped)
		return err
	}

	if !reflect.DeepEqual(lastSteps, stepped.AllSteps()) {
		return e.persist(id, stepped.BridgeTx(), stepped.AllSteps())
	}
	return nil
}

// resolveBridgeTx returns the bridge's resolved facts and its current expected path, persisting
// the result if it is not yet known (or a previous resolution attempt left an outstanding Error
// to retry). domain.ResolveBridgeTx owns the FindBridge call and its outcome (see its doc); this
// method's own job is only logging and persistence — the IsDone guard is duplicated here so the
// FindBridge debug log below is skipped, too, once the bridge no longer needs it
func (e *Engine) resolveBridgeTx(
	ctx context.Context, tracking *domain.TrackingData,
) (*domain.TrackingData, error) {
	if tracking == nil {
		return nil, errors.New("nil tracking data")
	}
	if tracking.BridgeTx().IsDone() {
		return tracking, nil
	}
	id := tracking.ID()

	e.logger.Debugf("resolving bridge %s through FindBridge", id)
	resolved, err := domain.ResolveBridgeTx(ctx, e.sources.Bridges, tracking, e.cfg.UnresolvedTimeout, e.now())
	if err != nil {
		e.persistResolveFailure(id, resolved.BridgeTx(), err)
		return nil, err
	}

	if perr := e.persist(id, resolved.BridgeTx(), resolved.AllSteps()); perr != nil {
		return nil, perr
	}
	return resolved, nil
}

// persistStepError logs and persists a step-level failure: stepped already carries it (see
// domain.MarkStepError, applied by ResolveSteps on error) — the accumulated retry count and
// description, and the failed step's Status turned Error. A later successful resolution clears
// it automatically: UpdateStep never carries a step's Error forward
func (e *Engine) persistStepError(id TrackingID, causeErr error, stepped *domain.TrackingData) {
	e.logger.Warnf("failed to resolve a step of bridge %s: %v", id, causeErr)
	_ = e.persist(id, stepped.BridgeTx(), stepped.AllSteps())
}

// persist writes allSteps (UpdateTrackingStep, silent) then tx (UpdateTrackingBridgeTx, which
// notifies) as a single commit, so subscribers see one consistent, fully-merged snapshot
// instead of one partial notification per field. Failures are logged here; the returned
// error lets callers abort the tick for this bridge
func (e *Engine) persist(id TrackingID, tx domain.TrackingBridgeTx, allSteps []types.BridgeStepPath) error {
	for i, step := range allSteps {
		if err := e.store.UpdateTrackingStep(id, uint(i), step); err != nil {
			e.logger.Warnf("failed to persist step %d of bridge %s: %v", i, id, err)
			return err
		}
	}

	if err := e.store.UpdateTrackingBridgeTx(id, tx); err != nil {
		e.logger.Warnf("failed to persist status of bridge %s: %v", id, err)
		return err
	}
	return nil
}

// persistResolveFailure persists a domain.ResolveBridgeTx failure outcome (tx already carries
// the resulting Error — permanent, or transient possibly turned Exhausted) and logs the two
// cases that mark a bridge as terminally failed: a permanent cause, or a transient one that
// just ran out of its give-up grace period (Exhausted). TrackingStatus derives Error from tx
// (see TrackingData.TrackingStatus)
func (e *Engine) persistResolveFailure(id TrackingID, tx domain.TrackingBridgeTx, causeErr error) {
	if tx.Error != nil {
		switch tx.Error.ErrorType {
		case types.StepErrorPermanent:
			e.logger.Infof("%s failed permanently, marking as failed: %v", id, causeErr)
		case types.StepErrorExhausted:
			e.logger.Infof("%s not resolved after %s, marking as failed", id, tx.Timeout)
		}
	}

	if err := e.store.UpdateTrackingBridgeTx(id, tx); err != nil {
		e.logger.Warnf("failed to persist bridge %s: %v", id, err)
	}
}

// computeAllSteps advances a resolved bridge (tracking.BridgeTx().IsDone(), AllSteps already
// seeded) through as much of its expected path as its current facts allow — see
// domain.ResolveSteps. TrackingStatus and step index are not computed here, TrackingData
// derives them from the returned steps. The bridge's public BridgeStatus is derived separately,
// from info alone (see api.BridgeStatus)
func (e *Engine) computeAllSteps(
	ctx context.Context, tracking *domain.TrackingData,
) (*domain.TrackingData, error) {
	return domain.ResolveSteps(ctx, e.sources, tracking, e.now())
}
