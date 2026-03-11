package claimsync

import (
	"context"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgesync"
	claimsyncStorage "github.com/agglayer/aggkit/claimsync/storage"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	downloadBufferSize = 1000
	defaultDBTimeout   = 30 * time.Second
)

// ClaimSyncer is the interface for the claim syncer component used by aggsender.
type ClaimSyncer interface {
	Start(ctx context.Context)
}

// NewFromBridgeSync creates a ClaimSyncer backed by an existing BridgeSync that
// has an embedded claim processor. It returns nil if bs is nil.
func NewFromBridgeSync(bs *bridgesync.BridgeSync) ClaimSyncer {
	if bs == nil {
		return nil
	}
	return &bridgeSyncClaimSyncer{bs: bs}
}

type bridgeSyncClaimSyncer struct {
	bs *bridgesync.BridgeSync
}

func (b *bridgeSyncClaimSyncer) Start(_ context.Context) {}

// ClaimSync is the standalone implementation that independently processes claim events.
type ClaimSync struct {
	processor *processor
	driver    *sync.EVMDriver
}

// NewStandaloneClaimSync creates a standalone ClaimSync that indexes claim events from the bridge contract directly.
func NewStandaloneClaimSync(
	ctx context.Context,
	cfg bridgesync.Config,
	rd sync.ReorgDetector,
	ethClient aggkittypes.EthClienter,
	syncerID claimsynctypes.ClaimSyncerID,
) (*ClaimSync, error) {
	logger := log.WithFields("module", syncerID.String())
	return NewClaimSync(ctx, cfg, rd, ethClient, syncerID, logger)
}

// NewClaimSync creates a standalone ClaimSync that indexes claim events from the bridge contract directly.
func NewClaimSync(
	ctx context.Context,
	cfg bridgesync.Config,
	rd sync.ReorgDetector,
	ethClient aggkittypes.EthClienter,
	syncerID claimsynctypes.ClaimSyncerID,
	logger aggkitcommon.Logger,
) (*ClaimSync, error) {

	dbQueryTimeout := cfg.DBQueryTimeout.Duration
	if dbQueryTimeout == 0 {
		dbQueryTimeout = defaultDBTimeout
	}
	store, err := claimsyncStorage.NewStandalone(logger, cfg.DBPath, syncerID.String())
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create storage: %w", err)
	}

	proc, err := newProcessor(logger, store, dbQueryTimeout)
	if err != nil {
		return nil, err
	}

	agglayerBridgeContract, err := agglayerbridge.NewAgglayerbridge(cfg.BridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create AgglayerBridge binding: %w", err)
	}

	isSovereign, agglayerBridgeL2Contract, err := detectSovereignChain(ctx, cfg.BridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to detect chain type: %w", err)
	}

	appender, err := buildAppender(ctx, ethClient, proc, cfg.BridgeAddr,
		agglayerBridgeContract, agglayerBridgeL2Contract, isSovereign, logger)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to build appender: %w", err)
	}

	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: cfg.MaxRetryAttemptsAfterError,
		RetryAfterErrorPeriod:      cfg.RetryAfterErrorPeriod.Duration,
	}

	downloader, err := sync.NewEVMDownloader(
		syncerID.String(),
		sync.NewAdapterEthClientToMultidownloader(ethClient),
		cfg.SyncBlockChunkSize,
		cfg.BlockFinality,
		cfg.WaitForNewBlocksPeriod.Duration,
		appender,
		[]common.Address{cfg.BridgeAddr},
		rh,
		rd.GetFinalizedBlockType(),
		rd,
		syncerID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create EVMDownloader: %w", err)
	}

	lastBlock, err := proc.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("claimsync: get last processed block: %w", err)
	}
	if lastBlock < cfg.InitialBlockNum {
		header, err := ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(cfg.InitialBlockNum))
		if err != nil {
			return nil, fmt.Errorf("claimsync: get initial block %d: %w", cfg.InitialBlockNum, err)
		}
		if err := proc.ProcessBlock(ctx, sync.Block{Num: cfg.InitialBlockNum, Hash: header.Hash}); err != nil {
			return nil, fmt.Errorf("claimsync: process initial block %d: %w", cfg.InitialBlockNum, err)
		}
	}

	compatibilityChecker := compatibility.NewCompatibilityCheck(
		cfg.RequireStorageContentCompatibility,
		downloader.RuntimeData,
		proc,
	)

	driver, err := sync.NewEVMDriver(rd, proc, downloader, syncerID.String(), downloadBufferSize, rh, compatibilityChecker)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create EVMDriver: %w", err)
	}

	logger.Infof(
		"claimsync created: dbPath=%s initialBlock=%d blockFinality=%s bridgeAddr=%s sovereign=%t",
		cfg.DBPath, cfg.InitialBlockNum, cfg.BlockFinality.String(), cfg.BridgeAddr.String(), isSovereign,
	)

	return &ClaimSync{
		processor: proc,
		driver:    driver,
	}, nil
}

// Start starts the synchronization process.
func (c *ClaimSync) Start(ctx context.Context) {
	c.driver.Sync(ctx)
}
