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
	// DefaultEngineIdleTimeout is the default time a bridge — terminal or still active — is
	// kept once nobody has read it (no Get/GetAndAwait) and it has no active WebSocket
	// subscriber. It bounds the memory an abandoned tracker holds onto regardless of whether it
	// ever resolves, on top of RetentionPeriod's grace period for the ones that do. Set well
	// above PollInterval and a plausible client poll cadence so a caller polling at a normal
	// pace never sees its bridge evicted between two of its own requests
	DefaultEngineIdleTimeout = 30 * time.Minute
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
	// IdleTimeout is how long an unaccessed, unsubscribed bridge — terminal or still active —
	// is kept before being forgotten (see DefaultEngineIdleTimeout)
	IdleTimeout time.Duration
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
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultEngineIdleTimeout
	}
	return cfg
}

// EngineSources groups the driven ports the engine resolves bridge facts through. Each is
// handed directly to the one or two step resolvers that need it (see createResolvers) rather
// than adapted into one do-everything port
type EngineSources struct {
	Bridges                BridgeEventSource
	Certificates           CertificateSource
	GERs                   GERSource
	WaitingGERUpdateSource domain.WaitingGERUpdateSource
	LERs                   LERSource
	Claims                 ClaimSource
	Settlement             SettlementSource
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
	now       func() time.Time
	resolvers map[types.BridgeStep]domain.StepResolver
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
	case sources.Settlement == nil:
		return nil, errors.New("engine requires a SettlementSource")
	}
	resolvers := createResolvers(logger, sources)

	return &Engine{
		logger:    logger,
		cfg:       cfg.withDefaults(),
		store:     store,
		sources:   sources,
		now:       time.Now,
		resolvers: resolvers,
	}, nil
}

func createResolvers(logger aggkitcommon.Logger, sources EngineSources) map[types.BridgeStep]domain.StepResolver {
	return map[types.BridgeStep]domain.StepResolver{
		types.StepWaitingGERUpdate:    domain.NewWaitingGERUpdateResolver(logger, sources.WaitingGERUpdateSource),
		types.StepWaitingLERUpdate:    domain.NewWaitingLERUpdateResolver(sources.LERs),
		types.StepPendingInclusion:    domain.NewPendingInclusionResolver(sources.Certificates),
		types.StepCertificatePending:  domain.NewCertificatePendingResolver(sources.Certificates),
		types.StepWaitL1SettledGER:    domain.NewWaitL1SettledGERResolver(sources.Settlement, sources.GERs),
		types.StepWaitingGERInjection: domain.NewWaitingGERInjectionResolver(sources.GERs),
		types.StepWaitingClaim:        domain.NewWaitingClaimResolver(sources.Claims),
	}
}

// Start launches the resolution loop; it stops when ctx is cancelled. Besides the regular
// poll cadence, it also watches the store's Triggerable channel (if implemented) to resolve a
// freshly registered bridge right away instead of leaving it for the next tick — see
// resolveTriggered and SupervisedStore.GetAndAwait, which is what a caller actually waits on
func (e *Engine) Start(ctx context.Context) {
	var triggers <-chan domain.TrackingID
	if triggerable, ok := e.store.(domain.Triggerable); ok {
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
			case id := <-triggers:
				e.resolveTriggered(ctx, id)
			}
		}
	}()
}

// resolveTriggered resolves a single freshly registered bridge immediately, outside of the
// regular poll cadence, so a caller blocked in GetAndAwait does not have to wait out a full
// PollInterval for its first real update. Errors are handled the same way as tick's per-bridge
// resolution: logged/persisted inside resolveBridgeStep, never fatal to the loop. A miss (the
// bridge was pruned, or the signal is stale) is silently ignored: the regular tick already
// covers anything still worth tracking
func (e *Engine) resolveTriggered(ctx context.Context, id domain.TrackingID) {
	tracking, err := e.store.Get(id, false)
	if err != nil {
		return
	}
	_ = e.resolveBridgeStep(ctx, tracking)
}

// tick runs one resolution round over the supervised list, then forgets the terminal entries
// whose retention has elapsed (see EngineConfig.RetentionPeriod) and the unaccessed,
// unsubscribed entries whose idle timeout has elapsed (see EngineConfig.IdleTimeout)
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

	prunedIdle, err := e.store.PruneIdle(e.now().Add(-e.cfg.IdleTimeout))
	if err != nil {
		e.logger.Warnf("failed to prune idle bridges: %v", err)
		return
	}
	if prunedIdle > 0 {
		e.logger.Infof("forgot %d idle bridges past the %s idle timeout", prunedIdle, e.cfg.IdleTimeout)
	}
}

// resolveBridgeStep resolves one step of progress for a bridge, incrementally: FindBridge
// only if its facts are not yet known (once resolved they are persisted and skipped on later
// ticks), then domain.ResolveSteps for whichever step is currently unmet — milestones already
// persisted as done are not re-queried, only the pending ones are (it naturally stops at the
// first unmet one). A source failure is persisted as a step-level error instead of
// only being logged — see persistStepError. On success, all mutations for the tick — including
// the bridge's tx-level facts (Info) the first time FindBridge resolves it — are merged into a
// single tx, committed through exactly one UpdateTrackingBridgeTx call (preceded by the step
// writes, if any changed) so subscribers only ever see one consistent, fully-merged snapshot:
// never one where BridgeStatus is already populated but AllSteps still reflects the bare,
// just-seeded pending path instead of what ResolveSteps actually computed for it
func (e *Engine) resolveBridgeStep(ctx context.Context, tracking *domain.TrackingData) error {
	ctx, cancel := context.WithTimeout(ctx, e.cfg.ResolveTimeout)
	defer cancel()

	id := tracking.ID()
	originalTx := tracking.BridgeTx()

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

	txChanged := originalTx.String() != stepped.BridgeTx().String()
	if txChanged || !reflect.DeepEqual(lastSteps, stepped.AllSteps()) {
		return e.persist(id, stepped.BridgeTx(), stepped.AllSteps())
	}
	return nil
}

// resolveBridgeTx returns the bridge's resolved facts and its current expected path. Unlike a
// transient/permanent failure (persisted immediately via persistResolveFailure, since there is
// nothing further to compute for it this tick), a success is deliberately left unpersisted: the
// caller (resolveBridgeStep) still has to run computeAllSteps over it, and commits both together
// in one call so subscribers never observe Info populated ahead of the steps it unlocked.
// domain.ResolveBridgeTx owns the FindBridge call and its outcome (see its doc); this method's
// own job is only logging and persisting the failure case — the IsDone guard is duplicated here
// so the FindBridge debug log below is skipped, too, once the bridge no longer needs it
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

	return resolved, nil
}

// persistStepError logs and persists a step-level failure: stepped already carries it (see
// domain.UpdateStep's stepErr, applied by ResolveSteps on error) — the accumulated retry count
// and description, and the failed step's Status turned Error. A later successful resolution
// clears it automatically: UpdateStep never carries a step's Error forward
func (e *Engine) persistStepError(id TrackingID, causeErr error, stepped *domain.TrackingData) {
	e.logger.Warnf("failed to resolve a step of bridge %s: %v", id, causeErr)
	_ = e.persist(id, stepped.BridgeTx(), stepped.AllSteps())
}

// persist writes allSteps (UpdateTrackingStep, silent) then tx (UpdateTrackingBridgeTx, which
// notifies) as a single commit, so subscribers see one consistent, fully-merged snapshot
// instead of one partial notification per field. Failures are logged here; the returned
// error lets callers abort the tick for this bridge
func (e *Engine) persist(id TrackingID, tx domain.TrackingBridgeTx, allSteps []BridgeStepPath) error {
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
	return domain.ResolveSteps(ctx, e.logger, e.resolvers, tracking, e.now())
}
