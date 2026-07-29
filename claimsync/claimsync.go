package claimsync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	claimsyncStorage "github.com/agglayer/aggkit/claimsync/storage"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
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

	appender, err := buildAppender(ethClient, cfg.BridgeAddr, deployment, logger)
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
		cfg.BlockFinality,
		rd,
		syncerID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("claimsync: failed to create EVMDownloader: %w", err)
	}
	downloader.SetLogsHook(NewPreferDetailedClaimLogsHook(logger))

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
func (c *ClaimSync) SyncNextBlock(ctx context.Context, blockNum uint64) error {
	c.logger.Infof("SyncNextBlock: syncing block %d", blockNum)
	c.syncNextBlockInfinite(ctx, blockNum)
	return nil
}

// OriginNetwork returns the network ID of the origin chain.
func (c *ClaimSync) OriginNetwork() uint32 {
	return c.originNetwork
}

func (c *ClaimSync) SetNextRequiredBlock(ctx context.Context, blockNumber uint64) error {
	if blockNumber < c.cfg.InitialBlockNum {
		c.logger.Infof("SetNextRequiredBlock: requested block %d is below InitialBlockNum %d, capping to %d",
			blockNumber, c.cfg.InitialBlockNum, c.cfg.InitialBlockNum)
		blockNumber = c.cfg.InitialBlockNum
	}
	lastBlock, found, err := c.processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return fmt.Errorf("claimsync: failed to get last processed block: %w", err)
	}
	if !found {
		c.logger.Infof("Starting to sync from block %d (no processed blocks found)", blockNumber)
		if err := c.driver.SyncNextBlock(ctx, blockNumber); err != nil {
			return fmt.Errorf("claimsync: failed to create starting point: %w", err)
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

	c.logger.Infof("Syncer is already running; block %d is within the processed range [%d - %d], no action needed",
		blockNumber, firstBlock, lastBlock)

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

// GetLatestBlockNumByGlobalIndexFromRPC scans claim event logs on-chain backwards from toBlock to 0
// and returns the block number of the most recent log whose GlobalIndex matches.
// If toBlock is nil, the finality from the configuration (cfg.BlockFinality) is used.
// The scan is split into chunks if the RPC reports a max-range limit; the chunk size is taken
// from the error message the first time a full-range call fails.
// The bool return value is false when no matching log is found (no error in that case).
func (c *ClaimSync) GetLatestBlockNumByGlobalIndexFromRPC(
	ctx context.Context, globalIndex *big.Int, toBlock *aggkittypes.BlockNumberFinality) (uint64, bool, error) {
	if toBlock == nil {
		toBlock = &c.cfg.BlockFinality
	}
	toBlockNum, err := toBlock.BlockNumber(ctx, c.ethClient)
	if err != nil {
		return 0, false, fmt.Errorf("claimsync: failed to resolve toBlock: %w", err)
	}

	agglayerBridgeContract, err := agglayerbridge.NewAgglayerbridge(c.cfg.BridgeAddr, c.ethClient)
	if err != nil {
		return 0, false, fmt.Errorf("claimsync: failed to create AgglayerBridge binding: %w", err)
	}
	agglayerBridgeL2Contract, err := agglayerbridgel2.NewAgglayerbridgel2(c.cfg.BridgeAddr, c.ethClient)
	if err != nil {
		return 0, false, fmt.Errorf("claimsync: failed to create AgglayerBridgeL2 binding: %w", err)
	}
	legacyBridgeContract, err := polygonzkevmbridge.NewPolygonzkevmbridge(c.cfg.BridgeAddr, c.ethClient)
	if err != nil {
		return 0, false, fmt.Errorf("claimsync: failed to create PolygonZkEVMBridge binding: %w", err)
	}

	// scanRange fetches logs for [from, to] and returns the block number of the matching log,
	// or 0/false when no match is found in that range.
	scanRange := func(from, to uint64) (uint64, bool, error) {
		query := ethereum.FilterQuery{
			FromBlock: new(big.Int).SetUint64(from),
			ToBlock:   new(big.Int).SetUint64(to),
			Addresses: []common.Address{c.cfg.BridgeAddr},
			Topics: [][]common.Hash{{
				claimEventSignaturePreEtrog,
				claimEventSignature,
				detailedClaimEventSignature,
			}},
		}
		logs, err := c.ethClient.FilterLogs(ctx, query)
		if err != nil {
			return 0, false, err
		}
		// logs are returned in ascending block order; iterate in reverse to return the most recent match
		for i := len(logs) - 1; i >= 0; i-- {
			l := logs[i]
			if len(l.Topics) == 0 {
				continue
			}
			switch l.Topics[0] {
			case claimEventSignaturePreEtrog:
				event, err := legacyBridgeContract.ParseClaimEvent(l)
				if err != nil {
					c.logger.Warnf("claimsync: failed to parse pre-Etrog ClaimEvent at block %d: %v", l.BlockNumber, err)
					continue
				}
				if new(big.Int).SetUint64(uint64(event.Index)).Cmp(globalIndex) == 0 {
					return l.BlockNumber, true, nil
				}
			case claimEventSignature:
				event, err := agglayerBridgeContract.ParseClaimEvent(l)
				if err != nil {
					c.logger.Warnf("claimsync: failed to parse ClaimEvent at block %d: %v", l.BlockNumber, err)
					continue
				}
				if event.GlobalIndex.Cmp(globalIndex) == 0 {
					return l.BlockNumber, true, nil
				}
			case detailedClaimEventSignature:
				event, err := agglayerBridgeL2Contract.ParseDetailedClaimEvent(l)
				if err != nil {
					c.logger.Warnf("claimsync: failed to parse DetailedClaimEvent at block %d: %v", l.BlockNumber, err)
					continue
				}
				if event.GlobalIndex.Cmp(globalIndex) == 0 {
					return l.BlockNumber, true, nil
				}
			}
		}
		return 0, false, nil
	}

	// Probe the full range to either get the result directly or discover the RPC chunk limit.
	blockNum, found, err := scanRange(0, toBlockNum)
	if err == nil {
		return blockNum, found, nil
	}

	chunkSize, isMaxRangeErr := aggkitcommon.ParseMaxRangeFromError(err.Error())
	if !isMaxRangeErr {
		return 0, false, fmt.Errorf("claimsync: FilterLogs error for globalIndex %s [0, %d]: %w",
			globalIndex.String(), toBlockNum, err)
	}

	// Scan backwards in chunks of chunkSize, returning on the first match found.
	current := toBlockNum
	for {
		chunkFrom := uint64(0)
		if current >= chunkSize {
			chunkFrom = current - chunkSize + 1
		}

		c.logger.Debugf("claimsync: scanning RPC logs for globalIndex %s in chunk [%d, %d]",
			globalIndex.String(), chunkFrom, current)

		blockNum, found, err := scanRange(chunkFrom, current)
		if err != nil {
			return 0, false, fmt.Errorf("claimsync: FilterLogs error for globalIndex %s [%d, %d]: %w",
				globalIndex.String(), chunkFrom, current, err)
		}
		if found {
			return blockNum, true, nil
		}

		if chunkFrom == 0 {
			break
		}
		current = chunkFrom - 1
	}

	return 0, false, nil
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
