package claimer

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	aggkit "github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

// API major versions. A new one is only added for a breaking change, and the previous one keeps
// being served for at least one release (see SPEC.md, "API versioning").
const (
	apiVersion1 = 1
	apiVersion2 = 2
)

// supportedAPIVersions returns the API major versions this claimer serves.
func supportedAPIVersions() []int { return []int{apiVersion1, apiVersion2} }

const (
	apiBasePath       = "/claimer/v1"
	apiBasePathV2     = "/claimer/v2"
	destAddressParam  = "dest_address"
	addressParam      = "address"
	depositCountParam = "deposit_count"
	shutdownTimeout   = 5 * time.Second
)

// Server exposes the claimer over HTTP using Gin.
type Server struct {
	logger       *log.Logger
	address      string
	readTimeout  time.Duration
	writeTimeout time.Duration
	claimer      *Claimer
	router       *gin.Engine
	// version is the build version reported by /health.
	version VersionInfo
}

// NewServer builds the HTTP server and registers the routes.
func NewServer(cfg *Config, claimer *Claimer, logger *log.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	s := &Server{
		logger:       logger,
		address:      cfg.ListenAddress(),
		readTimeout:  time.Duration(cfg.ReadTimeoutSeconds) * time.Second,
		writeTimeout: time.Duration(cfg.WriteTimeoutSeconds) * time.Second,
		claimer:      claimer,
		router:       router,
		version:      versionInfo(),
	}

	v1 := router.Group(apiBasePath)
	v1.GET("/health", s.handleHealth)
	v1.GET("/bridges", s.handleBridges)
	v1.GET("/claim-params", s.handleClaimParams)

	v2 := router.Group(apiBasePathV2)
	v2.GET("/health", s.handleHealthV2)
	v2.GET("/bridges", s.handleBridgesV2)
	v2.GET("/claim-params", s.handleClaimParamsV2)

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
		s.logger.Infof("claimer backend listening on %s (base paths %s, %s)",
			s.address, apiBasePath, apiBasePathV2)
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

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{
		Status:      "ok",
		NetworkID:   s.claimer.NetworkID(),
		Version:     s.version,
		APIVersions: supportedAPIVersions(),
	})
}

func (s *Server) handleHealthV2(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponseV2{
		APIVersion:  apiVersion2,
		Status:      "ok",
		NetworkID:   s.claimer.NetworkID(),
		Version:     s.version,
		APIVersions: supportedAPIVersions(),
	})
}

func (s *Server) handleBridges(c *gin.Context) {
	destAddr, ok := parseAddress(c, destAddressParam, respondErrorV1)
	if !ok {
		return
	}

	bridges, err := s.claimer.ListBridges(destAddr)
	if err != nil {
		respondErrorV1(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, BridgesResponse{
		NetworkID:          s.claimer.NetworkID(),
		DestinationAddress: destAddr.Hex(),
		Bridges:            bridges,
	})
}

func (s *Server) handleClaimParams(c *gin.Context) {
	destAddr, ok := parseAddress(c, destAddressParam, respondErrorV1)
	if !ok {
		return
	}

	depositCount, ok := parseDepositCount(c, respondErrorV1)
	if !ok {
		return
	}

	claims, err := s.claimer.BuildClaimParams(c.Request.Context(), destAddr, depositCount)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrLocalExitRootNotSettled) {
			status = http.StatusConflict
		}
		respondErrorV1(c, status, err.Error())
		return
	}

	c.JSON(http.StatusOK, ClaimParamsResponse{
		NetworkID:          s.claimer.NetworkID(),
		DestinationAddress: destAddr.Hex(),
		Claims:             claims,
	})
}

func (s *Server) handleBridgesV2(c *gin.Context) {
	address, ok := parseAddress(c, addressParam, respondErrorV2)
	if !ok {
		return
	}

	bridges, err := s.claimer.ListBridgesV2(address)
	if err != nil {
		respondErrorV2(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, BridgesResponseV2{
		APIVersion: apiVersion2,
		NetworkID:  s.claimer.NetworkID(),
		Address:    address.Hex(),
		Bridges:    bridges,
	})
}

func (s *Server) handleClaimParamsV2(c *gin.Context) {
	address, ok := parseAddress(c, addressParam, respondErrorV2)
	if !ok {
		return
	}

	depositCount, ok := parseDepositCount(c, respondErrorV2)
	if !ok {
		return
	}

	claims, err := s.claimer.BuildClaimParamsV2(c.Request.Context(), address, depositCount)
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
		NetworkID:  s.claimer.NetworkID(),
		Address:    address.Hex(),
		Claims:     claims,
	})
}

// errorResponder writes an error body in the form of one API version.
type errorResponder func(c *gin.Context, status int, msg string)

// parseAddress reads and validates the address query param called name, writing a 400 on failure.
func parseAddress(c *gin.Context, name string, respond errorResponder) (common.Address, bool) {
	raw := c.Query(name)
	if raw == "" {
		respond(c, http.StatusBadRequest, name+" query parameter is required")
		return common.Address{}, false
	}
	if !common.IsHexAddress(raw) {
		respond(c, http.StatusBadRequest, "invalid "+name+": "+raw)
		return common.Address{}, false
	}
	return common.HexToAddress(raw), true
}

// parseDepositCount reads the optional deposit_count query param. Returns (nil, true) when absent
// (all matching exits are returned), writing a 400 only on a malformed value.
func parseDepositCount(c *gin.Context, respond errorResponder) (*uint32, bool) {
	raw := c.Query(depositCountParam)
	if raw == "" {
		return nil, true
	}
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		respond(c, http.StatusBadRequest, "invalid "+depositCountParam+": "+raw)
		return nil, false
	}
	dc := uint32(v)
	return &dc, true
}

func respondErrorV1(c *gin.Context, status int, msg string) {
	c.JSON(status, errorResponse{Error: msg})
}

func respondErrorV2(c *gin.Context, status int, msg string) {
	c.JSON(status, errorResponseV2{APIVersion: apiVersion2, Error: msg})
}
