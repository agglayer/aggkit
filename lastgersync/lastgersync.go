package lastgersync

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/sync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	reorgDetectorID = "lastGERSyncer"
)

type SyncMode string

const (
	Legacy         SyncMode = "Legacy"
	SovereignChain SyncMode = "SovereignChain"
)

// L1InfoTreeQuerier is abstraction for querying the L1InfoTree data
type L1InfoTreeQuerier interface {
	GetLastL1InfoTreeRoot(ctx context.Context) (treetypes.Root, error)
	GetInfoByIndex(ctx context.Context, index uint32) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetInfoByGlobalExitRoot(ger common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error)
}

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
	l2Client aggkittypes.BaseEthereumClienter,
	l2GERManagerAddr common.Address,
	l1InfoTreeSync L1InfoTreeQuerier,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	blockFinality aggkittypes.BlockNumberFinality,
	waitForNewBlocksPeriod time.Duration,
	downloadBufferSize int,
	requireStorageContentCompatibility bool,
	syncMode SyncMode,
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

	var downloader sync.Downloader

	switch syncMode {
	case Legacy:
		downloader, err = newDownloaderLegacy(
			l2Client, l2GERManagerAddr,
			l1InfoTreeSync, processor,
			rh, bf, waitForNewBlocksPeriod,
		)

	case SovereignChain:
		downloader, err = newDownloaderSovereign(
			l2Client, l2GERManagerAddr,
			l1InfoTreeSync, processor,
			rh, bf, waitForNewBlocksPeriod,
		)

	default:
		return nil, fmt.Errorf("unknown sync mode %s provided", syncMode)
	}

	if err != nil {
		return nil, err
	}
	compatibilityChecker := compatibility.NewCompatibilityCheck(
		requireStorageContentCompatibility,
		downloader.RuntimeData,
		processor)
	driver, err := sync.NewEVMDriver(rdL2, processor, downloader, reorgDetectorID,
		downloadBufferSize, rh, compatibilityChecker)
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
) (GlobalExitRootInfo, error) {
	return s.processor.GetFirstGERAfterL1InfoTreeIndex(ctx, atOrAfterL1InfoTreeIndex)
}

// GetInjectedGERsForRange retrieves all injected global exit roots within a specified block range.
// It returns a map where the keys are the global exit root hashes and the values are the
// corresponding GlobalExitRootInfo containing the L1 info tree index, global exit root and block number.
func (s *LastGERSync) GetInjectedGERsForRange(ctx context.Context,
	fromBlock, toBlock uint64) (map[common.Hash]GlobalExitRootInfo, error) {
	return s.processor.GetInjectedGERsForRange(ctx, fromBlock, toBlock)
}

// GetLastProcessedBlock returns the last processed block number
func (s *LastGERSync) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	return s.processor.GetLastProcessedBlock(ctx)
}
