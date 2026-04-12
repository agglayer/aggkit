package l2gersync

import (
	"context"
	"fmt"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	gethvm "github.com/ethereum/go-ethereum/core/vm"
)

const (
	reorgDetectorID = "l2GERSyncer"
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
	IsUpToDate(ctx context.Context, l1Client aggkittypes.BaseEthereumClienter) (bool, error)
}

// L2GERSync is responsible for managing GER synchronization.
type L2GERSync struct {
	driver    *sync.EVMDriver
	processor *processor
	cfg       Config
}

// New initializes and returns a new instance of L2GERSync
func New(
	ctx context.Context,
	cfg Config,
	rdL2 sync.ReorgDetector,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSync L1InfoTreeQuerier,
	l1Client aggkittypes.BaseEthereumClienter,
) (*L2GERSync, error) {
	if cfg.SyncBlockChunkSize == 0 {
		return nil, fmt.Errorf("syncBlockChunkSize must be greater than 0")
	}

	processor, err := newProcessor(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create processor: %w", err)
	}

	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      cfg.RetryAfterErrorPeriod.Duration,
		MaxRetryAttemptsAfterError: cfg.MaxRetryAttemptsAfterError,
	}

	syncMode, err := resolveSyncMode(ctx, cfg.GlobalExitRootL2Addr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve l2 ger syncer sync mode: %w", err)
	}

	var downloader sync.Downloader

	switch syncMode {
	case Legacy:
		downloader, err = newDownloaderLegacy(
			l2Client, cfg.GlobalExitRootL2Addr,
			l1InfoTreeSync, processor,
			rh, cfg.BlockFinality, cfg.WaitForNewBlocksPeriod.Duration,
		)

	case SovereignChain:
		downloader, err = newDownloaderSovereign(
			l2Client, cfg.GlobalExitRootL2Addr,
			l1InfoTreeSync, l1Client, cfg.GlobalExitRootL1Addr,
			rh, cfg.BlockFinality, cfg.WaitForNewBlocksPeriod.Duration,
			cfg.SyncBlockChunkSize,
		)

	default:
		return nil, fmt.Errorf("unknown sync mode %s provided", syncMode)
	}

	if err != nil {
		return nil, err
	}
	compatibilityChecker := compatibility.NewCompatibilityCheck(
		cfg.RequireStorageContentCompatibility,
		downloader.RuntimeData,
		processor)
	driver, err := sync.NewEVMDriver(rdL2, processor, downloader, reorgDetectorID,
		cfg.DownloadBufferSize, rh, compatibilityChecker)
	if err != nil {
		return nil, err
	}

	return &L2GERSync{
		driver:    driver,
		processor: processor,
		cfg:       cfg,
	}, nil
}

// resolveSyncMode determines the synchronization mode based on deployed provided l2 ger manager contract
func resolveSyncMode(ctx context.Context, address common.Address, backend bind.ContractBackend) (SyncMode, error) {
	// Try sovereign chain ger manager
	sovereignGERManager, err := agglayergerl2.NewAgglayergerl2(address, backend)
	if err == nil {
		updater, err := sovereignGERManager.GlobalExitRootUpdater(&bind.CallOpts{Context: ctx})
		if err == nil {
			log.Debugf("Detected: GlobalExitRootManagerL2SovereignChain, GlobalExitRootUpdater = %s", updater.Hex())
			return SovereignChain, nil
		}

		if !strings.Contains(err.Error(), gethvm.ErrExecutionReverted.Error()) {
			return "", fmt.Errorf("unexpected error when checking l2 ger manager (sovereign chain): %w", err)
		}
	}

	// Try with legacy ger manager
	legacyGERManager, err := agglayerger.NewAgglayerger(address, backend)
	if err == nil {
		bridgeAddr, err := legacyGERManager.BridgeAddress(&bind.CallOpts{Context: ctx})
		if err == nil {
			log.Debugf("Detected: PolygonZkEVMGlobalExitRootV2, BridgeAddress = %s", bridgeAddr)
			return Legacy, nil
		}
	}

	return "", fmt.Errorf("could not determine l2 ger sync mode based on the ger manager contract@%s", address.Hex())
}

// Start initiates the synchronization process.
func (s *L2GERSync) Start(ctx context.Context) {
	s.processor.log.Infof("starting l2gersync at block %d", s.cfg.InitialBlockNum)
	s.driver.Sync(ctx, &s.cfg.InitialBlockNum)
}

// GetFirstGERAfterL1InfoTreeIndex returns the first GER after a specified L1 info tree index
func (s *L2GERSync) GetFirstGERAfterL1InfoTreeIndex(
	ctx context.Context, atOrAfterL1InfoTreeIndex uint32,
) (GlobalExitRootInfo, error) {
	return s.processor.GetFirstGERAfterL1InfoTreeIndex(ctx, atOrAfterL1InfoTreeIndex)
}

// GetInjectedGERsForRange retrieves all injected global exit roots within a specified block range.
// It returns a map where the keys are the global exit root hashes and the values are the
// corresponding GlobalExitRootInfo containing the L1 info tree index, global exit root and block number.
func (s *L2GERSync) GetInjectedGERsForRange(ctx context.Context,
	fromBlock, toBlock uint64) (map[common.Hash]GlobalExitRootInfo, error) {
	return s.processor.GetInjectedGERsForRange(ctx, fromBlock, toBlock)
}

// GetLastProcessedBlock returns the last processed block number
func (s *L2GERSync) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	num, _, err := s.processor.GetLastProcessedBlock(ctx)
	return num, err
}

// GetRemoveGEREvents retrieves remove GER events from the database with optional filters
func (s *L2GERSync) GetRemoveGEREvents(
	ctx context.Context,
	globalExitRoot *common.Hash,
	limit uint32,
) ([]*RemoveGEREvent, error) {
	return s.processor.GetRemoveGEREvents(ctx, globalExitRoot, limit)
}
