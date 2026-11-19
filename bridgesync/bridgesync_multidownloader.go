package bridgesync

import (
	"context"
	"fmt"
	"math"

	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	mdrsync "github.com/agglayer/aggkit/multidownloader/sync"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// MultidownloaderBackend is the multidownloader that a bridge syncer is synced through: it
// downloads the logs and the block headers, detects the reorgs and tracks the finality of the blocks.
type MultidownloaderBackend interface {
	mdrsynctypes.MultidownloaderInterface
	// RegisterSyncer registers the syncer, it must be called before the multidownloader is initialized
	RegisterSyncer(data aggkittypes.SyncerConfig) error
	// GetLastReorgData returns the most recent reorg detected, or nil if there is none
	GetLastReorgData(ctx context.Context) (*mdrtypes.ReorgData, error)
}

// NewL1WithDownloader creates an L1 bridge syncer that is synced through the shared multidownloader:
// it downloads the logs and the block headers from it, relies on it to detect the reorgs and to
// track the finality of the blocks, and resolves the bridge transactions through the shared
// transaction provider. The legacy reorg detector is not used.
//
// It requires cfg.Downloader to select the multidownloader and both backend and provider to be
// non-nil; there is no silent fallback to direct RPC. The syncer is registered into the backend
// synchronously, before the backend is initialized/started, so the backend knows which contract,
// block range and (optional) transaction topics to download.
func NewL1WithDownloader(
	ctx context.Context,
	cfg Config,
	ethClient aggkittypes.EthClienter,
	originNetwork uint32,
	backend MultidownloaderBackend,
	provider aggkittypes.TransactionProvider,
) (*BridgeSync, error) {
	if err := cfg.Downloader.Validate(); err != nil {
		return nil, fmt.Errorf("bridgesync: invalid Downloader configuration: %w", err)
	}
	if !cfg.Downloader.IsMultidownloader() {
		return nil, fmt.Errorf("bridgesync: NewL1WithDownloader requires Downloader=%s in config, got %q",
			cfgtypes.Multidownloader, cfg.Downloader)
	}
	if backend == nil {
		return nil, fmt.Errorf("bridgesync: %w: backend is nil", ErrMultidownloaderDepsMissing)
	}
	if provider == nil {
		return nil, fmt.Errorf("bridgesync: %w: transaction provider is nil", ErrMultidownloaderDepsMissing)
	}
	syncFromInBridges := cfg.SyncFromInBridges.Resolved != nil && *cfg.SyncFromInBridges.Resolved
	return newBridgeSyncMultidownloader(ctx, cfg, ethClient, originNetwork, syncFromInBridges, backend, provider)
}

func newBridgeSyncMultidownloader(
	ctx context.Context,
	cfg Config,
	ethClient aggkittypes.EthClienter,
	networkID uint32,
	syncFromInBridges bool,
	backend MultidownloaderBackend,
	provider aggkittypes.TransactionProvider,
) (*BridgeSync, error) {
	syncerID := L1BridgeSyncer
	logger := log.WithFields("module", syncerID.String())
	logger.Infof("Bridge sync %s through the multidownloader, syncFromInBridges: %t",
		syncerID.String(), syncFromInBridges)

	agglayerBridge, processor, err := newBridgeContractAndProcessor(
		cfg, ethClient, syncerID, bridgesynctypes.EmptyLER, logger)
	if err != nil {
		return nil, err
	}

	// Register the syncer in the multidownloader synchronously, before the backend is initialized.
	//
	// The *log* range (FromBlock) stays at cfg.InitialBlockNum on purpose: it is the historical
	// coverage the shared address needs, and it is what the shared downloader starts downloading
	// from for this address. The bridge backfill resolves old records, so logs older than the
	// consumer's committed checkpoint L must remain covered.
	//
	// The *transaction* requirement is a separate, future-only concern: its FromBlock is the first
	// not-yet-processed block (L+1) so only future transactions are fetched. The log frontier and
	// the transaction frontier are deliberately different.
	txFromBlock, err := computeTransactionFromBlock(ctx, processor, cfg.InitialBlockNum)
	if err != nil {
		return nil, err
	}
	syncerConfig := aggkittypes.SyncerConfig{
		SyncerID:          syncerID.String(),
		ContractAddresses: []common.Address{cfg.BridgeAddr},
		FromBlock:         cfg.InitialBlockNum,
		ToBlock:           cfg.BlockFinality,
		TransactionRequirements: aggkittypes.TransactionRequirement{
			Enabled:   true,
			FromBlock: txFromBlock,
			Topic0:    []common.Hash{bridgeEventSignature},
		},
	}
	if err := backend.RegisterSyncer(syncerConfig); err != nil {
		return nil, fmt.Errorf("failed to register %s in multidownloader: %w", syncerID.String(), err)
	}
	logger.Infof("registered %s in multidownloader: blockFinality=%s fromBlock=%d txFromBlock=%d",
		syncerID.String(), cfg.BlockFinality.String(), cfg.InitialBlockNum, txFromBlock)

	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: cfg.MaxRetryAttemptsAfterError,
		RetryAfterErrorPeriod:      cfg.RetryAfterErrorPeriod.Duration,
	}

	bridgeDeployment, err := resolveBridgeDeployment(ctx, cfg.BridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve bridge deployment. Reason: %w", err)
	}

	// The transactions are resolved through the shared provider instead of a direct
	// eth_getTransactionByHash
	txResolver := &providerTxnResolver{provider: provider}
	appender, err := buildAppender(ctx, ethClient, cfg.BridgeAddr, syncFromInBridges, bridgeDeployment, logger, txResolver)
	if err != nil {
		return nil, err
	}
	downloader := mdrsync.NewEVMDownloader(
		backend,
		logger,
		rh,
		appender,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.WaitForNewBlocksPeriod.Duration,
	)
	// The Bridge event is built by a context-aware appender, so the shared provider flight is
	// resolved with the active download context, and the transaction cache is warmed with the same
	// context before the events are built.
	downloader.SetContextualLogAppenders(buildContextualAppender(
		cfg.BridgeAddr, ethClient, syncFromInBridges, bridgeDeployment, logger, txResolver))
	downloader.SetLogsEnricher(bridgeLogsEnricher(provider))

	compatibilityChecker := compatibility.NewCompatibilityCheck(
		cfg.RequireStorageContentCompatibility,
		func(ctx context.Context) (BridgeSyncRuntimeData, error) {
			chainID, err := downloader.ChainID(ctx)
			if err != nil {
				return BridgeSyncRuntimeData{}, fmt.Errorf("failed to get runtime data: %w", err)
			}
			ver := CurrentDBVersion
			return BridgeSyncRuntimeData{
				ChainID:           chainID,
				Addresses:         []common.Address{cfg.BridgeAddr},
				DBVersion:         &ver,
				SyncFromInBridges: &syncFromInBridges,
			}, nil
		},
		processor)

	// The processor stores the blocks one by one and the post-commit subscriptions are preserved
	blockProcessor := mdrsync.NewBlockByBlockProcessor(processor, processor.getBlockHash)
	reorgsFinality := backend.Finality()
	driver := mdrsync.NewEVMDriver(logger, blockProcessor, downloader, syncerConfig,
		cfg.SyncBlockChunkSize, rh, compatibilityChecker)

	logger.Infof(
		"%s created:\n"+
			"  dbPath: %s\n"+
			"  initialBlock: %d\n"+
			"  blockFinality: %s\n"+
			"  bridgeAddr: %s\n"+
			"  maxRetryAttemptsAfterError: %d\n"+
			"  retryAfterErrorPeriod: %s\n"+
			"  syncBlockChunkSize: %d\n"+
			"  downloader: multidownloader (reorgs finality: %s)\n"+
			"  waitForNewBlocksPeriod: %s\n"+
			"  syncFromInBridges: %t",
		syncerID,
		cfg.DBPath,
		cfg.InitialBlockNum,
		cfg.BlockFinality.String(),
		cfg.BridgeAddr.String(),
		cfg.MaxRetryAttemptsAfterError,
		cfg.RetryAfterErrorPeriod.String(),
		cfg.SyncBlockChunkSize,
		reorgsFinality.String(),
		cfg.WaitForNewBlocksPeriod.String(),
		syncFromInBridges,
	)

	return &BridgeSync{
		processor:       processor,
		driver:          driver,
		blockSubscriber: blockProcessor,
		originNetwork:   networkID,
		multidownloader: backend,
		ethClient:       ethClient,
		agglayerBridge:  agglayerBridge,
		cfg:             cfg,
	}, nil
}

// getLastReorgEventFromMultidownloader returns the last reorg that the multidownloader detected.
// If there is none it returns an empty event, as the legacy reorg detector does.
func (s *BridgeSync) getLastReorgEventFromMultidownloader(ctx context.Context) (*LastReorg, error) {
	reorgData, err := s.multidownloader.GetLastReorgData(ctx)
	if err != nil {
		s.processor.log.Errorf("failed to get last reorg event from the multidownloader: %v", err)
		return nil, err
	}
	if reorgData == nil {
		return &LastReorg{}, nil
	}
	if reorgData.DetectedTimestamp > math.MaxInt64 {
		return nil, fmt.Errorf("the detection timestamp of the reorg %d overflows: %d",
			reorgData.ReorgID, reorgData.DetectedTimestamp)
	}
	return &LastReorg{
		DetectedAt: int64(reorgData.DetectedTimestamp),
		FromBlock:  reorgData.BlockRangeAffected.FromBlock,
		ToBlock:    reorgData.BlockRangeAffected.ToBlock,
	}, nil
}

// lastProcessedBlockReader exposes the processor checkpoint needed to compute the transaction frontier.
// The concrete *processor satisfies it; tests can substitute lightweight fakes to exercise checkpoint
// values a real storage driver would reject (for example block numbers with the high bit set).
type lastProcessedBlockReader interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, bool, error)
}

// computeTransactionFromBlock returns the first block for which the multidownloader must fetch the
// transactions of the synced bridge logs. The processor storage is committed and inclusive up to the
// last processed block L, which is materialized processed history: those blocks and their transactions
// already belong to the synced state. Only the blocks after L are future-only, so L+1 is the first
// block whose transactions still have to be fetched. This does not mean the multidownloader transaction
// cache already holds the history up to L: switching to that backend does not retroactively enrich the
// transactions of already processed blocks, so the requirement must not start before L+1. When no block
// has been processed yet (fresh database) the configured InitialBlockNum is used as the starting point.
func computeTransactionFromBlock(
	ctx context.Context, proc lastProcessedBlockReader, initialBlockNum uint64,
) (uint64, error) {
	lastProcessed, found, err := proc.GetLastProcessedBlock(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to read last processed block for transaction requirement: %w", err)
	}
	if !found {
		return initialBlockNum, nil
	}
	if lastProcessed == math.MaxUint64 {
		return 0, fmt.Errorf("last processed block %d overflows the transaction requirement from block",
			lastProcessed)
	}
	return lastProcessed + 1, nil
}
