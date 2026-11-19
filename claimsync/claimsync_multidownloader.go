package claimsync

import (
	"context"
	"fmt"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	mdrsync "github.com/agglayer/aggkit/multidownloader/sync"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// MultidownloaderBackend is the multidownloader that a claim syncer is synced through: it
// downloads the logs and the block headers, detects the reorgs and tracks the finality of the blocks.
type MultidownloaderBackend interface {
	mdrsynctypes.MultidownloaderInterface
	// RegisterSyncer registers the syncer, it must be called before the multidownloader is initialized
	RegisterSyncer(data aggkittypes.SyncerConfig) error
}

// NewStandaloneClaimSyncWithDownloader creates a standalone ClaimSync that is synced through the
// shared multidownloader: it downloads the logs and the block headers from it and relies on it to
// detect the reorgs and to track the finality of the blocks. The legacy reorg detector is not used.
// Claims do not need transaction data, so no transaction provider is required.
//
// It requires cfg.Downloader to select the multidownloader, backend to be non-nil and the syncer to
// be the L1 claim syncer; the multidownloader is not supported for the L2 claims. The syncer is
// registered into the backend synchronously, before the backend is initialized/started.
func NewStandaloneClaimSyncWithDownloader(
	ctx context.Context,
	cfg ConfigStandalone,
	ethClient aggkittypes.EthClienter,
	syncerID claimsynctypes.ClaimSyncerID,
	originNetwork uint32,
	backend MultidownloaderBackend,
) (*ClaimSync, error) {
	if err := cfg.Downloader.Validate(); err != nil {
		return nil, fmt.Errorf("claimsync: invalid Downloader configuration: %w", err)
	}
	if !cfg.Downloader.IsMultidownloader() {
		return nil, fmt.Errorf("claimsync: NewStandaloneClaimSyncWithDownloader requires Downloader=%s, got %q",
			cfgtypes.Multidownloader, cfg.Downloader)
	}
	if backend == nil {
		return nil, fmt.Errorf("claimsync: %w: backend is nil", ErrMultidownloaderDepsMissing)
	}
	if syncerID != claimsynctypes.L1ClaimSyncer {
		return nil, fmt.Errorf("claimsync: multidownloader is only supported for the L1 claim syncer, got %s", syncerID)
	}
	logger := log.WithFields("module", syncerID.String())
	return newClaimSyncMultidownloader(ctx, cfg, ethClient, originNetwork, syncerID, logger, backend)
}

func newClaimSyncMultidownloader(
	ctx context.Context,
	cfg ConfigStandalone,
	ethClient aggkittypes.EthClienter,
	originNetwork uint32,
	syncerID claimsynctypes.ClaimSyncerID,
	logger aggkitcommon.Logger,
	backend MultidownloaderBackend,
) (*ClaimSync, error) {
	store, proc, err := newStorageAndProcessor(cfg, syncerID, logger)
	if err != nil {
		return nil, err
	}

	// Register the syncer in the multidownloader synchronously, before the backend is initialized.
	// Claims never need transaction data, so no transaction requirement is registered.
	syncerConfig := aggkittypes.SyncerConfig{
		SyncerID:          syncerID.String(),
		ContractAddresses: []common.Address{cfg.BridgeAddr},
		FromBlock:         cfg.InitialBlockNum,
		ToBlock:           cfg.BlockFinality,
	}
	if err := backend.RegisterSyncer(syncerConfig); err != nil {
		return nil, fmt.Errorf("claimsync: failed to register %s in multidownloader: %w", syncerID, err)
	}
	logger.Infof("claimsync: registered %s in multidownloader: blockFinality=%s fromBlock=%d",
		syncerID, cfg.BlockFinality.String(), cfg.InitialBlockNum)

	deployment, err := resolveBridgeDeployment(ctx, cfg.BridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to detect chain type: %w", err)
	}
	if deployment.kind == Unknown {
		logger.Warnf("unable to determine bridge contract type at address %s", cfg.BridgeAddr.Hex())
	}

	// The bounded parallel root-trace prefetch is enabled for the multidownloader-backed (L1) claim
	// syncer: the ordinary/pre-Etrog handlers resolve their root call from the prefetched snapshot.
	resolver := NewTraceResolver(ethClient, cfg.BridgeAddr, logger, ClaimTracePrefetchConcurrency)
	appender, contextualAppender, err := buildAppenderWithResolver(ethClient, cfg.BridgeAddr, deployment, logger, resolver)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to build appender: %w", err)
	}

	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: cfg.MaxRetryAttemptsAfterError,
		RetryAfterErrorPeriod:      cfg.RetryAfterErrorPeriod.Duration,
	}

	downloader := mdrsync.NewEVMDownloader(
		backend,
		logger,
		rh,
		appender,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.WaitForNewBlocksPeriod.Duration,
	)
	// The handlers that trace the claim transaction run with the active download context
	downloader.SetContextualLogAppenders(contextualAppender)
	downloader.SetLogsHook(NewPreferDetailedClaimLogsHook(logger))
	// The trace-prefetch enricher is installed after the hook so the prefetch runs on the logs the
	// hook already filtered (a ClaimEvent superseded by a DetailedClaimEvent is dropped before an
	// unnecessary/failing trace is requested). A downloader that does not support the hook fails
	// construction instead of starting a syncer whose handlers can never resolve a prefetched trace.
	if err := installClaimLogsEnricher(downloader, resolver); err != nil {
		return nil, fmt.Errorf("claimsync: failed to install logs enricher: %w", err)
	}

	addresses := []common.Address{cfg.BridgeAddr}
	compatibilityChecker := compatibility.NewCompatibilityCheck(
		cfg.RequireStorageContentCompatibility,
		func(ctx context.Context) (sync.RuntimeData, error) {
			chainID, err := downloader.ChainID(ctx)
			if err != nil {
				return sync.RuntimeData{}, err
			}
			return sync.RuntimeData{ChainID: chainID, Addresses: addresses}, nil
		},
		proc,
	)

	// The processor commits the downloaded blocks atomically through the claim processor's batch,
	// bounded to sync.MaxBatchBlocks, and the subscribers are notified after the single commit.
	blockProcessor := mdrsync.NewBlockByBlockProcessorWithBatch(proc,
		func(ctx context.Context, blockNum uint64) (common.Hash, bool, error) {
			return store.GetBlockHash(ctx, nil, blockNum)
		},
		proc.processBlocks,
	)
	driver := mdrsync.NewEVMDriver(logger, blockProcessor, downloader, syncerConfig,
		cfg.SyncBlockChunkSize, rh, compatibilityChecker)

	logger.Infof(
		"claimsync created through the multidownloader: dbPath=%s initialBlock=%d blockFinality=%s bridgeAddr=%s "+
			"bridgeKind=%s tracePrefetch=on batchedPersistence=on",
		cfg.DBPath, cfg.InitialBlockNum, cfg.BlockFinality.String(), cfg.BridgeAddr.String(), deployment.kind.String(),
	)

	return &ClaimSync{
		processor:       proc,
		driver:          driver,
		reader:          store,
		multidownloader: backend,
		ethClient:       ethClient,
		logger:          logger,
		originNetwork:   originNetwork,
		syncerID:        syncerID,
		cfg:             cfg,
	}, nil
}
