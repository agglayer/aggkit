package proxy

import (
	"context"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
)

// RESTServer owns the shared REST/WS HTTP server of the proxy binary: every component
// registers its routes on it through Register before Start, and Start only binds the
// listener when at least one component registered routes
type RESTServer struct {
	cfg       aggkitcommon.RESTConfig
	logger    aggkitcommon.Logger
	server    *aggkitcommon.HTTPServer
	hasRoutes bool
}

// NewRESTServer creates the shared REST server. The listener is not bound until Start
func NewRESTServer(cfg aggkitcommon.RESTConfig, logger aggkitcommon.Logger) *RESTServer {
	return &RESTServer{
		cfg:    cfg,
		logger: logger,
		server: aggkitcommon.NewHTTPServer(cfg, logger),
	}
}

// Register registers a component's routes on the shared engine
func (s *RESTServer) Register(component aggkitcommon.RoutesRegisterer) {
	component.RegisterRoutes(s.server.Engine())
	s.hasRoutes = true
}

// Start binds the configured address and serves requests in the background, shutting down
// gracefully when ctx is done. It is a no-op if no component registered routes. A bind
// failure (e.g. port already in use) is returned so the caller can abort startup
func (s *RESTServer) Start(ctx context.Context) error {
	if !s.hasRoutes {
		s.logger.Warn("no routes registered, REST server not started")
		return nil
	}
	if err := s.server.Start(ctx); err != nil {
		return fmt.Errorf("failed to start REST server: %w", err)
	}
	s.logger.Infof("REST server listening on %s", s.cfg.Address())
	return nil
}
