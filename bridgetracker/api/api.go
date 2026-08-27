// @title Bridge Tracker API
// @version 1.0
// @description API documentation for the bridge tracker service

// @contact.name API Support
// @contact.url https://polygon.technology/

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @BasePath /tracker/v1

package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	_ "github.com/agglayer/aggkit/bridgetracker/api/docs"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	swaggerfiles "github.com/swaggo/files"
	ginswagger "github.com/swaggo/gin-swagger"
)

const (
	// TrackerV1Prefix is the url prefix for the bridge tracker service
	TrackerV1Prefix = "/tracker/v1"

	txHashParam      = "tx_hash"
	networkIDParam   = "network_id"
	fromAddressParam = "from_address"

	// includeTrackingQueryParam, when set to "true", makes the activity endpoint additionally
	// register every still-unclaimed bridge it finds with the bridge tracker (see
	// activityCommand.Execute)
	includeTrackingQueryParam = "includeTracking"

	// filterBridgesQueryParam selects which bridges the activity endpoint returns: "all"
	// (default), "claimed" or "pending" (see types.ActivityFilter)
	filterBridgesQueryParam = "filterBridges"

	decimalBase   = 10
	uint32BitSize = 32
)

// API is the HTTP service of the bridge tracker: the REST and WebSocket endpoints served on
// the shared HTTP server. Each endpoint's business logic is built once, at construction time,
// into its own command/handler object below — the API struct only wires routes to them, it
// does not hold the supervised registry, logger or instance identity itself.
type API struct {
	getTxStatusCmd *getTxStatusCommand
	healthCmd      *healthCommand
	wsHandler      *wsHandler
	// activityCmd serves GET /activity/from/{from_address}; nil (when NewAPI is given a nil
	// activity) leaves the route unregistered entirely — see RegisterRoutes
	activityCmd *activityCommand
}

// NewAPI returns the tracker HTTP service serving the given supervised registry.
// registerResolveTimeout is how long GetTxStatus waits, the first time a tx is registered, for
// the tracking engine's immediate resolution attempt to produce an update before answering (see
// getTxStatusCommand); <= 0 disables the wait. cors governs which origins may open the
// WebSocket endpoint (see wsHandler). activity may be nil, in which case the
// GET /activity/from/{from_address} endpoint is not registered at all (see RegisterRoutes)
func NewAPI(
	logger aggkitcommon.Logger,
	configSHA1 string,
	supervised domain.SupervisedRegistry,
	activity domain.ActivityQuerier,
	registerResolveTimeout time.Duration,
	cors aggkitcommon.CORSConfig,
) *API {
	api := &API{
		getTxStatusCmd: &getTxStatusCommand{supervised: supervised, resolveTimeout: registerResolveTimeout},
		healthCmd: &healthCommand{
			// instanceID is a UUID generated at startup, exposed by the health endpoint to
			// tell instances (and restarts of the same instance) apart
			instanceID: uuid.NewString(),
			configSHA1: configSHA1,
		},
		wsHandler: newWSHandler(logger, supervised, cors),
	}
	if activity != nil {
		api.activityCmd = &activityCommand{querier: activity}
	}
	return api
}

// RegisterRoutes registers all bridge tracker routes on router. Route-level documentation
// (see swagger.json/swagger.yaml, generated via `make generate-swagger-docs`) lives on the
// actual handler each route dispatches to: getTxStatusCommand.Execute, healthCommand.Execute
// and wsHandler.TxStatusWSHandler
func (a *API) RegisterRoutes(router gin.IRouter) {
	trackerGroup := router.Group(TrackerV1Prefix)
	{
		trackerGroup.GET("/health", func(c *gin.Context) { runCommand(c, a.healthCmd) })
		trackerGroup.GET("/network/:"+networkIDParam+"/tx/:"+txHashParam,
			func(c *gin.Context) { runCommand(c, a.getTxStatusCmd) })
		trackerGroup.GET("/network/:"+networkIDParam+"/tx/:"+txHashParam+"/ws", a.wsHandler.TxStatusWSHandler)
		if a.activityCmd != nil {
			trackerGroup.GET("/activity/from/:"+fromAddressParam,
				func(c *gin.Context) { runCommand(c, a.activityCmd) })
		}

		// Swagger docs endpoint
		trackerGroup.GET("/swagger/*any", ginswagger.WrapHandler(swaggerfiles.Handler))

		// Redirect to the Swagger UI
		trackerGroup.GET("/swagger", func(ctx *gin.Context) {
			ctx.Redirect(http.StatusFound, TrackerV1Prefix+"/swagger/index.html")
		})
	}
}

// command is the interface every bridgetracker API command implements: it runs its business
// logic against the request and returns the HTTP status code and body to write, or the
// ErrorData to write instead
type command interface {
	Execute(c *gin.Context) (code int, obj any, errData *types.ErrorData)
}

// runCommand executes cmd and writes its result (or error) as the gin response
func runCommand(c *gin.Context, cmd command) {
	code, obj, errData := cmd.Execute(c)
	if errData != nil {
		c.JSON(errData.Code, errData)
		return
	}
	c.JSON(code, obj)
}

// parseBridgeRequest builds a BridgeRequest from the network_id and tx_hash path parameters
func parseBridgeRequest(c *gin.Context) (*types.BridgeRequest, error) {
	networkID, err := strconv.ParseUint(c.Param(networkIDParam), decimalBase, uint32BitSize)
	if err != nil {
		return nil, fmt.Errorf("invalid %s parameter", networkIDParam)
	}

	txHashStr := c.Param(txHashParam)
	if !common.IsHexHash(txHashStr) {
		return nil, fmt.Errorf("invalid %s parameter", txHashParam)
	}

	return &types.BridgeRequest{
		NetworkID: uint32(networkID),
		TxHash:    common.HexToHash(txHashStr),
	}, nil
}
