// @title Bridge Service API
// @version 1.0
// @description API documentation for the bridge service

// @contact.name API Support
// @contact.url https://polygon.technology/

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @BasePath /bridge/v1

package bridgeservice

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/agglayer/aggkit"
	_ "github.com/agglayer/aggkit/bridgeservice/docs"
	"github.com/agglayer/aggkit/bridgeservice/metrics"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/log"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginswagger "github.com/swaggo/gin-swagger"
)

const (
	// BridgeV1Prefix is the url prefix for the bridge service
	BridgeV1Prefix = "/bridge/v1"

	networkIDParam       = "network_id"
	networkIDsParam      = "network_ids"
	pageNumberParam      = "page_number"
	pageSizeParam        = "page_size"
	depositCountParam    = "deposit_count"
	fromAddressParam     = "from_address"
	originTokenAddrParam = "origin_token_address"
	leafIndexParam       = "leaf_index"
	includeAllFields     = "include_all_fields"
	globalIndexParam     = "global_index"

	// mainnetNetworkID is the network ID of L1 network
	mainnetNetworkID    = 0
	binarySearchDivider = 2

	errNetworkID         = "unsupported network id: %v"
	errSetupRequest      = "failed to setup request: %v"
	errDepositCountParam = "invalid deposit count parameter: %v"

	// etrogVersionID is the version ID of AgglayerManager after Etrog upgrade
	etrogVersionID = 2
)

var (
	ErrNotOnL1Info = errors.New("this bridge has not been included on the L1 Info Tree yet")
)

// AgglayerManagerUpgradeQuerier abstracts AgglayerManager upgrade block
// retrieval based on the rollup initializer version
type AgglayerManagerUpgradeQuerier interface {
	GetUpgradeBlock(ctx context.Context, versionID uint8) uint64
}

type Config struct {
	Logger       *log.Logger
	Address      string
	WriteTimeout time.Duration
	ReadTimeout  time.Duration
	NetworkID    uint32
}

// BridgeService contains implementations for the bridge service endpoints
type BridgeService struct {
	logger                      *log.Logger
	address                     string
	readTimeout                 time.Duration
	writeTimeout                time.Duration
	networkID                   uint32
	agglayerManagerUpgradeQuery AgglayerManagerUpgradeQuerier
	l1InfoTree                  L1InfoTreeSyncer
	injectedGERs                L2GERSyncer
	bridgeL1                    Bridger
	bridgeL2                    Bridger

	router *gin.Engine
}

// New returns instance of BridgeService
func New(
	cfg *Config,
	upgradeQuerier AgglayerManagerUpgradeQuerier,
	l1InfoTree L1InfoTreeSyncer,
	injectedGERs L2GERSyncer,
	bridgeL1 Bridger,
	bridgeL2 Bridger,
) *BridgeService {
	cfg.Logger.Infof("starting bridge service (network id=%d, address=%s)", cfg.NetworkID, cfg.Address)

	// The GIN_MODE environment variable controls the mode of the Gin framework.
	// Valid values are "debug", "release", and "test". If an invalid value is provided,
	// the mode defaults to "release" for safety and performance.
	ginMode := os.Getenv("GIN_MODE")
	switch ginMode {
	case gin.DebugMode, gin.ReleaseMode, gin.TestMode:
		gin.SetMode(ginMode)
	default:
		cfg.Logger.Infof("invalid or missing GIN_MODE value ('%s') provided, defaulting to '%s' mode",
			ginMode, gin.ReleaseMode)
		gin.SetMode(gin.ReleaseMode) // fallback to release mode
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(LoggerHandler(cfg.Logger))

	b := &BridgeService{
		logger:                      cfg.Logger,
		address:                     cfg.Address,
		readTimeout:                 cfg.ReadTimeout,
		writeTimeout:                cfg.WriteTimeout,
		networkID:                   cfg.NetworkID,
		agglayerManagerUpgradeQuery: upgradeQuerier,
		l1InfoTree:                  l1InfoTree,
		injectedGERs:                injectedGERs,
		bridgeL1:                    bridgeL1,
		bridgeL2:                    bridgeL2,
		router:                      router,
	}

	b.registerRoutes()
	cfg.Logger.Info("bridge service initialized successfully")

	return b
}

// LoggerHandler returns a Gin middleware that logs HTTP requests using logger at DEBUG level.
func LoggerHandler(logger aggkitcommon.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		if latency > time.Minute {
			latency = latency.Truncate(time.Second)
		}

		clientIP := c.ClientIP()
		method := c.Request.Method
		statusCode := c.Writer.Status()
		errorMessage := c.Errors.ByType(gin.ErrorTypePrivate).String()

		if raw != "" {
			path += "?" + raw
		}

		logger.Debugf(
			"[GIN] %v | %3d | %13v | %15s | %-7s %#v\n%s",
			start.Format("2006/01/02 - 15:04:05"),
			statusCode,
			latency,
			clientIP,
			method,
			path,
			errorMessage,
		)
	}
}

// registerRoutes registers the routes for the bridge service
func (b *BridgeService) registerRoutes() {
	// Health check endpoint at root path
	b.router.GET("/", b.HealthCheckHandler)

	bridgeGroup := b.router.Group(BridgeV1Prefix)
	{
		bridgeGroup.GET("/bridges", b.GetBridgesHandler)
		bridgeGroup.GET("/claims", b.GetClaimsHandler)
		bridgeGroup.GET("/token-mappings", b.GetTokenMappingsHandler)
		bridgeGroup.GET("/legacy-token-migrations", b.GetLegacyTokenMigrationsHandler)
		bridgeGroup.GET("/l1-info-tree-index", b.L1InfoTreeIndexForBridgeHandler)
		bridgeGroup.GET("/injected-l1-info-leaf", b.InjectedL1InfoLeafHandler)
		bridgeGroup.GET("/claim-proof", b.ClaimProofHandler)
		bridgeGroup.GET("/last-reorg-event", b.GetLastReorgEventHandler)
		bridgeGroup.GET("/sync-status", b.GetSyncStatusHandler)
		bridgeGroup.GET("/remove-ger-events", b.GetRemoveGEREventsHandler)

		// Swagger docs endpoint
		bridgeGroup.GET("/swagger/*any", ginswagger.WrapHandler(swaggerfiles.Handler))

		// Redirect to the Swagger UI
		bridgeGroup.GET("/swagger", func(ctx *gin.Context) {
			ctx.Redirect(http.StatusFound, BridgeV1Prefix+"/swagger/index.html")
		})
	}
}

// Start starts the HTTP bridge service
func (b *BridgeService) Start(ctx context.Context) {
	// Register metrics
	metrics.Register()

	srv := &http.Server{
		Addr:         b.address,
		Handler:      b.router,
		ReadTimeout:  b.readTimeout,
		WriteTimeout: b.writeTimeout,
	}

	b.logger.Infof("Bridge service listening on %s...", b.address)
	err := srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		b.logger.Panicf("failed to start bridge service: %v", err)
	}

	<-ctx.Done()

	b.logger.Info("Shutting down bridge service...")

	var parentCtx context.Context
	if ctx.Err() == nil {
		parentCtx = ctx
	} else {
		parentCtx = context.Background()
	}

	ctx, cancel := context.WithTimeout(parentCtx, b.readTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		b.logger.Panicf("Server shutdown error: %v", err)
	}

	b.logger.Info("Bridge service exited gracefully")
}

// HealthCheckHandler returns the health status and version information of the bridge service.
//
// @Summary Get health status
// @Description Returns the health status and version information of the bridge service
// @Tags health
// @Produce json
// @Success 200 {object} types.HealthCheckResponse "Health status and version information"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Router / [get]
func (b *BridgeService) HealthCheckHandler(c *gin.Context) {
	time := time.Now()
	version := aggkit.GetVersion()
	c.JSON(http.StatusOK,
		types.HealthCheckResponse{
			Status:  "ok",
			Time:    time.UTC(),
			Version: version.Version,
		})

	reportMetrics(metrics.GetHealthCheckReq, http.StatusOK, time)
}

// GetBridgesHandler retrieves paginated bridge data for the specified network.
//
// @Summary Get bridges
// @Description Returns a paginated list of bridge events for the specified network.
// @Tags bridges
// @Param network_id query uint32 true "Origin network ID"
// @Param page_number query uint32 false "Page number (default 1)"
// @Param page_size query uint32 false "Page size (default 100)"
// @Param deposit_count query uint64 false "Filter by deposit count"
// @Param from_address query string false "Filter by from address"
// @Param network_ids query []uint32 false "Filter by one or more destination network IDs (maximum 5 allowed)"
// @Produce json
// @Success 200 {object} types.BridgesResult
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Router /bridges [get]
func (b *BridgeService) GetBridgesHandler(c *gin.Context) {
	b.logger.Debugf("GetBridges request received (network id=%s, page number=%s, page size=%s)",
		c.Query(networkIDParam), c.Query(pageNumberParam), c.Query(pageSizeParam))

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(metrics.GetBridgesReq, statusCode, startTime)
	}()

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errNetworkID, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	depositCount, err := parseUintQuery(c, depositCountParam, false, uint64(math.MaxUint64))
	if err != nil {
		b.logger.Warnf(errDepositCountParam, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	var depositCountPtr *uint64
	if depositCount != math.MaxUint64 {
		depositCountPtr = &depositCount
	}

	fromAddress := c.Query(fromAddressParam)

	networkIDs, err := parseNetworkIDSliceParam(c, networkIDsParam)
	if err != nil {
		b.logger.Warnf("invalid network IDs parameter: %v", err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf("invalid %s parameter: %s", networkIDsParam, err)})
		return
	}

	ctx, cancel, pageNumber, pageSize, err := b.setupRequest(c)
	if err != nil {
		b.logger.Warnf(errSetupRequest, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}
	defer cancel()

	b.logger.Debugf(
		"fetching bridges (network id=%d, page=%d, size=%d, deposit_count=%v, network_ids=%v, from_address=%s)",
		networkID, pageNumber, pageSize, depositCountPtr, networkIDs, fromAddress)

	var (
		bridges []*bridgesync.Bridge
		count   int
	)

	//nolint:dupl
	switch networkID {
	case mainnetNetworkID:
		if b.bridgeL1 == nil {
			statusCode = http.StatusServiceUnavailable
			c.JSON(statusCode, gin.H{"error": "L1 bridge syncer is not available"})
			return
		}

		bridges, count, err = b.bridgeL1.GetBridgesPaged(ctx, pageNumber, pageSize, depositCountPtr, networkIDs, fromAddress)
		if err != nil {
			b.logger.Errorf("failed to get bridges for L1 network: %v", err)
			statusCode = http.StatusInternalServerError
			c.JSON(statusCode, gin.H{"error": fmt.Sprintf("failed to get bridges for the L1 network, error: %s", err)})
			return
		}
	case b.networkID:
		if b.bridgeL2 == nil {
			statusCode = http.StatusServiceUnavailable
			c.JSON(statusCode, gin.H{"error": "L2 bridge syncer is not available"})
			return
		}

		bridges, count, err = b.bridgeL2.GetBridgesPaged(ctx, pageNumber, pageSize, depositCountPtr, networkIDs, fromAddress)
		if err != nil {
			b.logger.Errorf("failed to get bridges for L2 network (ID=%d): %v", networkID, err)
			statusCode = http.StatusInternalServerError
			c.JSON(statusCode,
				gin.H{"error": fmt.Sprintf("failed to get bridges for the L2 network (ID=%d), error: %s", networkID, err)})
			return
		}
	default:
		b.logger.Warnf(errNetworkID, networkID)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf(errNetworkID, networkID)})
		return
	}

	etrogUpgradeL1Block := b.agglayerManagerUpgradeQuery.GetUpgradeBlock(ctx, etrogVersionID)

	b.logger.Debugf("successfully retrieved %d bridges for network %d", count, networkID)
	bridgeResponses := make([]*types.BridgeResponse, 0, len(bridges))
	for _, bridge := range bridges {
		bridgeResponses = append(bridgeResponses, NewBridgeResponse(bridge, networkID, etrogUpgradeL1Block))
	}

	c.JSON(statusCode,
		types.BridgesResult{
			Bridges: bridgeResponses,
			Count:   count,
		})
}

// GetClaimsHandler retrieves paginated claims for a given network.
//
// @Summary Get claims
// @Description Returns a paginated list of claims for the specified network.
// @Tags claims
// @Param network_id query uint32 true "Origin network ID"
// @Param page_number query uint32 false "Page number (default 1)"
// @Param page_size query uint32 false "Page size (default 100)"
// @Param network_ids query []uint32 false "Filter by one or more source network IDs (maximum 5 allowed)"
// @Param from_address query string false "Filter by from address"
// @Param include_all_fields query bool false "Whether to include full response fields (default false)"
// @Param global_index query uint32 false "Filter by global index"
// @Produce json
// @Success 200 {object} types.ClaimsResult
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Router /claims [get]
func (b *BridgeService) GetClaimsHandler(c *gin.Context) {
	b.logger.Debugf("GetClaims request received (network id=%s, page number=%s, page size=%s, "+
		"include_all_fields=%s, global_index=%s)",
		c.Query(networkIDParam), c.Query(pageNumberParam), c.Query(pageSizeParam),
		c.Query(includeAllFields), c.Query(globalIndexParam))

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(metrics.GetClaimsReq, statusCode, startTime)
	}()

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errNetworkID, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	networkIDs, err := parseNetworkIDSliceParam(c, networkIDsParam)
	if err != nil {
		b.logger.Warnf("invalid network IDs parameter: %v", err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf("invalid %s parameter: %s", networkIDsParam, err)})
		return
	}

	fromAddress := c.Query(fromAddressParam)

	// Parse include_all_fields parameter (default to false)
	includeAllFieldsFlag := false
	if includeAllFieldsStr := c.Query(includeAllFields); includeAllFieldsStr != "" {
		includeAllFieldsFlag, err = strconv.ParseBool(includeAllFieldsStr)
		if err != nil {
			b.logger.Warnf("invalid include_all_fields parameter: %v", err)
			statusCode = http.StatusBadRequest
			c.JSON(statusCode, gin.H{"error": "invalid include_all_fields parameter"})
			return
		}
	}

	globalIndexRaw := c.Query(globalIndexParam)
	var (
		globalIndex *big.Int
		ok          bool
	)
	if globalIndexRaw != "" {
		globalIndex, ok = new(big.Int).SetString(globalIndexRaw, 0)
		if !ok {
			b.logger.Warnf("invalid %s parameter", globalIndexParam)
			statusCode = http.StatusBadRequest
			c.JSON(statusCode,
				gin.H{"error": fmt.Sprintf("invalid %s parameter, it should be a numeric", globalIndexParam)})
			return
		}
	}

	ctx, cancel, pageNumber, pageSize, err := b.setupRequest(c)
	if err != nil {
		b.logger.Warnf(errSetupRequest, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}
	defer cancel()

	b.logger.Debugf(
		"fetching claims (network id=%d, page=%d, size=%d, "+
			"network_ids=%v, from_address=%s, include_all_fields=%t, global_index=%d)",
		networkID, pageNumber, pageSize, networkIDs, fromAddress, includeAllFieldsFlag, globalIndex)

	var (
		claims []*bridgesync.Claim
		count  int
	)

	//nolint:dupl
	switch networkID {
	case mainnetNetworkID:
		if b.bridgeL1 == nil {
			statusCode = http.StatusServiceUnavailable
			c.JSON(statusCode,
				gin.H{"error": "L1 bridge syncer is not available"})
			return
		}

		claims, count, err = b.bridgeL1.GetClaimsPaged(ctx, pageNumber, pageSize, networkIDs, fromAddress, globalIndex)
		if err != nil {
			b.logger.Warnf("failed to get claims for L1 network: %v", err)
			statusCode = http.StatusInternalServerError
			c.JSON(statusCode,
				gin.H{"error": fmt.Sprintf("failed to get claims for the L1 network, error: %s", err)})
			return
		}
	case b.networkID:
		if b.bridgeL2 == nil {
			statusCode = http.StatusServiceUnavailable
			c.JSON(statusCode,
				gin.H{"error": "L2 bridge syncer is not available"})
			return
		}

		claims, count, err = b.bridgeL2.GetClaimsPaged(ctx, pageNumber, pageSize, networkIDs, fromAddress, globalIndex)
		if err != nil {
			b.logger.Warnf("failed to get claims for L2 network (ID=%d): %v", networkID, err)
			statusCode = http.StatusInternalServerError
			c.JSON(statusCode,
				gin.H{"error": fmt.Sprintf("failed to get claims for the L2 network (ID=%d), error: %s", networkID, err)})
			return
		}
	default:
		b.logger.Warnf(errNetworkID, networkID)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf(errNetworkID, networkID)})
		return
	}

	// Use conditional function to create claim responses
	claimResponses := make([]*types.ClaimResponse, len(claims))
	for i, claim := range claims {
		claimResponses[i] = NewClaimResponse(claim, includeAllFieldsFlag)
	}

	c.JSON(statusCode,
		types.ClaimsResult{
			Claims: claimResponses,
			Count:  count,
		})
}

// @Summary Get token mappings
// @Description Returns token mappings for the given network, paginated
// @Tags token-mappings
// @Param network_id query int true "Network ID"
// @Param page_number query int false "Page number"
// @Param page_size query int false "Page size"
// @Param origin_token_address query string false "Filter by origin token address"
// @Produce json
// @Success 200 {object} types.TokenMappingsResult
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Router /token-mappings [get]
func (b *BridgeService) GetTokenMappingsHandler(c *gin.Context) {
	b.logger.Debugf(
		"GetTokenMappings request received (network id=%s, page number=%s, page size=%s, origin token address=%s)",
		c.Query(networkIDParam), c.Query(pageNumberParam), c.Query(pageSizeParam), c.Query(originTokenAddrParam))

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(metrics.GetTokenMappingsReq, statusCode, startTime)
	}()

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errNetworkID, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	originTokenAddress := c.Query(originTokenAddrParam)

	ctx, cancel, pageNumber, pageSize, err := b.setupRequest(c)
	if err != nil {
		b.logger.Warnf(errSetupRequest, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}
	defer cancel()

	var (
		tokenMappings      []*bridgesync.TokenMapping
		tokenMappingsCount int
	)

	switch networkID {
	case mainnetNetworkID:
		if b.bridgeL1 == nil {
			statusCode = http.StatusServiceUnavailable
			c.JSON(statusCode, gin.H{"error": "L1 bridge syncer is not available"})
			return
		}
		tokenMappings, tokenMappingsCount, err = b.bridgeL1.GetTokenMappings(ctx, pageNumber, pageSize, originTokenAddress)
	case b.networkID:
		if b.bridgeL2 == nil {
			statusCode = http.StatusServiceUnavailable
			c.JSON(statusCode, gin.H{"error": "L2 bridge syncer is not available"})
			return
		}
		tokenMappings, tokenMappingsCount, err = b.bridgeL2.GetTokenMappings(ctx, pageNumber, pageSize, originTokenAddress)
	default:
		b.logger.Warnf(errNetworkID, networkID)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf(errNetworkID, networkID)})
		return
	}

	if err != nil {
		b.logger.Errorf("failed to fetch token mappings: %v", err)
		statusCode = http.StatusInternalServerError
		c.JSON(statusCode,
			gin.H{"error": fmt.Sprintf("failed to fetch token mappings: %s", err.Error())})
		return
	}

	tokenMappingResponses := aggkitcommon.MapSlice(tokenMappings, NewTokenMappingResponse)

	c.JSON(statusCode,
		types.TokenMappingsResult{
			TokenMappings: tokenMappingResponses,
			Count:         tokenMappingsCount,
		})
}

// @Summary Get legacy token migrations
// @Description Returns legacy token migrations for the given network, paginated
// @Tags legacy-token-migrations
// @Param network_id query int true "Network ID"
// @Param page_number query int false "Page number"
// @Param page_size query int false "Page size"
// @Produce json
// @Success 200 {object} types.LegacyTokenMigrationsResult
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Router /legacy-token-migrations [get]
func (b *BridgeService) GetLegacyTokenMigrationsHandler(c *gin.Context) {
	b.logger.Debugf("GetLegacyTokenMigrations request received (network id=%s, page number=%s, page size=%s)",
		c.Query(networkIDParam), c.Query(pageNumberParam), c.Query(pageSizeParam))

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(metrics.GetLegacyTokenMigrationsReq, statusCode, startTime)
	}()

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errNetworkID, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel, pageNumber, pageSize, err := b.setupRequest(c)
	if err != nil {
		b.logger.Warnf(errSetupRequest, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}
	defer cancel()

	var (
		tokenMigrations      []*bridgesync.LegacyTokenMigration
		tokenMigrationsCount int
	)

	switch networkID {
	case mainnetNetworkID:
		if b.bridgeL1 == nil {
			statusCode = http.StatusServiceUnavailable
			c.JSON(statusCode, gin.H{"error": "L1 bridge syncer is not available"})
			return
		}
		tokenMigrations, tokenMigrationsCount, err = b.bridgeL1.GetLegacyTokenMigrations(ctx, pageNumber, pageSize)
	case b.networkID:
		if b.bridgeL2 == nil {
			statusCode = http.StatusServiceUnavailable
			c.JSON(statusCode, gin.H{"error": "L2 bridge syncer is not available"})
			return
		}
		tokenMigrations, tokenMigrationsCount, err = b.bridgeL2.GetLegacyTokenMigrations(ctx, pageNumber, pageSize)
	default:
		b.logger.Warnf(errNetworkID, networkID)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf(errNetworkID, networkID)})
		return
	}

	if err != nil {
		b.logger.Errorf("failed to fetch legacy token migrations: %v", err)
		statusCode = http.StatusInternalServerError
		c.JSON(statusCode,
			gin.H{"error": fmt.Sprintf("failed to fetch legacy token migrations: %s", err.Error())})
		return
	}

	tokenMigrationResponses := aggkitcommon.MapSlice(tokenMigrations, NewTokenMigrationResponse)

	c.JSON(statusCode,
		types.LegacyTokenMigrationsResult{
			TokenMigrations: tokenMigrationResponses,
			Count:           tokenMigrationsCount,
		})
}

// @Summary Get L1 Info Tree index for a bridge
// @Description Returns the first L1 Info Tree index after a given deposit count for the specified network
// @Tags l1-info-tree-leaf
// @Param network_id query int true "Network ID"
// @Param deposit_count query int true "Deposit count"
// @Produce json
// @Success 200 {object} uint32
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Router /l1-info-tree-index [get]
func (b *BridgeService) L1InfoTreeIndexForBridgeHandler(c *gin.Context) {
	b.logger.Debugf("L1InfoTreeIndexForBridge request received (network id=%s, deposit count=%s)",
		c.Query(networkIDParam), c.Query(depositCountParam))

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(metrics.GetL1InfoTreeIndexReq, statusCode, startTime)
	}()

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errNetworkID, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	depositCount, err := parseUintQuery(c, depositCountParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errDepositCountParam, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	var l1InfoTreeIndex uint32
	switch networkID {
	case mainnetNetworkID:
		l1InfoTreeIndex, err = b.getFirstL1InfoTreeIndexForL1Bridge(ctx, depositCount)
	case b.networkID:
		l1InfoTreeIndex, err = b.getFirstL1InfoTreeIndexForL2Bridge(ctx, depositCount)
	default:
		b.logger.Warnf(errNetworkID, networkID)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf(errNetworkID, networkID)})
		return
	}

	if err != nil {
		b.logger.Debugf(
			"failed to get L1 info tree index (network id=%d, deposit count=%d): %v",
			networkID,
			depositCount,
			err,
		)
		statusCode = http.StatusInternalServerError
		c.JSON(statusCode,
			gin.H{"error": fmt.Sprintf("failed to get l1 info tree index for network id %d and deposit count %d, error: %s",
				networkID, depositCount, err)})
		return
	}

	c.JSON(statusCode, l1InfoTreeIndex)
}

// @Summary Get injected L1 info tree leaf after a given L1 info tree index
// @Description Returns the L1 info tree leaf either at the given index (for L1)
// @Description or the first injected global exit root after the given index (for L2).
// @Tags l1-info-tree-leaf
// @Param network_id query int true "Network ID"
// @Param leaf_index query int true "L1 Info Tree Index"
// @Produce json
// @Success 200 {object} types.L1InfoTreeLeafResponse
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Router /injected-l1-info-leaf [get]
func (b *BridgeService) InjectedL1InfoLeafHandler(c *gin.Context) {
	b.logger.Debugf("InjectedInfoAfterIndex request received (network id=%s, leaf index=%s)",
		c.Query(networkIDParam), c.Query(leafIndexParam))

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(metrics.GetInjectedInfoAfterIndexReq, statusCode, startTime)
	}()

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errNetworkID, err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	l1InfoTreeIndex, err := parseUintQuery(c, leafIndexParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf("invalid L1 info tree index parameter: %v", err)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	var l1InfoLeaf *l1infotreesync.L1InfoTreeLeaf

	switch networkID {
	case mainnetNetworkID:
		l1InfoLeaf, err = b.l1InfoTree.GetInfoByIndex(ctx, l1InfoTreeIndex)
	case b.networkID:
		e, err := b.injectedGERs.GetFirstGERAfterL1InfoTreeIndex(ctx, l1InfoTreeIndex)
		if err != nil {
			b.logger.Errorf("failed to get injected global exit root for leaf index=%d: %v", l1InfoTreeIndex, err)
			statusCode = http.StatusInternalServerError
			c.JSON(statusCode,
				gin.H{"error": fmt.Sprintf("failed to get injected global exit root for leaf index=%d, error: %s",
					l1InfoTreeIndex, err)})
			return
		}

		l1InfoLeaf, err = b.l1InfoTree.GetInfoByIndex(ctx, e.L1InfoTreeIndex)
		if err != nil {
			b.logger.Errorf("failed to get L1 info tree leaf (leaf index=%d): %v", e.L1InfoTreeIndex, err)
			statusCode = http.StatusInternalServerError
			c.JSON(statusCode,
				gin.H{"error": fmt.Sprintf("failed to get L1 info tree leaf (leaf index=%d), error: %s",
					e.L1InfoTreeIndex, err)})
			return
		}
	default:
		b.logger.Warnf(errNetworkID, networkID)
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf(errNetworkID, networkID)})
		return
	}

	if err != nil {
		b.logger.Debugf("failed to get L1 info tree leaf (network id=%d, leaf index=%d): %v", networkID, l1InfoTreeIndex, err)
		statusCode = http.StatusInternalServerError
		c.JSON(statusCode,
			gin.H{"error": fmt.Sprintf("failed to get L1 info tree leaf (network id=%d, leaf index=%d), error: %s",
				networkID, l1InfoTreeIndex, err)})
		return
	}

	c.JSON(statusCode, NewL1InfoTreeLeafResponse(l1InfoLeaf))
}

// ClaimProofHandler returns the Merkle proofs required to verify a claim on the target network.
//
// @Summary Get claim proof
// @Description Returns the Merkle proofs (local and rollup exit root) and
// @Description the corresponding L1 info tree leaf needed to verify a claim.
// @Tags claims
// @Param network_id query uint32 true "Origin network ID"
// @Param leaf_index query uint32 true "Index in the L1 info tree"
// @Param deposit_count query uint32 true "Number of deposits in the bridge"
// @Produce json
// @Success 200 {object} types.ClaimProof "Merkle proofs and L1 info tree leaf"
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Router /claim-proof [get]
func (b *BridgeService) ClaimProofHandler(c *gin.Context) {
	b.logger.Debugf("ClaimProof request received (network id=%s, l1 info tree index=%s, deposit count=%s)",
		c.Query(networkIDParam), c.Query(leafIndexParam), c.Query(depositCountParam))

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(metrics.GetClaimProofReq, statusCode, startTime)
	}()

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errNetworkID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	l1InfoTreeIndex, err := parseUintQuery(c, leafIndexParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf("invalid L1 info tree index parameter: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	depositCount, err := parseUintQuery(c, depositCountParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errDepositCountParam, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	info, err := b.l1InfoTree.GetInfoByIndex(ctx, l1InfoTreeIndex)
	if err != nil {
		b.logger.Errorf("failed to get L1 info tree leaf for index %d: %v", l1InfoTreeIndex, err)
		c.JSON(http.StatusInternalServerError,
			gin.H{"error": fmt.Sprintf("failed to get l1 info tree leaf for index %d: %s", l1InfoTreeIndex, err)})
		return
	}

	var proofLocalExitRoot tree.Proof
	switch networkID {
	case mainnetNetworkID:
		if b.bridgeL1 == nil {
			c.JSON(http.StatusServiceUnavailable,
				gin.H{"error": "L1 bridge syncer is not available"})
			return
		}
		proofLocalExitRoot, err = b.bridgeL1.GetProof(ctx, depositCount, info.MainnetExitRoot)
		if err != nil {
			b.logger.Errorf("failed to get local exit proof for L1: %v", err)
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get local exit proof, error: %s", err)})
			return
		}

	case b.networkID:
		localExitRoot, err := b.l1InfoTree.GetLocalExitRoot(ctx, networkID, info.RollupExitRoot)
		if err != nil {
			b.logger.Errorf("failed to get local exit root from rollup exit tree: %v", err)
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get local exit root from rollup exit tree, error: %s", err)})
			return
		}
		if b.bridgeL2 == nil {
			c.JSON(http.StatusServiceUnavailable,
				gin.H{"error": "L2 bridge syncer is not available"})
			return
		}
		proofLocalExitRoot, err = b.bridgeL2.GetProof(ctx, depositCount, localExitRoot)
		if err != nil {
			b.logger.Errorf("failed to get local exit proof for L2: %v", err)
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get local exit proof, error: %s", err)})
			return
		}

	default:
		b.logger.Warnf("unsupported network id for claim proof: %d", networkID)
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("failed to get claim proof, unsupported network %d", networkID)})
		return
	}

	proofRollupExitRoot, err := b.l1InfoTree.GetRollupExitTreeMerkleProof(ctx, networkID, info.RollupExitRoot)
	if err != nil {
		b.logger.Errorf("failed to get rollup exit proof (network id=%d, leaf index=%d, deposit count=%d): %v",
			networkID, l1InfoTreeIndex, depositCount, err)
		c.JSON(http.StatusInternalServerError,
			gin.H{
				"error": fmt.Sprintf("failed to get rollup exit proof (network id=%d, leaf index=%d, deposit count=%d), error: %s",
					networkID, l1InfoTreeIndex, depositCount, err)})
		return
	}

	infoResponse := NewL1InfoTreeLeafResponse(info)

	c.JSON(http.StatusOK, types.ClaimProof{
		ProofLocalExitRoot:  types.ConvertToProofResponse(proofLocalExitRoot),
		ProofRollupExitRoot: types.ConvertToProofResponse(proofRollupExitRoot),
		L1InfoTreeLeaf:      *infoResponse,
	})
}

// GetLastReorgEventHandler returns the most recent reorganization event for the specified network.
//
// @Summary Get last reorg event
// @Description Retrieves the last known reorg event for either L1 or L2, based on the provided network ID.
// @Tags reorgs
// @Param network_id query int true "Network ID (e.g., 0 for L1, or the ID of the L2 network)"
// @Produce json
// @Success 200 {object} bridgesync.LastReorg "Details of the last reorg event"
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Router /last-reorg-event [get]
func (b *BridgeService) GetLastReorgEventHandler(c *gin.Context) {
	b.logger.Debugf("GetLastReorgEvent request received (network id=%s)", c.Query(networkIDParam))
	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(metrics.GetLastReorgEventReq, statusCode, startTime)
	}()

	networkID, err := parseUintQuery(c, networkIDParam, true, uint32(0))
	if err != nil {
		b.logger.Warnf(errNetworkID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var reorgEvent *bridgesync.LastReorg

	switch networkID {
	case mainnetNetworkID:
		if b.bridgeL1 == nil {
			c.JSON(http.StatusServiceUnavailable,
				gin.H{"error": "L1 bridge syncer is not available"})
			return
		}
		reorgEvent, err = b.bridgeL1.GetLastReorgEvent(ctx)
		if err != nil {
			b.logger.Errorf("failed to get last reorg event for L1 network: %v", err)
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get last reorg event for the L1 network, error: %s", err)})
			return
		}
	case b.networkID:
		if b.bridgeL2 == nil {
			c.JSON(http.StatusServiceUnavailable,
				gin.H{"error": "L2 bridge syncer is not available"})
			return
		}
		reorgEvent, err = b.bridgeL2.GetLastReorgEvent(ctx)
		if err != nil {
			b.logger.Errorf("failed to get last reorg event for L2 network (ID=%d): %v", networkID, err)
			c.JSON(http.StatusInternalServerError,
				gin.H{"error": fmt.Sprintf("failed to get last reorg event for the L2 network (ID=%d), error: %s", networkID, err)})
			return
		}
	default:
		b.logger.Warnf(errNetworkID, networkID)
		c.JSON(http.StatusBadRequest,
			gin.H{"error": fmt.Sprintf("failed to get last reorg event, unsupported network %d", networkID)})
		return
	}

	c.JSON(http.StatusOK, reorgEvent)
}

// GetRemoveGEREventsHandler retrieves remove GER events.
//
// @Summary Get remove GER events
// @Description Returns a list of remove GER events, optionally filtered by block range or specific GER
// @Tags ger-events
// @Param from_block query uint64 false "Start block number for filtering"
// @Param to_block query uint64 false "End block number for filtering"
// @Param global_exit_root query string false "Filter by specific Global Exit Root hash"
// @Produce json
// @Success 200 {object} types.RemoveGEREventsResult "List of remove GER events"
// @Failure 400 {object} types.ErrorResponse "Bad Request"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Failure 503 {object} types.ErrorResponse "Service Unavailable"
// @Router /remove-ger-events [get]
func (b *BridgeService) GetRemoveGEREventsHandler(c *gin.Context) {
	b.logger.Debugf("GetRemoveGEREvents request received")

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(metrics.GetRemoveGEREventsReq, statusCode, startTime)
	}()

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	// Check if L2GERSyncer is available
	if b.injectedGERs == nil {
		statusCode = http.StatusServiceUnavailable
		c.JSON(statusCode, gin.H{"error": "L2 GER syncer is not available"})
		return
	}

	// Parse query parameters
	fromBlockStr := c.Query("from_block")
	toBlockStr := c.Query("to_block")
	globalExitRootStr := c.Query("global_exit_root")

	var removeEvents []*l2gersync.RemoveGEREvent
	var err error

	// Determine which query method to use based on parameters
	switch {
	case globalExitRootStr != "":
		// Filter by specific GER
		if !isValidHexHash(globalExitRootStr) {
			statusCode = http.StatusBadRequest
			c.JSON(statusCode, gin.H{"error": "invalid global_exit_root parameter, must be a valid 32-byte hex hash (66 characters including 0x prefix)"})
			return
		}
		globalExitRoot := common.HexToHash(globalExitRootStr)
		removeEvents, err = b.injectedGERs.GetRemoveGEREventsByGER(ctx, globalExitRoot)

	case fromBlockStr != "" || toBlockStr != "":
		// Filter by block range
		fromBlock := uint64(0)
		toBlock := ^uint64(0) // Max uint64

		if fromBlockStr != "" {
			fromBlock, err = strconv.ParseUint(fromBlockStr, 10, 64)
			if err != nil {
				statusCode = http.StatusBadRequest
				c.JSON(statusCode, gin.H{"error": "invalid from_block parameter"})
				return
			}
		}

		if toBlockStr != "" {
			toBlock, err = strconv.ParseUint(toBlockStr, 10, 64)
			if err != nil {
				statusCode = http.StatusBadRequest
				c.JSON(statusCode, gin.H{"error": "invalid to_block parameter"})
				return
			}
		}

		if fromBlock > toBlock {
			statusCode = http.StatusBadRequest
			c.JSON(statusCode, gin.H{"error": "from_block must be less than or equal to to_block"})
			return
		}

		removeEvents, err = b.injectedGERs.GetRemoveGEREventsByBlockRange(ctx, fromBlock, toBlock)

	default:
		// Get all remove events
		removeEvents, err = b.injectedGERs.GetRemoveGEREvents(ctx)
	}

	if err != nil {
		b.logger.Errorf("failed to get remove GER events: %v", err)
		statusCode = http.StatusInternalServerError
		c.JSON(statusCode, gin.H{"error": fmt.Sprintf("failed to get remove GER events: %s", err)})
		return
	}

	// Convert to response format
	responseEvents := make([]*types.RemoveGEREventResponse, len(removeEvents))
	for i, event := range removeEvents {
		responseEvents[i] = &types.RemoveGEREventResponse{
			ID:             event.ID,
			GlobalExitRoot: types.Hash(event.GlobalExitRoot.Hex()),
			BlockNum:       event.BlockNum,
			CreatedAt:      event.CreatedAt,
		}
	}

	result := types.RemoveGEREventsResult{
		RemoveGEREvents: responseEvents,
		Count:           len(responseEvents),
	}

	c.JSON(http.StatusOK, result)
}

// populateNetworkSyncInfo populates sync information for a network if it's active
func (b *BridgeService) populateNetworkSyncInfo(
	ctx context.Context,
	c *gin.Context,
	bridge Bridger,
	networkInfo *types.NetworkSyncInfo,
	networkName string,
) int {
	statusCode := http.StatusOK

	contractDepositCount, err := bridge.GetContractDepositCount(ctx)
	if err != nil {
		statusCode = http.StatusInternalServerError
		c.JSON(statusCode,
			gin.H{"error": fmt.Sprintf("failed to get deposit count from %s bridge contract: %s", networkName, err)})
		return statusCode
	}

	// Get the last bridge from database
	_, bridgesCount, err := bridge.GetBridgesPaged(ctx, 1, 1, nil, nil, "")
	if err != nil {
		statusCode = http.StatusInternalServerError
		c.JSON(statusCode,
			gin.H{"error": fmt.Sprintf("failed to get bridges from %s database: %s", networkName, err)})
		return statusCode
	}

	networkInfo.SynchronizedDepositCount = uint32(bridgesCount)
	networkInfo.ContractDepositCount = contractDepositCount
	networkInfo.IsSynced = networkInfo.ContractDepositCount == networkInfo.SynchronizedDepositCount

	if !networkInfo.IsSynced {
		lastProcessedBlock, err := bridge.GetLastProcessedBlock(ctx)
		if err != nil {
			b.logger.Warnf("failed to get last processed block for %s: %s", networkName, err)
		} else {
			networkInfo.LastProcessedBlock = lastProcessedBlock
		}

		networkBlock, err := bridge.GetLatestNetworkBlock(ctx)
		if err != nil {
			b.logger.Warnf("failed to get latest network block for %s: %s", networkName, err)
		} else {
			networkInfo.NetworkBlock = networkBlock
		}
	}

	return statusCode
}

// GetSyncStatusHandler returns the bridge synchronization status for L1 and L2 networks.
//
// @Summary Get bridge synchronization status
// @Description Returns bridge sync status by comparing on-chain bridge deposit counts with local database counts.
// @Description Shows if bridge syncers are active and whether they're keeping up with on-chain events.
// @Tags sync
// @Produce json
// @Success 200 {object} types.SyncStatus "Bridge synchronization status for L1 and L2 networks"
// @Failure 500 {object} types.ErrorResponse "Internal Server Error"
// @Router /sync-status [get]
func (b *BridgeService) GetSyncStatusHandler(c *gin.Context) {
	b.logger.Debugf("GetSyncStatus request received")

	statusCode := http.StatusOK
	startTime := time.Now()
	defer func() {
		reportMetrics(metrics.GetSyncStatusReq, statusCode, startTime)
	}()

	ctx, cancel := context.WithTimeout(c, b.readTimeout)
	defer cancel()

	var syncStatus types.SyncStatus

	// Check L1 sync status
	var l1IsActive bool
	if b.bridgeL1 != nil {
		l1IsActive = b.bridgeL1.IsActive(ctx)
		syncStatus.L1Info = &types.NetworkSyncInfo{
			IsActive: l1IsActive,
		}

		if l1IsActive {
			statusCode = b.populateNetworkSyncInfo(ctx, c, b.bridgeL1, syncStatus.L1Info, "L1")
			if statusCode != http.StatusOK {
				return
			}
		}
	} else {
		syncStatus.L1Info = &types.NetworkSyncInfo{
			IsActive: false,
		}
	}

	// Check L2 sync status
	var l2IsActive bool
	if b.bridgeL2 != nil {
		l2IsActive = b.bridgeL2.IsActive(ctx)
		syncStatus.L2Info = &types.NetworkSyncInfo{
			IsActive: l2IsActive,
		}

		if l2IsActive {
			statusCode = b.populateNetworkSyncInfo(ctx, c, b.bridgeL2, syncStatus.L2Info, "L2")
			if statusCode != http.StatusOK {
				return
			}
		}
	} else {
		syncStatus.L2Info = &types.NetworkSyncInfo{
			IsActive: false,
		}
	}

	c.JSON(statusCode, syncStatus)
}

func (b *BridgeService) getFirstL1InfoTreeIndexForL1Bridge(ctx context.Context, depositCount uint32) (uint32, error) {
	if b.bridgeL1 == nil {
		return 0, fmt.Errorf("L1 bridge syncer is not available")
	}

	lastInfo, err := b.l1InfoTree.GetLastInfo()
	if err != nil {
		return 0, err
	}

	root, err := b.bridgeL1.GetRootByLER(ctx, lastInfo.MainnetExitRoot)
	if err != nil {
		b.logger.Infof(
			"failed to get root by LER for L1: %v, lastInfo MainnetExitRoot: %v, using fallback mechanism",
			err,
			lastInfo.MainnetExitRoot,
		)
		root, err = b.bridgeL1.GetLastRoot(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to get last root for L1: %w", err)
		}
		lastInfo, err = b.l1InfoTree.GetInfoByIndex(ctx, root.Index)
		if err != nil {
			return 0, fmt.Errorf("failed to get last info for L1: %w", err)
		}
	}
	if root.Index < depositCount {
		return 0, ErrNotOnL1Info
	}

	firstInfo, err := b.l1InfoTree.GetFirstInfo()
	if err != nil {
		return 0, err
	}

	// Binary search between the first and last blocks where L1 info tree was updated.
	// Find the smallest l1 info tree index that is greater than depositCount and matches with
	// a MER that is included on the l1 info tree
	bestResult := lastInfo
	lowerLimit := firstInfo.BlockNumber
	upperLimit := lastInfo.BlockNumber
	for lowerLimit <= upperLimit {
		targetBlock := lowerLimit + ((upperLimit - lowerLimit) / binarySearchDivider)
		targetInfo, err := b.l1InfoTree.GetFirstInfoAfterBlock(targetBlock)
		if err != nil {
			return 0, err
		}
		root, err := b.bridgeL1.GetRootByLER(ctx, targetInfo.MainnetExitRoot)
		if err != nil {
			return 0, err
		}
		if root.Index < depositCount {
			lowerLimit = targetBlock + 1
		} else if root.Index == depositCount {
			bestResult = targetInfo
			break
		} else {
			bestResult = targetInfo
			upperLimit = targetBlock - 1
		}
	}

	return bestResult.L1InfoTreeIndex, nil
}

func (b *BridgeService) getFirstL1InfoTreeIndexForL2Bridge(ctx context.Context, depositCount uint32) (uint32, error) {
	if b.bridgeL2 == nil {
		return 0, fmt.Errorf("L2 bridge syncer is not available")
	}

	// NOTE: this code assumes that all the rollup exit roots
	// (produced by the smart contract call verifyBatches / verifyBatchesTrustedAggregator)
	// are included in the L1 info tree. As per the current implementation (smart contracts) of the protocol
	// this is true. This could change in the future
	lastVerified, err := b.l1InfoTree.GetLastVerifiedBatches(b.networkID)
	if err != nil {
		return 0, err
	}

	root, err := b.bridgeL2.GetRootByLER(ctx, lastVerified.ExitRoot)
	if err != nil {
		b.logger.Infof(
			"failed to get root by LER for L2: %v, lastVerified ExitRoot: %v, using fallback mechanism",
			err,
			lastVerified.ExitRoot,
		)
		root, err = b.bridgeL2.GetLastRoot(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to get last root for L2: %w", err)
		}
		lastVerified, err = b.l1InfoTree.GetFirstVerifiedBatchesAfterBlock(b.networkID, root.BlockNum)
		if err != nil {
			return 0, fmt.Errorf("failed to get first verified batch after block for L2: %w, block num: %d", err, root.BlockNum)
		}
	}
	if root.Index < depositCount {
		return 0, ErrNotOnL1Info
	}

	firstVerified, err := b.l1InfoTree.GetFirstVerifiedBatches(b.networkID)
	if err != nil {
		return 0, err
	}

	// Binary search between the first and last blocks where batches were verified.
	// Find the smallest deposit count that is greater than depositCount and matches with
	// a LER that is verified
	bestResult := lastVerified
	lowerLimit := firstVerified.BlockNumber
	upperLimit := lastVerified.BlockNumber
	for lowerLimit <= upperLimit {
		targetBlock := lowerLimit + ((upperLimit - lowerLimit) / binarySearchDivider)
		targetVerified, err := b.l1InfoTree.GetFirstVerifiedBatchesAfterBlock(b.networkID, targetBlock)
		if err != nil {
			return 0, err
		}
		root, err = b.bridgeL2.GetRootByLER(ctx, targetVerified.ExitRoot)
		if err != nil {
			return 0, err
		}
		if root.Index < depositCount {
			lowerLimit = targetBlock + 1
		} else if root.Index == depositCount {
			bestResult = targetVerified
			break
		} else {
			bestResult = targetVerified
			upperLimit = targetBlock - 1
		}
	}

	info, err := b.l1InfoTree.GetFirstL1InfoWithRollupExitRoot(bestResult.RollupExitRoot)
	if err != nil {
		return 0, err
	}
	return info.L1InfoTreeIndex, nil
}

// setupRequest parses the pagination parameters from the request context
func (b *BridgeService) setupRequest(c *gin.Context) (context.Context, context.CancelFunc, uint32, uint32, error) {
	pageNumber, err := parseUintQuery(c, pageNumberParam, false, DefaultPage)
	if err != nil {
		return nil, nil, 0, 0, err
	}

	pageSize, err := parseUintQuery(c, pageSizeParam, false, DefaultPageSize)
	if err != nil {
		return nil, nil, 0, 0, err
	}

	err = validatePaginationParams(pageNumber, pageSize)
	if err != nil {
		return nil, nil, 0, 0, err
	}

	ctx, cancel := context.WithTimeout(c, b.readTimeout)

	return ctx, cancel, pageNumber, pageSize, nil
}

// reportMetrics reports the request metric for the given handler and status code
func reportMetrics(handlerID string, statusCode int, startTime time.Time) {
	metrics.IncTotalRequestCounter(handlerID, strconv.Itoa(statusCode))
	metrics.ObserveRequestLatencyHistogram(handlerID, startTime)
}

// isValidHexHash validates that a string is a valid 32-byte hex hash
// Expected format: 0x followed by exactly 64 hex characters (total 66 chars)
func isValidHexHash(s string) bool {
	// Check length: 0x (2 chars) + 64 hex chars = 66 total
	if len(s) != 66 {
		return false
	}

	// Check 0x prefix
	if !strings.HasPrefix(s, "0x") {
		return false
	}

	// Check that remaining characters are valid hex
	for _, char := range s[2:] {
		if !((char >= '0' && char <= '9') ||
			(char >= 'a' && char <= 'f') ||
			(char >= 'A' && char <= 'F')) {
			return false
		}
	}

	return true
}
