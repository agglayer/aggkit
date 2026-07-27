package bridgetracker

import (
	"github.com/agglayer/aggkit/bridgetracker/api"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	proxytypes "github.com/agglayer/aggkit/proxy/types"
	"github.com/ethereum/go-ethereum/common"
)

// Config holds the configuration of the bridge tracker service
type Config struct {
	Logger aggkitcommon.Logger

	// ConfigSHA1 is the sha1sum (hex) of the configuration the binary was started with,
	// exposed by the health endpoint to check that all instances behind a proxy run the
	// same configuration
	ConfigSHA1 string

	// Registry is the supervised-bridges subsystem to use. Leave nil to get the in-memory
	// adapter (single instance); inject a shared-store implementation so several tracker
	// instances behind a proxy answer for any registered tx
	Registry SupervisedRegistry
}

// BridgeTracker is the bridge tracker component: it owns the supervised-bridges registry,
// exposes the tracking engine entry points (Publish / PublishError) and the HTTP service
// implementing the tracker endpoints (API)
type BridgeTracker struct {
	logger aggkitcommon.Logger

	// supervised is the list of supervised bridges shared by the API endpoints and the
	// tracking engine, which feeds it through Publish / PublishError
	supervised SupervisedRegistry

	// api is the HTTP service serving the tracker REST/WS endpoints
	api *api.API
}

// New returns an instance of BridgeTracker
func New(cfg *Config) *BridgeTracker {
	cfg.Logger.Info("starting bridge tracker service")

	supervised := cfg.Registry
	if supervised == nil {
		supervised = NewMemoryRegistry()
	}

	return &BridgeTracker{
		logger:     cfg.Logger,
		supervised: supervised,
		api:        api.NewAPI(cfg.Logger, cfg.ConfigSHA1, supervised),
	}
}

// API returns the HTTP service of the tracker; register it on the shared HTTP server to
// expose the tracker REST/WS endpoints
func (b *BridgeTracker) API() *api.API {
	return b.api
}

// Publish stores the new tracking status, bridge status, step index and steps of a
// supervised bridge and pushes it to every subscriber (REST polls return it, WebSocket
// connections receive a "status" message). It is a no-op if the bridge is not in the
// supervised list. This is the entry point for the tracking engine
func (b *BridgeTracker) Publish(
	networkID uint32, txHash common.Hash,
	trackingStatus types.TrackingStatus, status *types.BridgeStatus, stepIndex int, allSteps []types.BridgeStepPath,
) {
	b.supervised.SetStatus(networkID, txHash, trackingStatus, status, &stepIndex, allSteps)
}

// PublishError marks a supervised bridge as terminally failed to resolve at all (e.g. the
// tx does not exist on the network or is not a bridge tx): TrackingStatus becomes Error and
// errStep is exposed as TrackingData.Error, both to REST polls and WebSocket connections
// (which then close normally). This is the entry point for the tracking engine
func (b *BridgeTracker) PublishError(networkID uint32, txHash common.Hash, errStep *types.ErrorStep) {
	b.supervised.SetError(networkID, txHash, errStep)
}

func Dependencies() []proxytypes.Component {
	return []proxytypes.Component{
		proxytypes.ComponentFinder,
		proxytypes.ComponentL1RPC,
		proxytypes.ComponentLog,
		proxytypes.ComponentREST,
	}
}
