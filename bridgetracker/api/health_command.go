package api

import (
	"net/http"

	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgetracker/types"
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

// healthCommand builds the health-check response: instance identity and build information.
// It has no side effects (it does not touch the supervised registry).
type healthCommand struct {
	instanceID    string
	configSHA1    string
	pendingLister PendingNetworksLister
}

// Execute implements command
//
// @Summary Health check
// @Description Returns the health status, instance identity and build information of the
// @Description running instance. Useful as liveness/readiness probe and to check which
// @Description build/configuration runs on each instance behind the proxy
// @Tags bridge-tracker
// @Produce json
// @Success 200 {object} types.HealthResponse "Health status and version information"
// @Router /health [get]
func (cmd *healthCommand) Execute(_ *gin.Context) (int, any, *types.ErrorData) {
	resp := types.HealthResponse{
		Status:      types.HealthStatusOK,
		APIRevision: types.CurrentAPIRevision,
		InstanceID:  cmd.instanceID,
		ConfigSHA1:  cmd.configSHA1,
		Version:     types.NewVersionInfo(),
	}
	if cmd.pendingLister != nil {
		resp.PendingNetworks = toPendingNetworks(cmd.pendingLister.PendingNetworks())
	}

	return http.StatusOK, resp, nil
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
