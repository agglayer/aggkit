package claimsync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

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

// ClaimSync is the standalone implementation that independently processes claim events.
type ClaimSync struct {
	processor     *processor
	driver        *sync.EVMDriver
	reader        claimsynctypes.ClaimsReader
	ethClient     aggkittypes.EthClienter
	logger        aggkitcommon.Logger
	originNetwork uint32
	syncerID      claimsynctypes.ClaimSyncerID
	cfg           ConfigStandalone
}

// NewStandaloneClaimSync creates a standalone ClaimSync that indexes claim events from the bridge contract directly.
func NewStandaloneClaimSync(
	ctx context.Context,
	cfg ConfigStandalone,
	rd sync.ReorgDetector,
	ethClient aggkittypes.EthClienter,
	syncerID claimsynctypes.ClaimSyncerID,
	originNetwork uint32,
) (*ClaimSync, error) {
	logger := log.WithFields("module", syncerID.String())
	return NewClaimSync(ctx, cfg, rd, ethClient, originNetwork, syncerID, logger)
}

// NewClaimSync creates a standalone ClaimSync that indexes claim events from the bridge contract directly.
func NewClaimSync(
	ctx context.Context,
	cfg ConfigStandalone,
	rd sync.ReorgDetector,
	ethClient aggkittypes.EthClienter,
	originNetwork uint32,
	syncerID claimsynctypes.ClaimSyncerID,
	logger aggkitcommon.Logger,
) (*ClaimSync, error) {
	dbQueryTimeout := cfg.DBQueryTimeout.Duration
	if dbQueryTimeout == 0 {
		dbQueryTimeout = defaultDBTimeout
	}
	store, err := claimsyncStorage.NewStandalone(logger, cfg.DBPath, syncerID.String(), cfg.DBQueryTimeout.Duration)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create storage: %w", err)
	}

	proc := newProcessor(logger, store, dbQueryTimeout)

	deployment, err := resolveBridgeDeployment(ctx, cfg.BridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to detect chain type: %w", err)
	}
	if deployment.kind == Unknown {
		logger.Warnf("unable to determine bridge contract type at address %s", cfg.BridgeAddr.Hex())
	}

	appender, err := buildAppender(ctx, ethClient, proc, cfg.BridgeAddr, deployment, logger)
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
	// TODO: Remove
	// lastBlock, _, err := proc.GetLastProcessedBlock(ctx)
	// if err != nil {
	// 	return nil, fmt.Errorf("claimsync: get last processed block: %w", err)
	// }
	// if lastBlock < cfg.InitialBlockNum {
	// 	header, err := ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(cfg.InitialBlockNum))
	// 	if err != nil {
	// 		return nil, fmt.Errorf("claimsync: get initial block %d: %w", cfg.InitialBlockNum, err)
	// 	}
	// 	if err := proc.ProcessBlock(ctx, sync.Block{Num: cfg.InitialBlockNum, Hash: header.Hash}); err != nil {
	// 		return nil, fmt.Errorf("claimsync: process initial block %d: %w", cfg.InitialBlockNum, err)
	// 	}
	// }

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
		"claimsync created: dbPath=%s initialBlock=%d blockFinality=%s bridgeAddr=%s bridgeKind=%s",
		cfg.DBPath, cfg.InitialBlockNum, cfg.BlockFinality.String(), cfg.BridgeAddr.String(), deployment.kind.String(),
	)

	return &ClaimSync{
		processor:     proc,
		driver:        driver,
		reader:        store,
		ethClient:     ethClient,
		logger:        logger,
		originNetwork: originNetwork,
		syncerID:      syncerID,
		cfg:           cfg,
	}, nil
}

// Start starts the synchronization process.
func (c *ClaimSync) Start(ctx context.Context) {
	c.logger.Infof("starting claim synchronizer AutoStart: %s InitialBlock: %d",
		c.cfg.AutoStart.String(), c.cfg.InitialBlockNum)
	if *c.cfg.AutoStart.Resolved {
		c.driver.Sync(ctx, &c.cfg.InitialBlockNum)
	} else {
		c.driver.Sync(ctx, nil)
	}
}

func (c *ClaimSync) syncNextBlockInfinite(ctx context.Context, blockNumber uint64) {
	c.logger.Infof("autoStartDownloading: bootstrapping block %d", blockNumber)
	for {
		err := c.driver.SyncNextBlock(ctx, blockNumber)
		if err == nil || errors.Is(err, sync.ErrAlreadyBootstrapped) {
			return
		}
		c.logger.Warnf("autoStartDownloading: failed to process block %d: %v — retrying in %s",
			blockNumber, err, c.cfg.RetryAfterErrorPeriod.Duration)
		select {
		case <-ctx.Done():
			c.logger.Info("autoStartDownloading: context cancelled, stopping")
			return
		case <-time.After(c.cfg.RetryAfterErrorPeriod.Duration):
		}
	}
}

// SyncNextBlock downloads and processes blockNum as a bootstrap step.
// Returns sync.ErrAlreadyBootstrapped (ignorable) if a processed block already exists.
func (c *ClaimSync) SyncNextBlock(ctx context.Context, blockNum uint64) error {
	c.logger.Infof("SyncNextBlock: syncing block %d", blockNum)
	c.syncNextBlockInfinite(ctx, blockNum)
	return nil
}

// OriginNetwork returns the network ID of the origin chain

func (c *ClaimSync) OriginNetwork() uint32 {
	return c.originNetwork
}

func (c *ClaimSync) SetNextRequiredBlock(ctx context.Context, blockNumber uint64) error {
	lastBlock, found, err := c.processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return fmt.Errorf("claimsync: failed to get last processed block: %w", err)
	}
	if !found {
		c.logger.Infof("Starting to sync from block %d (no processed blocks found)", blockNumber)
		if err := c.driver.SyncNextBlock(ctx, blockNumber); err != nil {
			return fmt.Errorf("claimsync: failed to createStartingPoint: %w", err)
		}
		return nil
	}
	firstBlock, _, err := c.processor.GetFirstProcessedBlock(ctx)
	if err != nil {
		return fmt.Errorf("claimsync: failed to get first processed block: %w", err)
	}
	if blockNumber < firstBlock {
		return fmt.Errorf("claimsync: cannot set next required block to %d, "+
			"it must be greater or equal than the first block in DB (%d)",
			blockNumber, firstBlock)
	}

	c.logger.Infof("Cannot set next required block to %d because is running, but is included. "+
		" Processed blocks [%d - %d]", blockNumber, firstBlock, lastBlock)

	return nil
}

func (c *ClaimSync) GetLastProcessedBlock(ctx context.Context) (uint64, bool, error) {
	return c.reader.GetLastProcessedBlock(ctx, nil)
}

func (c *ClaimSync) GetFirstProcessedBlock(ctx context.Context) (uint64, bool, error) {
	return c.reader.GetFirstProcessedBlock(ctx, nil)
}

func (c *ClaimSync) GetClaims(ctx context.Context, fromBlock, toBlock uint64) ([]claimsynctypes.Claim, error) {
	return c.reader.GetClaims(ctx, nil, fromBlock, toBlock)
}

func (c *ClaimSync) GetClaimsByGlobalIndex(ctx context.Context, globalIndex *big.Int) ([]claimsynctypes.Claim, error) {
	return c.reader.GetClaimsByGlobalIndex(ctx, nil, globalIndex)
}

func (c *ClaimSync) GetClaimsPaged(ctx context.Context, page, pageSize uint32,
	networkIDs []uint32, globalIndex *big.Int) ([]*Claim, int, error) {
	return c.reader.GetClaimsPaged(ctx, page, pageSize, networkIDs, globalIndex)
}
func (c *ClaimSync) GetUnsetClaimsPaged(ctx context.Context, page, pageSize uint32,
	globalIndex *big.Int) ([]*UnsetClaim, int, error) {
	return c.reader.GetUnsetClaimsPaged(ctx, page, pageSize, globalIndex)
}
func (c *ClaimSync) GetSetClaimsPaged(ctx context.Context, page, pageSize uint32,
	globalIndex *big.Int) ([]*SetClaim, int, error) {
	return c.reader.GetSetClaimsPaged(ctx, page, pageSize, globalIndex)
}

func (c *ClaimSync) GetClaimsByGER(ctx context.Context, globalExitRoot common.Hash) ([]*Claim, error) {
	return c.reader.GetClaimsByGER(ctx, nil, globalExitRoot)
}
