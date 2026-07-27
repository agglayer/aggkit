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
	// DefaultEngineNotFoundAfter is the default number of consecutive not-found polls
	// before a supervised tx is marked as terminally failed (the tx may not be mined yet
	// when the client registers it)
	DefaultEngineNotFoundAfter = 3
)

// EngineConfig holds the tracking engine tunables. Zero values take the defaults above
type EngineConfig struct {
	// PollInterval is the period between resolution rounds over the supervised list
	PollInterval time.Duration
	// ResolveTimeout bounds the source calls of a single bridge resolution
	ResolveTimeout time.Duration
	// NotFoundAfter is the number of consecutive ErrBridgeTxNotFound polls after which the
	// bridge is marked terminally failed (TrackingStatus becomes Error, with an exhausted
	// ErrorStep explaining why)
	NotFoundAfter int
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
// fans out to REST polls and WebSocket subscribers)
type Engine struct {
	logger  aggkitcommon.Logger
	cfg     EngineConfig
	store   SupervisedStore
	sources EngineSources
	// now is the clock, injectable for tests
	now func() time.Time

	// tracked is the engine-private state per supervised bridge. It is only touched from
	// the single resolution goroutine, so it needs no locking
	tracked map[BridgeKey]*trackedBridge
}

// trackedBridge is the engine-private state of one supervised bridge
type trackedBridge struct {
	// info holds the immutable bridge facts, nil until FindBridge succeeds
	info *BridgeInfo
	// notFoundCount counts consecutive ErrBridgeTxNotFound resolutions
	notFoundCount int
	// lastStatus is the last status stored, to publish only on change
	lastStatus *types.BridgeStatus
	// lastAllSteps is the last expected path stored, to publish only on change and carry
	// step dates/results over to the next resolution
	lastAllSteps []types.BridgeStepPath
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

	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultEnginePollInterval
	}
	if cfg.ResolveTimeout <= 0 {
		cfg.ResolveTimeout = DefaultEngineResolveTimeout
	}
	if cfg.NotFoundAfter <= 0 {
		cfg.NotFoundAfter = DefaultEngineNotFoundAfter
	}

	return &Engine{
		logger:  logger,
		cfg:     cfg,
		store:   store,
		sources: sources,
		now:     time.Now,
		tracked: make(map[BridgeKey]*trackedBridge),
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

// tick runs one resolution round over the supervised list and drops the private state of
// bridges that no longer need tracking (claimed or terminally failed)
func (e *Engine) tick(ctx context.Context) {
	active := e.store.ActiveBridges()

	activeSet := make(map[BridgeKey]struct{}, len(active))
	for _, key := range active {
		if ctx.Err() != nil {
			return
		}
		activeSet[key] = struct{}{}
		e.resolveBridge(ctx, key)
	}

	for key := range e.tracked {
		if _, ok := activeSet[key]; !ok {
			delete(e.tracked, key)
		}
	}
}

// resolveBridge resolves the current status of one bridge and stores it if it changed.
// Transient source errors only log: the bridge is retried on the next round
func (e *Engine) resolveBridge(ctx context.Context, key BridgeKey) {
	ctx, cancel := context.WithTimeout(ctx, e.cfg.ResolveTimeout)
	defer cancel()

	t, ok := e.tracked[key]
	if !ok {
		t = &trackedBridge{}
		e.tracked[key] = t
	}

	if t.info == nil {
		info, err := e.sources.Bridges.FindBridge(ctx, key.NetworkID, key.TxHash)
		switch {
		case errors.Is(err, ErrBridgeTxNotFound):
			t.notFoundCount++
			if t.notFoundCount >= e.cfg.NotFoundAfter {
				e.logger.Infof("tx %s not found on network %d after %d polls, marking as failed",
					key.TxHash, key.NetworkID, t.notFoundCount)
				e.store.SetError(key.NetworkID, key.TxHash, &types.ErrorStep{
					ErrorType:  types.StepErrorExhausted,
					RetryCount: t.notFoundCount,
					Description: []string{fmt.Sprintf("transaction %s does not exist on network %d "+
						"or is not a bridge transaction", key.TxHash, key.NetworkID)},
				})
				delete(e.tracked, key)
			}
			return
		case err != nil:
			e.logger.Warnf("failed to resolve bridge tx %s (network %d): %v", key.TxHash, key.NetworkID, err)
			return
		}
		t.info = info
		t.notFoundCount = 0
	}

	trackingStatus, status, stepIndex, allSteps, err := e.computeStatus(ctx, t)
	if err != nil {
		e.logger.Warnf("failed to compute status of bridge tx %s (network %d): %v",
			key.TxHash, key.NetworkID, err)
		return
	}

	if !reflect.DeepEqual(t.lastStatus, status) || !reflect.DeepEqual(t.lastAllSteps, allSteps) {
		e.store.SetStatus(key.NetworkID, key.TxHash, trackingStatus, status, &stepIndex, allSteps)
		t.lastStatus = status
		t.lastAllSteps = allSteps
	}
}

// computeStatus builds the full BridgeStatus, step index and expected path of a resolved
// bridge from the fact sources, along with the TrackingStatus it resolves to
func (e *Engine) computeStatus(
	ctx context.Context, t *trackedBridge,
) (types.TrackingStatus, *types.BridgeStatus, int, []types.BridgeStepPath, error) {
	res, err := domain.DeriveStep(ctx, t.info.Key.NetworkID, t.info.DestinationNetwork,
		&bridgeFacts{sources: e.sources, bridge: t.info})
	if err != nil {
		return 0, nil, 0, nil, err
	}

	bridgeType := t.info.BridgeType()
	allSteps := domain.BuildSteps(bridgeType, res, t.lastAllSteps, e.now())
	trackingStatus, stepIndex := domain.Lifecycle(allSteps, res.Step)
	return trackingStatus, &types.BridgeStatus{
		BridgeType:     bridgeType,
		BridgeLeafType: t.info.LeafType,
		BlockNumber:    t.info.BlockNumber,
		LogIndex:       t.info.LogIndex,
	}, stepIndex, allSteps, nil
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
