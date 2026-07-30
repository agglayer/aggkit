package bridgetracker

import (
	"context"
	"errors"
	"fmt"
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

// EngineSources groups the driven ports the engine resolves bridge facts through
type EngineSources struct {
	Bridges      BridgeEventSource
	Certificates CertificateSource
	GERs         GERSource
	LERs         LERSource
	Claims       ClaimSource
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
		e.resolveBridgeStep(ctx, tracking)
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
// ticks), then domain.DeriveStep for whichever step is currently unmet — milestones already
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
	tx := tracking.BridgeTx()

	info, lastSteps, err := e.resolveBridgeTx(ctx, tracking)
	if err != nil {
		e.logger.Debugf("resolving bridge %s fails", id)
		return err
	}
	// resolveBridgeTx persists its own updates to the store; keep the local copy in sync so
	// persistStepError/persist below don't overwrite it with the stale pre-call Info/Error
	tx.Info = info
	tx.Error = nil

	allSteps, err := e.computeAllSteps(ctx, info, lastSteps)
	if err != nil {
		e.persistStepError(id, tx, lastSteps, err)
		return err
	}

	if !reflect.DeepEqual(lastSteps, allSteps) {
		return e.persist(id, tx, allSteps)
	}
	return nil
}

// resolveBridgeTx returns the bridge's resolved facts and its current expected path, calling
// FindBridge and persisting the result if they are not yet known (or a previous resolution
// attempt left an outstanding Error to retry). The resolved bridge type reveals the full
// route, so a first resolution persists the tx together with the whole path as pending steps
// (domain.PendingPath) in one batch: clients see the complete way the bridge will walk from
// the very moment it resolves, before any milestone has been checked
func (e *Engine) resolveBridgeTx(
	ctx context.Context, tracking *domain.TrackingData,
) (*BridgeInfo, []types.BridgeStepPath, error) {
	if tracking == nil {
		return nil, nil, errors.New("nil tracking data")
	}
	tx := tracking.BridgeTx()
	if tx.IsDone() {
		return tx.Info, tracking.AllSteps(), nil
	}
	id := tracking.ID()
	if tx.StartDate.IsZero() {
		tx.StartDate = e.now()
		tx.Timeout = e.cfg.UnresolvedTimeout
	}

	e.logger.Debugf("resolving bridge %s through FindBridge", id)
	info, err := e.sources.Bridges.FindBridge(ctx, id)
	switch {
	case errors.Is(err, ErrBridgeTxNotFound):
		// the tx may simply not be mined yet: give it until Timeout before giving up
		e.handleUnresolved(id, tx, fmt.Sprintf("%s does not exist on the network", id))
		return nil, nil, err
	case errors.Is(err, ErrBridgeTxNotABridge), errors.Is(err, ErrSourceUnavailable):
		// permanent: either the tx exists and definitively is not a bridge tx, or the origin
		// network has no source configured to resolve it — retrying cannot change either
		e.handlePermanentFailure(id, tx, err)
		return nil, nil, err
	case err != nil:
		e.handleUnresolved(id, tx, err.Error())
		return nil, nil, err
	}
	tx.Info = info
	tx.Error = nil
	allSteps := tracking.AllSteps()
	if allSteps == nil {
		allSteps = domain.PendingPath(info.BridgeType(), e.now())
	}
	if err := e.persist(id, tx, allSteps); err != nil {
		return nil, nil, err
	}
	return info, allSteps, nil
}

// persistStepError marks the bridge's current step as failed, incrementing its retry count
// instead of silently discarding a transient source failure. lastSteps is the bridge's
// current expected path, always populated by resolveBridgeTx before any step is derived. A
// later successful resolution clears the error automatically: BuildSteps never carries an
// old step's Error forward
func (e *Engine) persistStepError(
	id TrackingID, tx domain.TrackingBridgeTx, lastSteps []types.BridgeStepPath, causeErr error,
) {
	e.logger.Warnf("failed to resolve a step of bridge %s: %v", id, causeErr)

	stepIndex := 0
	if idx := domain.NewTrackingData(id, tx, lastSteps).StepIndex(); idx != nil {
		stepIndex = *idx
	}

	current := lastSteps[stepIndex]
	retryCount, description := 1, []string{causeErr.Error()}
	if current.Error != nil {
		retryCount = current.Error.RetryCount + 1
		description = append(append([]string{}, current.Error.Description...), causeErr.Error())
	}
	current.Status = types.StepStatusError
	current.Error = &types.ErrorStep{
		ErrorType:   types.StepErrorTransient,
		RetryCount:  retryCount,
		Description: description,
	}

	if err := e.store.UpdateTrackingStep(id, uint(stepIndex), current); err != nil {
		e.logger.Warnf("failed to persist step error of bridge %s: %v", id, err)
		return
	}
	if err := e.store.UpdateTrackingBridgeTx(id, tx); err != nil {
		e.logger.Warnf("failed to persist bridge %s: %v", id, err)
	}
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

// handleUnresolved records a FindBridge failure (the tx not found yet, or any other transient
// source error) on the bridge's tx-level Error field, accumulating its retry count, and gives
// up once Timeout has elapsed since the bridge was first seen unresolved (StartDate) —
// regardless of the specific cause, so a source that keeps failing transiently doesn't retry
// forever. Giving up is expressed purely through the Error's type turning Exhausted:
// TrackingStatus derives Error from it (see TrackingData.TrackingStatus)
func (e *Engine) handleUnresolved(id TrackingID, tx domain.TrackingBridgeTx, cause string) {
	retryCount, description := 1, []string{cause}
	if tx.Error != nil {
		retryCount = tx.Error.RetryCount + 1
		description = append(append([]string{}, tx.Error.Description...), cause)
	}

	errorType := types.StepErrorTransient
	if tx.IsOutdated(e.now()) {
		e.logger.Infof("%s not resolved after %s, marking as failed", id, tx.Timeout)
		errorType = types.StepErrorExhausted
	}

	tx.Error = &types.ErrorStep{
		ErrorType:   errorType,
		RetryCount:  retryCount,
		Description: description,
	}

	if err := e.store.UpdateTrackingBridgeTx(id, tx); err != nil {
		e.logger.Warnf("failed to persist bridge %s: %v", id, err)
	}
}

// handlePermanentFailure marks a bridge as terminally failed because FindBridge returned an
// error that retrying cannot fix: its creating tx exists but is definitely not a bridge
// transaction (reverted, or mined without emitting a BridgeEvent log), or its origin network
// has no source configured to resolve it. Unlike handleUnresolved, there is no grace period.
// The Permanent error type is what makes TrackingStatus derive Error (see TrackingData.
// TrackingStatus)
func (e *Engine) handlePermanentFailure(id TrackingID, tx domain.TrackingBridgeTx, causeErr error) {
	e.logger.Infof("%s failed permanently, marking as failed: %v", id, causeErr)

	tx.Error = &types.ErrorStep{
		ErrorType:   types.StepErrorPermanent,
		Description: []string{causeErr.Error()},
	}

	if err := e.store.UpdateTrackingBridgeTx(id, tx); err != nil {
		e.logger.Warnf("failed to persist bridge %s: %v", id, err)
	}
}

// computeAllSteps builds the expected path of a resolved bridge from the fact sources;
// TrackingStatus and step index are not computed here, TrackingData derives them from the
// returned steps. The bridge's public BridgeStatus is derived separately, from info alone
// (see api.BridgeStatus). Resolution is incremental: lastAllSteps (as persisted by the
// previous tick) tells DeriveStep which milestones are already done so their sources are
// not queried again
func (e *Engine) computeAllSteps(
	ctx context.Context, info *BridgeInfo, lastAllSteps []types.BridgeStepPath,
) ([]types.BridgeStepPath, error) {
	res, err := domain.DeriveStep(ctx, info.NetworkID, info.DestinationNetwork,
		&bridgeFacts{sources: e.sources, bridge: info}, lastAllSteps)
	if err != nil {
		return nil, err
	}

	return domain.BuildSteps(info.BridgeType(), res, lastAllSteps, e.now()), nil
}

// bridgeFacts adapts the engine fact sources to the domain.BridgeFacts port for one bridge
type bridgeFacts struct {
	sources EngineSources
	bridge  *BridgeInfo
}

// OriginGER implements domain.BridgeFacts
func (f *bridgeFacts) OriginGER(ctx context.Context) (*types.GERData, error) {
	return f.sources.GERs.OriginGER(ctx, f.bridge)
}

// OriginLER implements domain.BridgeFacts
func (f *bridgeFacts) OriginLER(ctx context.Context) (*types.LERUpdateResult, error) {
	return f.sources.LERs.OriginLER(ctx, f.bridge)
}

// Certificate implements domain.BridgeFacts
func (f *bridgeFacts) Certificate(ctx context.Context) (*types.CertificateData, error) {
	return f.sources.Certificates.CertificateFor(ctx, f.bridge)
}

// InjectedGER implements domain.BridgeFacts
func (f *bridgeFacts) InjectedGER(ctx context.Context) (*types.GERData, error) {
	return f.sources.GERs.InjectedGER(ctx, f.bridge)
}

// ClaimFor implements domain.BridgeFacts
func (f *bridgeFacts) ClaimFor(ctx context.Context) (*types.ClaimResult, error) {
	return f.sources.Claims.ClaimFor(ctx, f.bridge)
}
