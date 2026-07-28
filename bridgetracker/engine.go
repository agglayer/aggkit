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

// tick runs one resolution round over the supervised list
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
}

// resolveBridgeStep resolves one step of progress for a bridge: FindBridge if its facts are
// not yet known, then domain.DeriveStep for whichever step is currently unmet (it naturally
// stops at the first one). A source failure is persisted as a step-level error instead of
// only being logged — see persistStepError. On success, all mutations for the tick are merged
// into a single tx, committed through exactly one UpdateTrackingBridgeTx call (preceded by
// the step writes, if any changed) so subscribers see one consistent, fully-merged snapshot
// instead of one partial notification per field
func (e *Engine) resolveBridgeStep(ctx context.Context, tracking *domain.TrackingData) error {
	ctx, cancel := context.WithTimeout(ctx, e.cfg.ResolveTimeout)
	defer cancel()

	id := tracking.ID()
	tx := tracking.BridgeTx()

	info, err := e.resolveBridgeTx(ctx, tracking)
	if err != nil {
		e.logger.Debugf("resolving bridge %s fails", id)
		return err
	}
	// resolveBridgeTx persists its own tx update to the store; keep the local copy in sync so
	// persistStepError/persist below don't overwrite it with the stale pre-call Info/Error
	tx.Info = info
	tx.Error = nil

	allSteps, err := e.computeAllSteps(ctx, info, tracking.AllSteps())
	if err != nil {
		// tx is committed regardless: it may carry a freshly resolved Info even though this
		// tick's step failed, and the next tick must not re-resolve it via FindBridge
		e.persistStepError(id, tx, info, tracking, err)
		return err
	}

	if !reflect.DeepEqual(tracking.AllSteps(), allSteps) {
		e.persist(id, tracking.RawTrackingStatus(), tx, allSteps)
	}
	return nil
}

// resolveBridgeTx returns the bridge's resolved facts, calling FindBridge and persisting the
// result if they are not yet known (or a previous resolution attempt left an outstanding
// Error to retry)
func (e *Engine) resolveBridgeTx(ctx context.Context, tracking *domain.TrackingData) (*BridgeInfo, error) {
	if tracking == nil {
		return nil, errors.New("nil tracking data")
	}
	tx := tracking.BridgeTx()
	if tx.IsDone() {
		return tx.Info, nil
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
		e.handleUnresolved(id, tracking.RawTrackingStatus(), tx, fmt.Sprintf("%s does not exist on the network", id))
		return nil, err
	case errors.Is(err, ErrBridgeTxNotABridge):
		// permanent: the tx exists and definitively is not a bridge tx, retrying cannot
		// change that
		e.handleNotABridge(id, tx, err)
		return nil, err
	case err != nil:
		e.handleUnresolved(id, tracking.RawTrackingStatus(), tx, err.Error())
		return nil, err
	}
	tx.Info = info
	tx.Error = nil
	if err := e.store.UpdateTrackingBridgeTx(id, tracking.RawTrackingStatus(), tx); err != nil {
		e.logger.Warnf("failed to persist bridge %s: %v", id, err)
		return nil, err
	}
	return info, nil
}

// persistStepError marks the bridge's current step (or, for a bridge resolved for the first
// time this tick, the first step of its expected path) as failed, incrementing its retry
// count instead of silently discarding a transient source failure. A later successful
// resolution clears it automatically: BuildSteps never carries an old step's Error forward
func (e *Engine) persistStepError(
	id TrackingID, tx domain.TrackingBridgeTx, info *BridgeInfo, tracking *domain.TrackingData, causeErr error,
) {
	e.logger.Warnf("failed to resolve a step of bridge %s: %v", id, causeErr)

	allSteps := tracking.AllSteps()
	stepIndex := 0
	if allSteps == nil {
		path := domain.ExpectedPath(info.BridgeType())
		allSteps = make([]types.BridgeStepPath, len(path))
		for i, step := range path {
			allSteps[i] = types.BridgeStepPath{Step: step, Status: types.StepStatusPending}
		}
	} else {
		stepIndex = *tracking.StepIndex()
	}

	current := allSteps[stepIndex]
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
	if err := e.store.UpdateTrackingBridgeTx(id, tracking.RawTrackingStatus(), tx); err != nil {
		e.logger.Warnf("failed to persist bridge %s: %v", id, err)
	}
}

// persist writes allSteps (UpdateTrackingStep, silent) then tx (UpdateTrackingBridgeTx, which
// notifies) as a single commit, so subscribers see one consistent, fully-merged snapshot
// instead of one partial notification per field
func (e *Engine) persist(
	id TrackingID, trackingStatus types.TrackingStatus, tx domain.TrackingBridgeTx,
	allSteps []types.BridgeStepPath,
) {
	for i, step := range allSteps {
		if err := e.store.UpdateTrackingStep(id, uint(i), step); err != nil {
			e.logger.Warnf("failed to persist step %d of bridge %s: %v", i, id, err)
			return
		}
	}

	if err := e.store.UpdateTrackingBridgeTx(id, trackingStatus, tx); err != nil {
		e.logger.Warnf("failed to persist status of bridge %s: %v", id, err)
	}
}

// handleUnresolved records a FindBridge failure (the tx not found yet, or any other transient
// source error) on the bridge's tx-level Error field, accumulating its retry count, and gives
// up (TrackingStatus becomes Error) once Timeout has elapsed since the bridge was first seen
// unresolved (StartDate) — regardless of the specific cause, so a source that keeps failing
// transiently doesn't retry forever. trackingStatus is the bridge's current raw status,
// carried through unchanged unless the bridge is now given up on
func (e *Engine) handleUnresolved(
	id TrackingID, trackingStatus types.TrackingStatus, tx domain.TrackingBridgeTx, cause string,
) {
	retryCount, description := 1, []string{cause}
	if tx.Error != nil {
		retryCount = tx.Error.RetryCount + 1
		description = append(append([]string{}, tx.Error.Description...), cause)
	}

	errorType := types.StepErrorTransient
	if e.now().Sub(tx.StartDate) >= tx.Timeout {
		e.logger.Infof("%s not resolved after %s, marking as failed", id, tx.Timeout)
		trackingStatus = types.TrackingStatusError
		errorType = types.StepErrorExhausted
	}

	tx.Error = &types.ErrorStep{
		ErrorType:   errorType,
		RetryCount:  retryCount,
		Description: description,
	}

	if err := e.store.UpdateTrackingBridgeTx(id, trackingStatus, tx); err != nil {
		e.logger.Warnf("failed to persist bridge %s: %v", id, err)
	}
}

// handleNotABridge marks a bridge as terminally failed because its creating tx exists but is
// definitely not a bridge transaction (reverted, or mined without emitting a BridgeEvent
// log). Unlike handleUnresolved, there is no grace period: retrying an already-mined tx
// cannot change the outcome
func (e *Engine) handleNotABridge(id TrackingID, tx domain.TrackingBridgeTx, causeErr error) {
	e.logger.Infof("%s is not a bridge transaction, marking as failed: %v", id, causeErr)

	tx.Error = &types.ErrorStep{
		ErrorType:   types.StepErrorPermanent,
		Description: []string{causeErr.Error()},
	}

	if err := e.store.UpdateTrackingBridgeTx(id, types.TrackingStatusError, tx); err != nil {
		e.logger.Warnf("failed to persist bridge %s: %v", id, err)
	}
}

// computeAllSteps builds the expected path of a resolved bridge from the fact sources;
// TrackingStatus and step index are not computed here, TrackingData derives them from the
// returned steps. The bridge's public BridgeStatus is derived separately, from info alone
// (see api.BridgeStatus)
func (e *Engine) computeAllSteps(
	ctx context.Context, info *BridgeInfo, lastAllSteps []types.BridgeStepPath,
) ([]types.BridgeStepPath, error) {
	res, err := domain.DeriveStep(ctx, info.NetworkID, info.DestinationNetwork,
		&bridgeFacts{sources: e.sources, bridge: info})
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
