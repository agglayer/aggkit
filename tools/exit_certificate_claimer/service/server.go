package claimer

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
)

const (
	apiBasePath       = "/claimer/v1"
	destAddressParam  = "dest_address"
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
	}

	v1 := router.Group(apiBasePath)
	v1.GET("/health", s.handleHealth)
	v1.GET("/bridges", s.handleBridges)
	v1.GET("/claim-params", s.handleClaimParams)

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
		s.logger.Infof("claimer backend listening on %s (base path %s)", s.address, apiBasePath)
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

func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "network_id": s.claimer.NetworkID()})
}

func (s *Server) handleBridges(c *gin.Context) {
	destAddr, ok := s.parseDestAddress(c)
	if !ok {
		return
	}

	bridges, err := s.claimer.ListBridges(destAddr)
	if err != nil {
		s.respondError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, BridgesResponse{
		NetworkID:          s.claimer.NetworkID(),
		DestinationAddress: destAddr.Hex(),
		Bridges:            bridges,
	})
}

func (s *Server) handleClaimParams(c *gin.Context) {
	destAddr, ok := s.parseDestAddress(c)
	if !ok {
		return
	}

	depositCount, ok := s.parseDepositCount(c)
	if !ok {
		return
	}

	claims, err := s.claimer.BuildClaimParams(c.Request.Context(), destAddr, depositCount)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrLocalExitRootNotSettled) {
			status = http.StatusConflict
		}
		s.respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, ClaimParamsResponse{
		NetworkID:          s.claimer.NetworkID(),
		DestinationAddress: destAddr.Hex(),
		Claims:             claims,
	})
}

// parseDestAddress reads and validates the dest_address query param, writing a 400 on failure.
func (s *Server) parseDestAddress(c *gin.Context) (common.Address, bool) {
	raw := c.Query(destAddressParam)
	if raw == "" {
		s.respondErrorMsg(c, http.StatusBadRequest, destAddressParam+" query parameter is required")
		return common.Address{}, false
	}
	if !common.IsHexAddress(raw) {
		s.respondErrorMsg(c, http.StatusBadRequest, "invalid "+destAddressParam+": "+raw)
		return common.Address{}, false
	}
	return common.HexToAddress(raw), true
}

// parseDepositCount reads the optional deposit_count query param. Returns (nil, true) when absent
// (all matching exits are returned), writing a 400 only on a malformed value.
func (s *Server) parseDepositCount(c *gin.Context) (*uint32, bool) {
	raw := c.Query(depositCountParam)
	if raw == "" {
		return nil, true
	}
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		s.respondErrorMsg(c, http.StatusBadRequest, "invalid "+depositCountParam+": "+raw)
		return nil, false
	}
	dc := uint32(v)
	return &dc, true
}

func (s *Server) respondError(c *gin.Context, status int, err error) {
	s.respondErrorMsg(c, status, err.Error())
}

func (s *Server) respondErrorMsg(c *gin.Context, status int, msg string) {
	c.JSON(status, errorResponse{Error: msg})
}
