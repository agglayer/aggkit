package dvnsyncer

import (
	"context"

	"github.com/agglayer/aggkit/log"
)

// Service is the DVN syncer service that watches on-chain LayerZero events and
// stores them for the dvnworker to process.
type Service struct {
	cfg    Config
	logger *log.Logger
}

// New creates a new dvnsyncer Service with the given configuration.
func New(cfg Config, logger *log.Logger) (*Service, error) {
	return &Service{
		cfg:    cfg,
		logger: logger,
	}, nil
}

// Start begins syncing events from the configured chain. It blocks until ctx is
// cancelled and returns ctx.Err().
func (s *Service) Start(ctx context.Context) error {
	s.logger.Info("dvnsyncer starting")
	<-ctx.Done()
	return ctx.Err()
}

// Close releases any resources held by the Service.
func (s *Service) Close() error {
	return nil
}
