package claimer

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	aggkit "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

// apiVersion2 is the API major version this claimer serves. API v1 has been removed: the claimer and
// its UI are deployed together, so only one version is served (see SPEC.md, "API versioning").
const apiVersion2 = 2

// supportedAPIVersions returns the API major versions this claimer serves.
func supportedAPIVersions() []int { return []int{apiVersion2} }

const (
	// apiBasePathV1 only keeps /health (so existing probes keep working); any other path under it
	// answers 410 Gone.
	apiBasePathV1     = "/claimer/v1"
	apiBasePathV2     = "/claimer/v2"
	addressParam      = "address"
	depositCountParam = "deposit_count"
	shutdownTimeout   = 5 * time.Second
	// apiV1RemovedMsg is the error of every removed /claimer/v1 endpoint.
	apiV1RemovedMsg = "claimer API v1 has been removed, use " + apiBasePathV2
)

// Server exposes the claimer over HTTP using Gin.
type Server struct {
	logger       *log.Logger
	address      string
	readTimeout  time.Duration
	writeTimeout time.Duration
	// runs holds the run being served; follow mode replaces it while requests are in flight.
	runs   *runSlot
	router *gin.Engine
	// version is the build version reported by /health.
	version VersionInfo
	// follow is the follow mode loop, nil when it is disabled. Set before Start.
	follow *follower
}

// NewServer builds the HTTP server and registers the routes.
func NewServer(cfg *Config, claimer *Claimer, logger *log.Logger) *Server {
	return newServer(cfg, &servedRun{claimer: claimer}, logger)
}

// newServer is NewServer for a loaded run (see loadRun), whose description /health reports.
func newServer(cfg *Config, run *servedRun, logger *log.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	s := &Server{
		logger:       logger,
		address:      cfg.ListenAddress(),
		readTimeout:  time.Duration(cfg.ReadTimeoutSeconds) * time.Second,
		writeTimeout: time.Duration(cfg.WriteTimeoutSeconds) * time.Second,
		runs:         newRunSlot(run),
		router:       router,
		version:      versionInfo(),
	}

	v2 := router.Group(apiBasePathV2)
	v2.GET("/health", s.handleHealthV2)
	v2.GET("/bridges", s.handleBridgesV2)
	v2.GET("/claim-params", s.handleClaimParamsV2)

	// API v1 is gone but its /health keeps answering (with api_versions: [2]) for existing probes.
	router.GET(apiBasePathV1+"/health", s.handleHealthV1)
	router.NoRoute(handleRemovedV1)

	return s
}

// Start runs the HTTP server until the context is cancelled, then shuts it down gracefully.
func (s *Server) Start(ctx context.Context) error {
	srv := &http.Server{
		Addr:         s.address,
		Handler:      s.router,
		ReadTimeout:  s.readTimeout,
		WriteTimeout: s.writeTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Infof("claimer backend listening on %s (base path %s)", s.address, apiBasePathV2)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func versionInfo() VersionInfo {
	v := aggkit.GetVersion()
	return VersionInfo{
		Version:   v.Version,
		GitRev:    v.GitRev,
		GitBranch: v.GitBranch,
		BuildDate: v.BuildDate,
		GoVersion: v.GoVersion,
		OS:        v.OS,
		Arch:      v.Arch,
	}
}

// followStatus returns the state of follow mode, nil when it is disabled.
func (s *Server) followStatus() *FollowStatus {
	if s.follow == nil {
		return nil
	}
	return s.follow.Status()
}

// handleHealthV1 answers the legacy /claimer/v1/health so existing probes and monitoring keep working;
// its api_versions tells a client that only v2 is served.
func (s *Server) handleHealthV1(c *gin.Context) {
	run, done := s.runs.acquire()
	defer done()
	c.JSON(http.StatusOK, HealthResponse{
		Status:      "ok",
		NetworkID:   run.claimer.NetworkID(),
		Version:     s.version,
		APIVersions: supportedAPIVersions(),
		Run:         run.info,
		Follow:      s.followStatus(),
	})
}

// handleRemovedV1 answers 410 Gone for any removed /claimer/v1 path. Other unknown paths keep the
// default 404.
func handleRemovedV1(c *gin.Context) {
	if strings.HasPrefix(c.Request.URL.Path, apiBasePathV1+"/") {
		c.JSON(http.StatusGone, apiRemovedResponse{Error: apiV1RemovedMsg, APIVersions: supportedAPIVersions()})
	}
}

func (s *Server) handleHealthV2(c *gin.Context) {
	run, done := s.runs.acquire()
	defer done()
	c.JSON(http.StatusOK, HealthResponseV2{
		APIVersion:  apiVersion2,
		Status:      "ok",
		NetworkID:   run.claimer.NetworkID(),
		Version:     s.version,
		APIVersions: supportedAPIVersions(),
		Run:         run.info,
		Follow:      s.followStatus(),
	})
}

func (s *Server) handleBridgesV2(c *gin.Context) {
	run, done := s.runs.acquire()
	defer done()

	address, ok := parseAddress(c, addressParam)
	if !ok {
		return
	}

	bridges, err := run.claimer.ListBridgesV2(address)
	if err != nil {
		respondErrorV2(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, BridgesResponseV2{
		APIVersion: apiVersion2,
		NetworkID:  run.claimer.NetworkID(),
		Address:    address.Hex(),
		Bridges:    bridges,
	})
}

func (s *Server) handleClaimParamsV2(c *gin.Context) {
	run, done := s.runs.acquire()
	defer done()

	address, ok := parseAddress(c, addressParam)
	if !ok {
		return
	}

	depositCount, ok := parseDepositCount(c)
	if !ok {
		return
	}

	claims, err := run.claimer.BuildClaimParamsV2(c.Request.Context(), address, depositCount)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrLocalExitRootNotSettled) {
			status = http.StatusConflict
		}
		respondErrorV2(c, status, err.Error())
		return
	}

	c.JSON(http.StatusOK, ClaimParamsResponseV2{
		APIVersion: apiVersion2,
		NetworkID:  run.claimer.NetworkID(),
		Address:    address.Hex(),
		Claims:     claims,
	})
}

// parseAddress reads and validates the address query param called name, writing a 400 on failure.
func parseAddress(c *gin.Context, name string) (common.Address, bool) {
	raw := c.Query(name)
	if raw == "" {
		respondErrorV2(c, http.StatusBadRequest, name+" query parameter is required")
		return common.Address{}, false
	}
	if !common.IsHexAddress(raw) {
		respondErrorV2(c, http.StatusBadRequest, "invalid "+name+": "+raw)
		return common.Address{}, false
	}
	return common.HexToAddress(raw), true
}

// parseDepositCount reads the optional deposit_count query param. Returns (nil, true) when absent
// (all matching exits are returned), writing a 400 only on a malformed value.
func parseDepositCount(c *gin.Context) (*uint32, bool) {
	raw := c.Query(depositCountParam)
	if raw == "" {
		return nil, true
	}
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		respondErrorV2(c, http.StatusBadRequest, "invalid "+depositCountParam+": "+raw)
		return nil, false
	}
	dc := uint32(v)
	return &dc, true
}

func respondErrorV2(c *gin.Context, status int, msg string) {
	c.JSON(status, errorResponseV2{APIVersion: apiVersion2, Error: msg})
}
