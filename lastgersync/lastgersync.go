package lastgersync

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/sync"
	"github.com/ethereum/go-ethereum/common"
)

const (
	reorgDetectorID = "lastGERSync"
)

// LastGERSync is responsible for managing GER synchronization.
type LastGERSync struct {
	driver    *sync.EVMDriver
	processor *processor
}

// New initializes and returns a new instance of LastGERSync
func New(
	ctx context.Context,
	dbPath string,
	rdL2 sync.ReorgDetector,
	l2Client EthClienter,
	globalExitRootL2 common.Address,
	l1InfoTreesync *l1infotreesync.L1InfoTreeSync,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	blockFinality etherman.BlockNumberFinality,
	waitForNewBlocksPeriod time.Duration,
	downloadBufferSize int,
	requireStorageContentCompatibility bool,
) (*LastGERSync, error) {
	processor, err := newProcessor(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create processor: %w", err)
	}

	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      retryAfterErrorPeriod,
		MaxRetryAttemptsAfterError: maxRetryAttemptsAfterError,
	}
	bf, err := blockFinality.ToBlockNum()
	if err != nil {
		return nil, err
	}
	downloader, err := newDownloader(
		l2Client,
		globalExitRootL2,
		l1InfoTreesync,
		processor,
		rh,
		bf,
		waitForNewBlocksPeriod,
	)
	if err != nil {
		return nil, err
	}

	driver, err := sync.NewEVMDriver(rdL2, processor, downloader, reorgDetectorID,
		downloadBufferSize, rh, requireStorageContentCompatibility)
	if err != nil {
		return nil, err
	}

	return &LastGERSync{
		driver:    driver,
		processor: processor,
	}, nil
}

// Start initiates the synchronization process.
func (s *LastGERSync) Start(ctx context.Context) error {
	s.driver.Sync(ctx)
	return nil
}

// GetFirstGERAfterL1InfoTreeIndex returns the first GER after a specified L1 info tree index
func (s *LastGERSync) GetFirstGERAfterL1InfoTreeIndex(
	ctx context.Context, atOrAfterL1InfoTreeIndex uint32,
) (Event, error) {
	return s.processor.GetFirstGERAfterL1InfoTreeIndex(ctx, atOrAfterL1InfoTreeIndex)
}

// GetLastProcessedBlock returns the last processed block number
func (s *LastGERSync) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	return s.processor.GetLastProcessedBlock(ctx)
}
