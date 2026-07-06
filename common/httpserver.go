package common

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

const httpServerShutdownTimeout = 5 * time.Second

// RoutesRegisterer can register HTTP routes on a Gin router.
type RoutesRegisterer interface {
	RegisterRoutes(gin.IRouter)
}

// HTTPServer is a shared Gin-based HTTP server. Multiple components register
// their routes on it before Start is called.
type HTTPServer struct {
	cfg    RESTConfig
	engine *gin.Engine
	logger Logger
}

// NewHTTPServer creates an HTTP server with gin.Recovery and logger middleware.
// A nil logger omits the request-logging middleware.
func NewHTTPServer(cfg RESTConfig, logger Logger) *HTTPServer {
	ginMode := os.Getenv("GIN_MODE")
	switch ginMode {
	case gin.DebugMode, gin.ReleaseMode, gin.TestMode:
		gin.SetMode(ginMode)
	default:
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.Use(gin.Recovery())
	if logger != nil {
		engine.Use(HTTPLoggerHandler(logger))
	}
	return &HTTPServer{cfg: cfg, engine: engine, logger: logger}
}

// Engine returns the Gin router so callers can register routes.
func (s *HTTPServer) Engine() *gin.Engine { return s.engine }

// Start binds the configured address and serves requests in the background.
// The listener is created synchronously, so a bind failure (e.g. port already
// in use) is returned immediately and the caller can abort startup. Once Start
// returns nil the server is accepting connections; it shuts down gracefully
// when ctx is done.
func (s *HTTPServer) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.Address())
	if err != nil {
		return fmt.Errorf("httpserver: failed to listen on %s: %w", s.cfg.Address(), err)
	}

	srv := &http.Server{
		Handler:      s.engine,
		ReadTimeout:  s.cfg.ReadTimeout.Duration,
		WriteTimeout: s.cfg.WriteTimeout.Duration,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpServerShutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			if s.logger != nil {
				s.logger.Errorf("httpserver on %s stopped unexpectedly: %v", s.cfg.Address(), err)
			}
		}
	}()

	return nil
}

// HTTPLoggerHandler returns a Gin middleware that logs HTTP requests using logger at DEBUG level.
func HTTPLoggerHandler(logger Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(startTime)
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
			startTime.Format("2006/01/02 - 15:04:05"),
			statusCode,
			latency,
			clientIP,
			method,
			path,
			errorMessage,
		)
	}
}
