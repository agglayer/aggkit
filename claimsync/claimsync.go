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

	// defaultRPCScanBlockChunkSize is the chunk size used by GetLatestBlockNumByGlobalIndexFromRPC
	// when cfg.SyncBlockChunkSize is unset (zero). The scan never issues an *unbounded* call
	// spanning the whole [0, toBlockNum] range -- each call spans at most the configured chunk
	// size -- since on a long-lived chain that range can be millions of blocks and would make the
	// scan take tens of minutes. A short chain whose whole range fits within one chunk is still
	// covered by exactly one bounded call, which is not the same thing as an unbounded probe.
	defaultRPCScanBlockChunkSize = 10000

	// maxRPCScanDuration bounds the *total* wall-clock time GetLatestBlockNumByGlobalIndexFromRPC
	// may spend scanning chunks backwards, on top of each individual FilterLogs call already being
	// bounded by sync.DefaultFilterLogsTimeout (2 minutes). Without an overall bound, an unresolved
	// range on a long-lived chain (millions of blocks at the default 10000-block chunk size can
	// need hundreds to thousands of chunks) has no limit on the *number* of sequential chunk calls:
	// one scan attempt could run for hours even though no single call ever hangs, which undermines
	// the very "never spin forever" goal that the caller's bounded retry budget
	// (maxIBERPCLookupFailures, in aggsender/query/initial_block_to_claimsync_setter.go) exists to
	// guarantee.
	//
	// 5 minutes is a deliberate trade-off. It is generous enough that a scan spanning hundreds of
	// chunks against a healthy RPC (each chunk typically resolving in well under a second) is never
	// prematurely aborted, while still bounding the pathological case -- a slow/hanging RPC where
	// every chunk approaches the 2-minute per-call timeout -- to only a couple of chunks per attempt
	// instead of tens of hours. Combined with maxIBERPCLookupFailures (5), the worst-case additional
	// startup delay before the setter gives up and falls back to its safe lower bound is 5 * 5m =
	// 25 minutes: long enough to look pathological in logs/metrics, but never unbounded.
	maxRPCScanDuration = 5 * time.Minute
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
		rd.GetFinalizedBlockType(),
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
// If toBlock is nil, it defaults to LatestBlock. The result of this function is only ever used as a
// starting point for a syncer; reorg safety is the syncer's own job, so scanning up to the chain's
// latest block (rather than defaulting to cfg.BlockFinality, a finality tag) is safe here. Defaulting
// to a finality tag would instead make a settled-but-not-yet-L2-finalized claim incorrectly report as
// "not found", since OP-stack finality lags L1 by 13+ minutes.
// The scan never issues an unbounded [0, toBlockNum] call; each call spans at most the chunk size:
// cfg.SyncBlockChunkSize is used as the chunk size when it is greater than zero, otherwise
// defaultRPCScanBlockChunkSize is used. A short range that fits within one chunk is still covered by
// exactly one such bounded call. If a chunk fails with a recognised eth_getLogs size/range error --
// either an explicit max-range limit or a "too many results" style response, both classified by
// aggkitcommon.NextEthGetLogsWindow, the single shared decision point every aggkit downloader uses
// for this -- the chunk size is shrunk accordingly, a WARN is logged so operators know to lower
// SyncBlockChunkSize in config, and that same window is retried before continuing the backwards
// scan. Any other chunk failure aborts the scan and returns an error that carries the failing
// chunk's range and size.
// Each underlying FilterLogs call is bounded by sync.DefaultFilterLogsTimeout so a hung RPC cannot
// block the caller forever. In addition, the *entire* backwards scan (all chunks together) is
// bounded by maxRPCScanDuration, so a huge unresolved range cannot run for an unbounded amount of
// wall-clock time even though no single chunk call hangs; expiry of that overall deadline is
// reported as an error (see below), never as "not found".
// The bool return value is false when no matching log is found within the scanned range (no error in
// that case); an error is returned only when the scan itself could not complete -- including when
// the overall scan deadline is exceeded before the scan reaches block 0.
func (c *ClaimSync) GetLatestBlockNumByGlobalIndexFromRPC(
	ctx context.Context, globalIndex *big.Int, toBlock *aggkittypes.BlockNumberFinality) (uint64, bool, error) {
	return c.getLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, toBlock, maxRPCScanDuration)
}

// getLatestBlockNumByGlobalIndexFromRPC implements GetLatestBlockNumByGlobalIndexFromRPC.
// scanDeadline is factored out as a parameter (rather than referencing the maxRPCScanDuration
// constant directly) solely so unit tests can exercise overall-deadline expiry without waiting on
// the real constant's value; production code always calls this via GetLatestBlockNumByGlobalIndexFromRPC,
// which passes maxRPCScanDuration.
func (c *ClaimSync) getLatestBlockNumByGlobalIndexFromRPC(
	ctx context.Context, globalIndex *big.Int, toBlock *aggkittypes.BlockNumberFinality,
	scanDeadline time.Duration) (uint64, bool, error) {
	if toBlock == nil {
		toBlock = &aggkittypes.LatestBlock
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
	// or 0/false when no match is found in that range. A per-call timeout is applied (derived from
	// parentCtx, which is the overall-scan-bounded context, so it can only ever fire sooner than
	// the overall deadline, never later) so a hung RPC cannot block the retry loop forever.
	scanRange := func(parentCtx context.Context, from, to uint64) (uint64, bool, error) {
		callCtx, cancel := context.WithTimeout(parentCtx, sync.DefaultFilterLogsTimeout)
		defer cancel()
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
		logs, err := c.ethClient.FilterLogs(callCtx, query)
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

	// chunkSize is the window size for the backwards scan. When SyncBlockChunkSize is configured
	// (> 0) it is used as-is; otherwise defaultRPCScanBlockChunkSize is used, so an unset config
	// never causes an unbounded scan of the whole [0, toBlockNum] range -- it is always bounded to
	// at most chunkSize per call, even though a short range (toBlockNum <= chunkSize) still ends
	// up covered by exactly one such bounded call.
	chunkSize := c.cfg.SyncBlockChunkSize
	if chunkSize == 0 {
		chunkSize = defaultRPCScanBlockChunkSize
	}

	c.logger.Infof("claimsync: scanning RPC logs for globalIndex %s in range [0, %d] with chunk size %d "+
		"(overall scan deadline %s)", globalIndex.String(), toBlockNum, chunkSize, scanDeadline)

	// overallScanCtx bounds the *entire* backwards scan below (all chunks together), on top of the
	// per-chunk timeout scanRange already applies. It is derived from ctx, so if ctx already carries
	// an earlier deadline, that earlier deadline still wins -- context.WithTimeout/WithDeadline are
	// specified to fire at whichever of the parent's and the new deadline comes first, so this never
	// extends a deadline the caller already imposed.
	overallScanCtx, cancel := context.WithTimeout(ctx, scanDeadline)
	defer cancel()

	// Scan backwards in chunks of chunkSize, returning on the first match found. If a chunk
	// fails with a recognised max-range error, shrink chunkSize to the reported value and retry
	// the same window (current is left unchanged) before continuing the backwards scan.
	current := toBlockNum
	for {
		select {
		case <-overallScanCtx.Done():
			return 0, false, fmt.Errorf(
				"claimsync: overall RPC scan deadline (%s) exceeded for globalIndex %s before scanning chunk "+
					"ending at block %d (requested range [0, %d]): %w",
				scanDeadline, globalIndex.String(), current, toBlockNum, overallScanCtx.Err())
		default:
		}

		chunkFrom := uint64(0)
		if current >= chunkSize {
			chunkFrom = current - chunkSize + 1
		}

		c.logger.Debugf("claimsync: scanning RPC logs for globalIndex %s in chunk [%d, %d]",
			globalIndex.String(), chunkFrom, current)

		blockNum, found, err := scanRange(overallScanCtx, chunkFrom, current)
		if err != nil {
			if overallScanCtx.Err() != nil {
				// Distinguish an overall-deadline expiry (counted by the caller as a scan failure
				// that is retried, per the setter's contract: err != nil => transient, retried up to
				// maxIBERPCLookupFailures before falling back) from an ordinary per-chunk error. This
				// is deliberately checked before the shrinkable/NextEthGetLogsWindow branch below,
				// since a chunk failing because its own context expired is not a "too large" signal.
				return 0, false, fmt.Errorf(
					"claimsync: overall RPC scan deadline (%s) exceeded for globalIndex %s while scanning "+
						"chunk [%d, %d] (requested range [0, %d]): %w",
					scanDeadline, globalIndex.String(), chunkFrom, current, toBlockNum, overallScanCtx.Err())
			}
			newChunkSize, shrinkable := aggkitcommon.NextEthGetLogsWindow(err, chunkSize)
			if shrinkable {
				c.logger.Warnf("claimsync: RPC rejected chunk [%d, %d] for globalIndex %s as too large "+
					"(configured/current chunk size %d); shrinking chunk size to %d and retrying -- if "+
					"this warning persists, lower SyncBlockChunkSize in config to avoid repeated retries: %v",
					chunkFrom, current, globalIndex.String(), chunkSize, newChunkSize, err)
				chunkSize = newChunkSize
				continue
			}
			return 0, false, fmt.Errorf(
				"claimsync: FilterLogs error for globalIndex %s in chunk [%d, %d] (chunk size %d): %w",
				globalIndex.String(), chunkFrom, current, chunkSize, err)
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
