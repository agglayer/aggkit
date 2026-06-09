// @title Auto Claim API
// @version 1.0
// @description API documentation for the Auto Claim service

// @contact.name API Support
// @contact.url https://polygon.technology/

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @BasePath /autoclaim/v1

package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginswagger "github.com/swaggo/gin-swagger"

	_ "github.com/agglayer/aggkit/autoclaim/api/docs"
	autoclaimstorage "github.com/agglayer/aggkit/autoclaim/storage"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
)

const (
	// Prefix is the non-colliding REST prefix for the Auto Claim API.
	Prefix = "/autoclaim/v1"

	swaggerInstanceName = "autoclaim"

	defaultPageSize = uint32(100)
	manualPolicy    = "manual"
)

// Config configures the optional Auto Claim REST API.
type Config struct {
	Enabled      bool
	Address      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// Storage is the API persistence boundary.
type Storage interface {
	GetRequest(ctx context.Context, key autoclaimtypes.RequestKey) (*autoclaimtypes.AutoClaimRequest, error)
	ListRequests(ctx context.Context, filter autoclaimtypes.RequestFilter) (*autoclaimtypes.RequestPage, error)
	ApproveManualRequest(
		ctx context.Context,
		key autoclaimtypes.RequestKey,
		decision autoclaimtypes.PolicyDecision,
		now time.Time,
	) (*autoclaimtypes.AutoClaimRequest, error)
	RejectManualRequest(
		ctx context.Context,
		key autoclaimtypes.RequestKey,
		decision autoclaimtypes.PolicyDecision,
		now time.Time,
	) (*autoclaimtypes.AutoClaimRequest, error)
}

// Option configures an API instance.
type Option func(*API)

// WithNow configures the clock used for manual decision timestamps.
func WithNow(now func() time.Time) Option {
	return func(api *API) {
		if now != nil {
			api.now = now
		}
	}
}

// WithLogger configures optional API startup logs.
func WithLogger(log aggkitcommon.Logger) Option {
	return func(api *API) {
		api.log = log
	}
}

// API exposes Auto Claim request status and manual decision routes.
type API struct {
	cfg      Config
	storage  Storage
	registry autoclaimtypes.ClaimerRegistry
	router   *gin.Engine
	now      func() time.Time
	log      aggkitcommon.Logger
}

// ConfigFromRESTConfig converts a common REST config into an Auto Claim API config.
func ConfigFromRESTConfig(enabled bool, rest aggkitcommon.RESTConfig) Config {
	return Config{
		Enabled:      enabled,
		Address:      rest.Address(),
		ReadTimeout:  rest.ReadTimeout.Duration,
		WriteTimeout: rest.WriteTimeout.Duration,
	}
}

// New creates an optional Auto Claim API. Disabled APIs do not register Auto Claim routes.
func New(
	cfg Config,
	storage Storage,
	registry autoclaimtypes.ClaimerRegistry,
	options ...Option,
) (*API, error) {
	if cfg.Enabled && storage == nil {
		return nil, fmt.Errorf("autoclaim API storage is nil")
	}

	router := gin.New()
	router.Use(gin.Recovery())

	api := &API{
		cfg:      cfg,
		storage:  storage,
		registry: registry,
		router:   router,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
	for _, option := range options {
		option(api)
	}
	api.RegisterRoutes(router)

	return api, nil
}

// Router returns the API HTTP handler.
func (a *API) Router() http.Handler {
	return a.router
}

// RegisterRoutes registers Auto Claim routes on router when the API is enabled.
func (a *API) RegisterRoutes(router gin.IRouter) {
	if a == nil || !a.cfg.Enabled {
		return
	}

	group := router.Group(Prefix)
	{
		group.GET("/bridges", a.listBridges)
		group.GET("/bridges/:id", a.getBridge)
		group.POST("/bridges/:id/approve", a.approveBridge)
		group.POST("/bridges/:id/reject", a.rejectBridge)

		group.GET("/swagger/*any", ginswagger.WrapHandler(
			swaggerfiles.Handler,
			ginswagger.InstanceName(swaggerInstanceName),
		))
		group.GET("/swagger", func(ctx *gin.Context) {
			ctx.Redirect(http.StatusFound, Prefix+"/swagger/index.html")
		})
	}
}

// Start starts the optional API server and blocks until the server exits or ctx is cancelled.
func (a *API) Start(ctx context.Context) error {
	if a == nil || !a.cfg.Enabled {
		return nil
	}

	server := &http.Server{
		Addr:         a.cfg.Address,
		Handler:      a.router,
		ReadTimeout:  a.cfg.ReadTimeout,
		WriteTimeout: a.cfg.WriteTimeout,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if a.log != nil {
		a.log.Infof("Auto Claim API listening on %s", a.cfg.Address)
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("start Auto Claim API: %w", err)
	}
	return nil
}

// listBridges retrieves paginated Auto Claim bridge requests.
//
// @Summary List Auto Claim bridge requests
// @Description Returns tracked Auto Claim requests with optional filters and pagination.
// @Tags autoclaim
// @Param origin_network query uint32 false "Filter by origin network ID"
// @Param destination_network query uint32 false "Filter by destination network ID"
// @Param status query string false "Filter by request status"
// @Param policy_status query string false "Filter by policy result"
// @Param policy_result query string false "Alias for policy_status"
// @Param bridge_tx_hash query string false "Filter by 0x-prefixed bridge transaction hash"
// @Param claim_tx_hash query string false "Filter by 0x-prefixed claim transaction hash"
// @Param from_block query uint64 false "Filter by minimum bridge block number"
// @Param to_block query uint64 false "Filter by maximum bridge block number"
// @Param page_number query uint32 false "Page number (default 0)"
// @Param page_size query uint32 false "Page size (default 100)"
// @Produce json
// @Success 200 {object} ListResponse
// @Failure 400 {object} ErrorResponse "Bad Request"
// @Failure 500 {object} ErrorResponse "Internal Server Error"
// @Router /bridges [get]
func (a *API) listBridges(c *gin.Context) {
	filter, err := parseRequestFilter(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}

	page, err := a.storage.ListRequests(c.Request.Context(), filter)
	if err != nil {
		writeError(c, http.StatusInternalServerError, err)
		return
	}

	bridges := make([]RequestResponse, 0, len(page.Requests))
	for _, request := range page.Requests {
		if request == nil {
			continue
		}
		bridges = append(bridges, newRequestResponse(*request))
	}
	c.JSON(http.StatusOK, ListResponse{
		Bridges:    bridges,
		Count:      page.Count,
		PageNumber: filter.PageNumber,
		PageSize:   effectivePageSize(filter.PageSize),
	})
}

// getBridge retrieves one Auto Claim bridge request.
//
// @Summary Get Auto Claim bridge request
// @Description Returns one tracked Auto Claim request by request ID.
// @Tags autoclaim
// @Param id path string true "Auto Claim request ID"
// @Produce json
// @Success 200 {object} RequestResponse
// @Failure 404 {object} ErrorResponse "Not Found"
// @Failure 500 {object} ErrorResponse "Internal Server Error"
// @Router /bridges/{id} [get]
func (a *API) getBridge(c *gin.Context) {
	request, ok := a.requestByID(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, newRequestResponse(*request))
}

// approveBridge approves a request waiting for manual approval.
//
// @Summary Approve Auto Claim bridge request
// @Description Approves a request currently in manual-approval-required and advances the matching claimer when present.
// @Tags autoclaim
// @Param id path string true "Auto Claim request ID"
// @Param decision body DecisionRequest false "Manual approval metadata"
// @Accept json
// @Produce json
// @Success 200 {object} RequestResponse
// @Failure 400 {object} ErrorResponse "Bad Request"
// @Failure 404 {object} ErrorResponse "Not Found"
// @Failure 409 {object} ErrorResponse "Conflict"
// @Failure 500 {object} ErrorResponse "Internal Server Error"
// @Router /bridges/{id}/approve [post]
func (a *API) approveBridge(c *gin.Context) {
	a.manualDecision(c, autoclaimtypes.PolicyResultApproved)
}

// rejectBridge rejects a request waiting for manual approval.
//
// @Summary Reject Auto Claim bridge request
// @Description Rejects a request currently in manual-approval-required and advances the matching claimer when present.
// @Tags autoclaim
// @Param id path string true "Auto Claim request ID"
// @Param decision body DecisionRequest false "Manual rejection metadata"
// @Accept json
// @Produce json
// @Success 200 {object} RequestResponse
// @Failure 400 {object} ErrorResponse "Bad Request"
// @Failure 404 {object} ErrorResponse "Not Found"
// @Failure 409 {object} ErrorResponse "Conflict"
// @Failure 500 {object} ErrorResponse "Internal Server Error"
// @Router /bridges/{id}/reject [post]
func (a *API) rejectBridge(c *gin.Context) {
	a.manualDecision(c, autoclaimtypes.PolicyResultRejected)
}

func (a *API) manualDecision(c *gin.Context, result autoclaimtypes.PolicyResult) {
	request, ok := a.requestByID(c)
	if !ok {
		return
	}
	if request.Status != autoclaimtypes.RequestStatusManualApprovalRequired {
		writeError(c, http.StatusConflict, fmt.Errorf("request %s is not waiting for manual approval", request.Key))
		return
	}

	body, err := readDecisionRequest(c.Request.Body)
	if err != nil {
		writeError(c, http.StatusBadRequest, err)
		return
	}
	now := a.now()
	decision := autoclaimtypes.PolicyDecision{
		PolicyName: manualPolicy,
		Result:     result,
		Reason:     defaultDecisionReason(result, body.Reason),
		Metadata:   body.Metadata,
		Decider:    body.Decider,
		DeciderID:  body.DeciderID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	var updated *autoclaimtypes.AutoClaimRequest
	switch result {
	case autoclaimtypes.PolicyResultApproved:
		updated, err = a.storage.ApproveManualRequest(c.Request.Context(), request.Key, decision, now)
	case autoclaimtypes.PolicyResultRejected:
		updated, err = a.storage.RejectManualRequest(c.Request.Context(), request.Key, decision, now)
	default:
		err = fmt.Errorf("unsupported manual decision result: %s", result)
	}
	if err != nil {
		a.writeStorageError(c, err)
		return
	}
	if err := a.notifyClaimer(c.Request.Context(), *updated); err != nil {
		writeError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, newRequestResponse(*updated))
}

func (a *API) requestByID(c *gin.Context) (*autoclaimtypes.AutoClaimRequest, bool) {
	key := autoclaimtypes.RequestKey(c.Param("id"))
	request, err := a.storage.GetRequest(c.Request.Context(), key)
	if err != nil {
		a.writeStorageError(c, err)
		return nil, false
	}
	return request, true
}

func (a *API) notifyClaimer(ctx context.Context, request autoclaimtypes.AutoClaimRequest) error {
	if a.registry == nil {
		return nil
	}
	claimer, ok, err := a.registry.ClaimerForDestination(ctx, request.Bridge.DestinationNetwork)
	if err != nil {
		return fmt.Errorf("resolve claimer for destination %d: %w", request.Bridge.DestinationNetwork, err)
	}
	if !ok || claimer == nil {
		return nil
	}
	if err := claimer.Advance(ctx, request.Key); err != nil {
		return fmt.Errorf("advance autoclaim request %s after manual decision: %w", request.Key, err)
	}
	return nil
}

func (a *API) writeStorageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(c, http.StatusNotFound, err)
	case errors.Is(err, autoclaimstorage.ErrPreconditionFailed), errors.Is(err, autoclaimstorage.ErrInvalidTransition):
		writeError(c, http.StatusConflict, err)
	default:
		writeError(c, http.StatusInternalServerError, err)
	}
}

func parseRequestFilter(c *gin.Context) (autoclaimtypes.RequestFilter, error) {
	var filter autoclaimtypes.RequestFilter
	var err error
	if filter.OriginNetwork, err = parseOptionalUint32(c, "origin_network"); err != nil {
		return filter, err
	}
	if filter.DestinationNetwork, err = parseOptionalUint32(c, "destination_network"); err != nil {
		return filter, err
	}
	if filter.Status, err = parseOptionalStatus(c); err != nil {
		return filter, err
	}
	if filter.PolicyResult, err = parseOptionalPolicyResult(c); err != nil {
		return filter, err
	}
	if filter.BridgeTxHash, err = parseOptionalHash(c, "bridge_tx_hash"); err != nil {
		return filter, err
	}
	if filter.ClaimTxHash, err = parseOptionalHash(c, "claim_tx_hash"); err != nil {
		return filter, err
	}
	if filter.FromBlock, err = parseOptionalUint64(c, "from_block"); err != nil {
		return filter, err
	}
	if filter.ToBlock, err = parseOptionalUint64(c, "to_block"); err != nil {
		return filter, err
	}
	pageNumber, err := parseOptionalUint32(c, "page_number")
	if err != nil {
		return filter, err
	}
	if pageNumber != nil {
		filter.PageNumber = *pageNumber
	}
	pageSize, err := parseOptionalUint32(c, "page_size")
	if err != nil {
		return filter, err
	}
	if pageSize != nil {
		filter.PageSize = *pageSize
	}
	return filter, nil
}

func parseOptionalUint32(c *gin.Context, name string) (*uint32, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid %s parameter: %w", name, err)
	}
	result := uint32(parsed)
	return &result, nil
}

func parseOptionalUint64(c *gin.Context, name string) (*uint64, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid %s parameter: %w", name, err)
	}
	return &parsed, nil
}

func parseOptionalStatus(c *gin.Context) (*autoclaimtypes.RequestStatus, error) {
	value := strings.TrimSpace(c.Query("status"))
	if value == "" {
		return nil, nil
	}
	status := autoclaimtypes.RequestStatus(value)
	switch status {
	case autoclaimtypes.RequestStatusDetected,
		autoclaimtypes.RequestStatusPolicyApproved,
		autoclaimtypes.RequestStatusPolicyRejected,
		autoclaimtypes.RequestStatusManualApprovalRequired,
		autoclaimtypes.RequestStatusQueued,
		autoclaimtypes.RequestStatusSending,
		autoclaimtypes.RequestStatusSent,
		autoclaimtypes.RequestStatusConfirmed,
		autoclaimtypes.RequestStatusFailed:
		return &status, nil
	default:
		return nil, fmt.Errorf("invalid status parameter: %s", value)
	}
}

func parseOptionalPolicyResult(c *gin.Context) (*autoclaimtypes.PolicyResult, error) {
	value := strings.TrimSpace(c.Query("policy_status"))
	if value == "" {
		value = strings.TrimSpace(c.Query("policy_result"))
	}
	if value == "" {
		return nil, nil
	}
	result := autoclaimtypes.PolicyResult(value)
	switch result {
	case autoclaimtypes.PolicyResultApproved,
		autoclaimtypes.PolicyResultRejected,
		autoclaimtypes.PolicyResultManual:
		return &result, nil
	default:
		return nil, fmt.Errorf("invalid policy_status parameter: %s", value)
	}
}

func parseOptionalHash(c *gin.Context, name string) (*common.Hash, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return nil, nil
	}
	if !isHexHash(value) {
		return nil, fmt.Errorf("invalid %s parameter: must be a 0x-prefixed 32-byte hash", name)
	}
	hash := common.HexToHash(value)
	return &hash, nil
}

func isHexHash(value string) bool {
	trimmed := strings.TrimPrefix(value, "0x")
	if len(trimmed) != common.HashLength*2 {
		return false
	}
	_, err := hex.DecodeString(trimmed)
	return err == nil
}

func readDecisionRequest(reader io.Reader) (DecisionRequest, error) {
	var request DecisionRequest
	if reader == nil {
		return request, nil
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return request, fmt.Errorf("read manual decision request: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return request, nil
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return request, fmt.Errorf("decode manual decision request: %w", err)
	}
	return request, nil
}

func defaultDecisionReason(result autoclaimtypes.PolicyResult, reason string) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	return fmt.Sprintf("%s through Auto Claim API", result)
}

func effectivePageSize(pageSize uint32) uint32 {
	if pageSize == 0 {
		return defaultPageSize
	}
	return pageSize
}

func writeError(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"error": err.Error()})
}

func hashPtrHex(hash *common.Hash) *string {
	if hash == nil {
		return nil
	}
	value := hash.Hex()
	return &value
}

func bigIntString(value *big.Int) string {
	if value == nil {
		return ""
	}
	return value.String()
}
