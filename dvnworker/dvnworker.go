package dvnworker

import (
	"context"

	"github.com/agglayer/aggkit/dvnworker/correlator"
	"github.com/agglayer/aggkit/log"
)

// Service is the DVN worker service that correlates synced LayerZero events with
// AggLayer settlements and submits verification transactions on the destination chain.
type Service struct {
	cfg        Config
	logger     *log.Logger
	correlator *correlator.Correlator
}

// New creates a new dvnworker Service with the given configuration and route.
// The correlator is wired in here and will be used in W-3.4+ to validate jobs.
func New(cfg Config, route correlator.RouteConfig, logger *log.Logger) (*Service, error) {
	c := correlator.New(route, logger)
	return &Service{
		cfg:        cfg,
		logger:     logger,
		correlator: c,
	}, nil
}

// Start begins processing DVN jobs. It blocks until ctx is cancelled and returns ctx.Err().
func (s *Service) Start(ctx context.Context) error {
	s.logger.Info("dvnworker starting", "correlator", "ready")
	<-ctx.Done()
	return ctx.Err()
}

// Close releases any resources held by the Service.
func (s *Service) Close() error {
	return nil
}
