// Package dvnsyncer watches LayerZero PacketSent and AggLayerDVN JobAssigned events
// on-chain and stores them into a local SQLite database for the dvnworker to consume.
package dvnsyncer

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/dvnsyncer/db"
	"github.com/agglayer/aggkit/log"
)

// Service is the DVN syncer service that watches on-chain LayerZero events and
// stores them for the dvnworker to process.
type Service struct {
	cfg    Config
	logger *log.Logger
	db     *db.DB
}

// New creates a new dvnsyncer Service with the given configuration.
// It runs database migrations and opens the SQLite database.
func New(cfg Config, logger *log.Logger) (*Service, error) {
	database, err := db.New(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("dvnsyncer: failed to open db: %w", err)
	}

	return &Service{
		cfg:    cfg,
		logger: logger,
		db:     database,
	}, nil
}

// Start begins syncing events from the configured chain. It blocks until ctx is
// cancelled and returns ctx.Err().
//
// TODO: W-3.2 reorg integration mirrors bridgesync pattern — subscribe to reorgdetector
// and call d.DeleteFromBlock(ctx, chainID, reorgFromBlock) on reorg notification.
func (s *Service) Start(ctx context.Context) error {
	s.logger.Info("dvnsyncer starting")
	// TODO: W-3.2 implement polling sync loop:
	//   1. Poll for new blocks up to tip-ConfirmationDepth
	//   2. Fetch logs for PacketSent (EndpointV2Addr) and JobAssigned (AggLayerDVNAddr)
	//   3. Decode logs via dvnsyncer/codec
	//   4. Insert into DB via s.db.InsertPacket / s.db.InsertJobAssigned
	//   5. On reorg detected: s.db.DeleteFromBlock(ctx, s.cfg.ChainID, reorgFromBlock)
	<-ctx.Done()
	return ctx.Err()
}

// Close releases any resources held by the Service.
func (s *Service) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// GetPacketByHash returns the PacketRecord for the given payloadHash on this service's chain.
func (s *Service) GetPacketByHash(ctx context.Context, payloadHash string) (*db.PacketRecord, error) {
	return s.db.GetPacketByHash(ctx, s.cfg.ChainID, payloadHash)
}

// ListPendingJobs returns JobAssignedRecords starting from sinceBlock with the given confirmation depth.
// Pass confirmations=0 to retrieve all jobs regardless of confirmation depth.
func (s *Service) ListPendingJobs(ctx context.Context, sinceBlock, confirmations uint64) ([]db.JobAssignedRecord, error) {
	return s.db.ListPendingJobs(ctx, s.cfg.ChainID, sinceBlock, confirmations)
}

// GetJobAssigned returns the JobAssignedRecord for the given payloadHash on this service's chain.
func (s *Service) GetJobAssigned(ctx context.Context, payloadHash string) (*db.JobAssignedRecord, error) {
	return s.db.GetJobAssigned(ctx, s.cfg.ChainID, payloadHash)
}
