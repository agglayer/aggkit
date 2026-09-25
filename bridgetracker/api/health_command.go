package api

import (
	"net/http"
	"time"

	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/gin-gonic/gin"
)

// compile-time check: healthCommand fulfils the command interface
var _ command = (*healthCommand)(nil)

// PendingNetworksLister is the slice of bridgeservicefinder.Finder the health endpoint needs: the
// networks that were discovered after startup but not activated. It is optional — when nil the
// health response simply omits pending_networks (e.g. a tracker embedded without a finder).
type PendingNetworksLister interface {
	PendingNetworks() []bridgeservicefinder.PendingNetwork
}

// healthCommand builds the health-check response: instance identity, build information, and a
// read-only snapshot of the supervised registry/activity subsystem's size (cache footprint,
// alive counts). It never writes to either.
type healthCommand struct {
	instanceID string
	// startDate is when this instance started, captured once at construction time alongside
	// instanceID and served verbatim, so every response of one execution reports the same value
	startDate     time.Time
	configSHA1    string
	pendingLister PendingNetworksLister

	// supervised is read for AliveTrackers/Cache (see NewAPI); nil only in tests that don't
	// exercise those fields
	supervised domain.SupervisedStore
	// activity is read for AliveActivities; nil when the activity endpoint is not configured
	// (see NewAPI's doc), in which case AliveActivities is omitted entirely
	activity domain.ActivityRegistry
	logger   aggkitcommon.Logger
}

// Execute implements command
//
// @Summary Health check
// @Description Returns the health status, instance identity, build information and a snapshot
// @Description of the supervised registry/activity subsystem's size (cache footprint, alive
// @Description counts) of the running instance. Useful as liveness/readiness probe and to check
// @Description which build/configuration runs on each instance behind the proxy
// @Tags bridge-tracker
// @Produce json
// @Success 200 {object} types.HealthResponse "Health status and version information"
// @Router /health [get]
func (cmd *healthCommand) Execute(_ *gin.Context) (int, any, *types.ErrorData) {
	resp := types.HealthResponse{
		Status:      types.HealthStatusOK,
		APIRevision: types.CurrentAPIRevision,
		InstanceID:  cmd.instanceID,
		StartDate:   cmd.startDate,
		ConfigSHA1:  cmd.configSHA1,
		Version:     types.NewVersionInfo(),
		Cache:       cmd.cacheInfo(),
	}
	if cmd.pendingLister != nil {
		resp.PendingNetworks = toPendingNetworks(cmd.pendingLister.PendingNetworks())
	}
	if cmd.supervised != nil {
		if active, err := cmd.supervised.GetTrackerActives(nil); err != nil {
			cmd.warnf("bridgetracker: health check counting active trackers: %v", err)
		} else {
			resp.AliveTrackers = len(active)
		}
	}
	if cmd.activity != nil {
		if addrs, err := cmd.activity.GetActiveAddresses(); err != nil {
			cmd.warnf("bridgetracker: health check counting active activity addresses: %v", err)
		} else {
			numActive := len(addrs)
			resp.AliveActivities = &numActive
		}
	}

	return http.StatusOK, resp, nil
}

// cacheInfo reports the supervised registry's persistence backend: CacheKindDisk with its
// current size when supervised implements domain.CacheStatsProvider (the SQLite-backed
// adapter), CacheKindMemory otherwise — including when supervised is nil, which should not
// happen outside tests (see NewAPI, always given at least an in-memory registry)
func (cmd *healthCommand) cacheInfo() types.CacheInfo {
	provider, ok := cmd.supervised.(domain.CacheStatsProvider)
	if !ok {
		return types.CacheInfo{Kind: types.CacheKindMemory}
	}
	stats, err := provider.CacheStats()
	if err != nil {
		cmd.warnf("bridgetracker: health check reading cache size: %v", err)
		return types.CacheInfo{Kind: types.CacheKindDisk}
	}
	return types.CacheInfo{Kind: types.CacheKindDisk, SizeBytes: stats.SizeBytes}
}

// warnf logs through cmd.logger if one was wired (see NewAPI); a no-op otherwise, so tests that
// exercise an error path without setting logger don't need a fake one just to avoid a nil panic
func (cmd *healthCommand) warnf(format string, args ...any) {
	if cmd.logger != nil {
		cmd.logger.Warnf(format, args...)
	}
}

// toPendingNetworks maps the finder's pending records onto the health response's own type,
// keeping bridgetracker/types free of any dependency on bridgeservicefinder. It returns nil for an
// empty input so the pending_networks key is omitted.
func toPendingNetworks(src []bridgeservicefinder.PendingNetwork) []types.PendingNetwork {
	if len(src) == 0 {
		return nil
	}
	out := make([]types.PendingNetwork, len(src))
	for i, p := range src {
		out[i] = types.PendingNetwork{
			NetworkID:     p.NetworkID,
			RollupAddress: p.RollupAddress.Hex(),
			BlockNumber:   p.BlockNumber,
			FirstSeen:     p.FirstSeen,
			Reason:        p.Reason,
		}
	}
	return out
}
