// @title Auto Claim Admin API
// @version 1.0
// @description Admin API for the Auto Claim service (manual approval and rejection).

// @contact.name API Support
// @contact.url https://polygon.technology/

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @BasePath /autoclaim/v1

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	_ "github.com/agglayer/aggkit/autoclaim/api/docs"
	"github.com/agglayer/aggkit/autoclaim/apitypes"
	autoclaimstorage "github.com/agglayer/aggkit/autoclaim/storage"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginswagger "github.com/swaggo/gin-swagger"
)

const (
	// Prefix is the non-colliding REST prefix for the Auto Claim admin API.
	Prefix = "/autoclaim/v1"

	swaggerInstanceName = "autoclaim"

	manualPolicy            = "manual"
	shutdownTimeoutDuration = 5 * time.Second

	// maxDecisionBodyBytes caps the manual decision request body to defend against memory exhaustion.
	maxDecisionBodyBytes = 1 << 20 // 1 MiB
	maxDeciderLength     = 256
	maxDeciderIDLength   = 256
	maxReasonLength      = 1024
)

// Config configures the optional Auto Claim admin API.
type Config struct {
	Enabled      bool
	Address      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// Storage is the admin API persistence boundary.
type Storage interface {
	GetRequest(ctx context.Context, key autoclaimtypes.RequestKey) (*autoclaimtypes.AutoClaimRequest, error)
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

// API exposes Auto Claim manual decision routes.
type API struct {
	cfg      Config
	storage  Storage
	registry autoclaimtypes.ClaimerRegistry
	router   *gin.Engine
	now      func() time.Time
	log      aggkitcommon.Logger
}

// ConfigFromRESTConfig converts a common REST config into an Auto Claim admin API config.
func ConfigFromRESTConfig(enabled bool, rest aggkitcommon.RESTConfig) Config {
	return Config{
		Enabled:      enabled,
		Address:      rest.Address(),
		ReadTimeout:  rest.ReadTimeout.Duration,
		WriteTimeout: rest.WriteTimeout.Duration,
	}
}

// New creates an optional Auto Claim admin API. Disabled APIs do not register Auto Claim routes.
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

// RegisterRoutes registers Auto Claim admin routes on router when the API is enabled.
func (a *API) RegisterRoutes(router gin.IRouter) {
	if a == nil || !a.cfg.Enabled {
		return
	}

	group := router.Group(Prefix)
	{
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
		// Use a fresh context so cancellation of the parent context starts, rather than aborts, graceful shutdown.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeoutDuration)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if a.log != nil {
		a.log.Infof("Auto Claim admin API listening on %s", a.cfg.Address)
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("start Auto Claim admin API: %w", err)
	}
	return nil
}

// approveBridge approves a request waiting for manual approval.
//
// @Summary Approve Auto Claim bridge request
// @Description Approves a request currently in manual-approval-required and advances the matching claimer when present.
// @Tags autoclaim
// @Param id path string true "Auto Claim request ID"
// @Param decision body apitypes.DecisionRequest false "Manual approval metadata"
// @Accept json
// @Produce json
// @Success 200 {object} apitypes.RequestResponse
// @Failure 400 {object} apitypes.ErrorResponse "Bad Request"
// @Failure 404 {object} apitypes.ErrorResponse "Not Found"
// @Failure 409 {object} apitypes.ErrorResponse "Conflict"
// @Failure 500 {object} apitypes.ErrorResponse "Internal Server Error"
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
// @Param decision body apitypes.DecisionRequest false "Manual rejection metadata"
// @Accept json
// @Produce json
// @Success 200 {object} apitypes.RequestResponse
// @Failure 400 {object} apitypes.ErrorResponse "Bad Request"
// @Failure 404 {object} apitypes.ErrorResponse "Not Found"
// @Failure 409 {object} apitypes.ErrorResponse "Conflict"
// @Failure 500 {object} apitypes.ErrorResponse "Internal Server Error"
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
	if err := validateDecisionRequest(body); err != nil {
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

	c.JSON(http.StatusOK, apitypes.NewRequestResponse(*updated))
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

func readDecisionRequest(reader io.Reader) (apitypes.DecisionRequest, error) {
	var request apitypes.DecisionRequest
	if reader == nil {
		return request, nil
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxDecisionBodyBytes))
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

func validateDecisionRequest(request apitypes.DecisionRequest) error {
	if len(request.Decider) > maxDeciderLength {
		return fmt.Errorf("decider exceeds maximum length of %d", maxDeciderLength)
	}
	if len(request.DeciderID) > maxDeciderIDLength {
		return fmt.Errorf("decider_id exceeds maximum length of %d", maxDeciderIDLength)
	}
	if len(request.Reason) > maxReasonLength {
		return fmt.Errorf("reason exceeds maximum length of %d", maxReasonLength)
	}
	return nil
}

func defaultDecisionReason(result autoclaimtypes.PolicyResult, reason string) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	return fmt.Sprintf("%s through Auto Claim API", result)
}

func writeError(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"error": err.Error()})
}
