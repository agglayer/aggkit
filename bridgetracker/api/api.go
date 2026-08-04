package api

import (
	"fmt"
	"strconv"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	// TrackerV1Prefix is the url prefix for the bridge tracker service
	TrackerV1Prefix = "/tracker/v1"

	txHashParam    = "tx_hash"
	networkIDParam = "network_id"

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
}

// NewAPI returns the tracker HTTP service serving the given supervised registry.
// registerResolveTimeout is how long GetTxStatus waits, the first time a tx is registered, for
// the tracking engine's immediate resolution attempt to produce an update before answering (see
// getTxStatusCommand); <= 0 disables the wait
func NewAPI(
	logger aggkitcommon.Logger,
	configSHA1 string,
	supervised domain.SupervisedRegistry,
	registerResolveTimeout time.Duration,
) *API {
	return &API{
		getTxStatusCmd: &getTxStatusCommand{supervised: supervised, resolveTimeout: registerResolveTimeout},
		healthCmd: &healthCommand{
			// instanceID is a UUID generated at startup, exposed by the health endpoint to
			// tell instances (and restarts of the same instance) apart
			instanceID: uuid.NewString(),
			configSHA1: configSHA1,
		},
		wsHandler: &wsHandler{logger: logger, supervised: supervised},
	}
}

// RegisterRoutes registers all bridge tracker routes on router
//
// GetTxStatusHandler returns the status of the bridge originated by the given transaction
// hash, registering the bridge in the supervised list if it was not already being tracked.
//
// @Summary Get bridge status by transaction hash
// @Description Returns the current step of the bridge and the full path it is expected to
// @Description follow. Calling this endpoint adds the bridge to the list of supervised
// @Description bridges. The response is always a TrackingData: its bridge_status field is
// @Description null until the tracker resolves the bridge, so the client keeps polling (or
// @Description subscribes over the WebSocket) until it is populated
// @Tags bridge-tracker
// @Produce json
// @Param network_id path uint32 true "Network where the bridge transaction was sent (0 -> Mainnet)"
// @Param tx_hash path string true "Hash of the transaction that created the bridge (bridgeAsset or bridgeMessage)"
// @Success 200 {object} TrackingData "Bridge registered; bridge_status/error fill in once resolved"
// @Failure 400 {object} types.ErrorData "Invalid transaction hash or network id"
// @Router /network/{network_id}/tx/{tx_hash} [get]
//
// HealthHandler is the health-check endpoint: no parameters and no side effects (it does
// not register anything in the supervised list).
//
// @Summary Health check
// @Description Returns the health status, instance identity and build information of the
// @Description running instance. Useful as liveness/readiness probe and to check which
// @Description build/configuration runs on each instance behind the proxy
// @Tags bridge-tracker
// @Produce json
// @Success 200 {object} types.HealthResponse "Health status and version information"
// @Router /health [get]
func (a *API) RegisterRoutes(router gin.IRouter) {
	trackerGroup := router.Group(TrackerV1Prefix)
	{
		trackerGroup.GET("/health", func(c *gin.Context) { runCommand(c, a.healthCmd) })
		trackerGroup.GET("/network/:"+networkIDParam+"/tx/:"+txHashParam,
			func(c *gin.Context) { runCommand(c, a.getTxStatusCmd) })
		trackerGroup.GET("/network/:"+networkIDParam+"/tx/:"+txHashParam+"/ws", a.wsHandler.TxStatusWSHandler)
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
