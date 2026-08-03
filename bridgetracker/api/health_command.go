package api

import (
	"net/http"

	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/gin-gonic/gin"
)

// compile-time check: healthCommand fulfils the command interface
var _ command = (*healthCommand)(nil)

// healthCommand builds the health-check response: instance identity and build information.
// It has no parameters and no side effects (it does not touch the supervised registry).
type healthCommand struct {
	instanceID string
	configSHA1 string
}

// Execute implements command
func (cmd *healthCommand) Execute(_ *gin.Context) (int, any, *types.ErrorData) {
	return http.StatusOK, types.HealthResponse{
		Status:     types.HealthStatusOK,
		InstanceID: cmd.instanceID,
		ConfigSHA1: cmd.configSHA1,
		Version:    types.NewVersionInfo(),
	}, nil
}
