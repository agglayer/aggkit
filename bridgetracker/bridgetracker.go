package bridgetracker

import (
	"github.com/agglayer/aggkit/bridgetracker/api"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	proxytypes "github.com/agglayer/aggkit/proxy/types"
)

// BridgeTracker is the bridge tracker component: it owns the supervised-bridges registry and
// the HTTP service implementing the tracker endpoints (API). The tracking engine is wired
// separately (see NewEngine) over the same registry passed in Config
type BridgeTracker struct {
	logger aggkitcommon.Logger

	// supervised is the list of supervised bridges shared by the API endpoints and, when
	// wired over the same instance, the tracking engine
	supervised SupervisedRegistry

	// api is the HTTP service serving the tracker REST/WS endpoints
	api *api.API
}

// New returns an instance of BridgeTracker
func New(cfg *Config) *BridgeTracker {
	cfg.Logger.Info("starting bridge tracker service")

	supervised := cfg.Registry
	if supervised == nil {
		supervised = NewMemoryRegistry(cfg.MaxTrackedBridges)
	}

	return &BridgeTracker{
		logger:     cfg.Logger,
		supervised: supervised,
		api:        api.NewAPI(cfg.Logger, cfg.ConfigSHA1, supervised, cfg.RegisterResolveTimeout.Duration),
	}
}

// API returns the HTTP service of the tracker; register it on the shared HTTP server to
// expose the tracker REST/WS endpoints
func (b *BridgeTracker) API() *api.API {
	return b.api
}

// Publish stores the resolved bridge facts and expected path of a supervised bridge and
// pushes it to every subscriber (REST polls return it, WebSocket connections receive a
// "status" message); TrackingStatus and step index are derived from allSteps (see
// TrackingData), and the public BridgeStatus is derived from info (see api.BridgeStatus). It
// is a no-op if the bridge is not in the supervised list
func (b *BridgeTracker) Publish(id TrackingID, info *BridgeInfo, allSteps []types.BridgeStepPath) {
	if err := publishStatus(b.supervised, id, info, allSteps); err != nil {
		b.logger.Warnf("failed to publish status of bridge %s: %v", id, err)
	}
}

// PublishError marks a supervised bridge as terminally failed to resolve at all (e.g. the
// tx does not exist on the network or is not a bridge tx): TrackingStatus becomes Error and
// errStep is exposed as TrackingData.Error, both to REST polls and WebSocket connections
// (which then close normally). It is a no-op if the bridge is not in the supervised list
func (b *BridgeTracker) PublishError(id TrackingID, errStep *types.ErrorStep) {
	if err := publishError(b.supervised, id, errStep); err != nil {
		b.logger.Warnf("failed to publish error of bridge %s: %v", id, err)
	}
}

// publishStatus upserts the bridge's resolved facts and expected path through the store's
// fine-grained update methods: the steps first (UpdateTrackingStep, silent), then the tx
// last (UpdateTrackingBridgeTx, which notifies) so subscribers see exactly one consistent,
// fully-merged snapshot instead of one partial notification per step. It is a no-op
// (ErrTrackingNotFound) if the bridge is not in the supervised list
func publishStatus(
	store SupervisedStore, id TrackingID, info *BridgeInfo, allSteps []types.BridgeStepPath,
) error {
	tracking, err := store.Get(id, false)
	if err != nil {
		return err
	}

	for i, step := range allSteps {
		if err := store.UpdateTrackingStep(id, uint(i), step); err != nil {
			return err
		}
	}

	tx := tracking.BridgeTx()
	tx.Info = info
	return store.UpdateTrackingBridgeTx(id, tx)
}

// publishError marks the bridge as terminally failed to resolve at all through the store. It
// is a no-op (ErrTrackingNotFound) if the bridge is not in the supervised list. errStep must
// carry a terminal ErrorType (Permanent or Exhausted): TrackingStatus derives Error from it,
// a Transient one would read as still being resolved (see TrackingData.TrackingStatus)
func publishError(store SupervisedStore, id TrackingID, errStep *types.ErrorStep) error {
	tracking, err := store.Get(id, false)
	if err != nil {
		return err
	}

	tx := tracking.BridgeTx()
	tx.Error = errStep
	return store.UpdateTrackingBridgeTx(id, tx)
}

func Dependencies() []proxytypes.Component {
	return []proxytypes.Component{
		proxytypes.ComponentFinder,
		proxytypes.ComponentL1RPC,
		proxytypes.ComponentLog,
		proxytypes.ComponentREST,
	}
}
