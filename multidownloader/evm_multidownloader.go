package multidownloader

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/etherman"
	ethermanblocknotifier "github.com/agglayer/aggkit/etherman/block_notifier"
	ethermantypes "github.com/agglayer/aggkit/etherman/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethrpc "github.com/ethereum/go-ethereum/rpc"
)

const (
	safeMode                 = mdrtypes.Finalized
	unsafeMode               = mdrtypes.NotFinalized
	chunkSizeReductionFactor = 10
	minChunkSize             = 1
)

type EVMMultidownloader struct {
	log                  aggkitcommon.Logger
	cfg                  Config
	ethClient            aggkittypes.BaseEthereumClienter
	rpcClient            aggkittypes.RPCClienter
	storage              mdrtypes.Storager
	blockNotifierManager ethermantypes.BlockNotifierManager
	name                 string
	syncersConfig        mdrtypes.SetSyncerConfig
	reorgProcessor       mdrtypes.ReorgProcessor

	mutex      sync.Mutex
	state      *State // current state of synced and pending segments if nil not initialized
	statistics *Statistics

	// Control fields for Start/Stop
	stopRequested bool
	isRunning     bool
	wg            sync.WaitGroup
	cancel        context.CancelFunc

	// Debug fields
	debug *EVMMultidownloaderDebug
}

var _ aggkittypes.MultiDownloaderLegacy = (*EVMMultidownloader)(nil)

// NewEVMMultidownloader creates a new EVM multidownloader instance with proper validation
func NewEVMMultidownloader(log aggkitcommon.Logger,
	cfg Config,
	name string,
	ethClient aggkittypes.BaseEthereumClienter,
	rpcClient aggkittypes.RPCClienter,
	storageDB mdrtypes.Storager,
	blockNotifierManager ethermantypes.BlockNotifierManager,
	reorgProcessor mdrtypes.ReorgProcessor,
) (*EVMMultidownloader, error) {
	if blockNotifierManager == nil {
		blockNotifierManager = ethermanblocknotifier.NewBlockNotifierManager(log,
			func(finality aggkittypes.BlockNumberFinality) (ethermantypes.BlockNotifier, error) {
				bn, er := ethermanblocknotifier.NewBlockNotifierPolling(ethClient, ethermanblocknotifier.ConfigBlockNotifierPolling{
					BlockFinalityType: finality,
				}, log, nil)
				return bn, er
			})
	}
	var err error
	if err = cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Multidownloader (%s) config: %w", name, err)
	}
	if storageDB == nil {
		storageDB, err = storage.NewMultidownloaderStorage(log,
			storage.MultidownloaderStorageConfig{
				DBPath: cfg.StoragePath,
			})
		if err != nil {
			return nil, fmt.Errorf("Initialize: cannot create storage: %w", err)
		}
	}

	if reorgProcessor == nil {
		log.Infof("NewEVMMultidownloader: creating default ReorgProcessor for multidownloader (%s)", name)
		reorgProcessor = NewReorgProcessor(log, ethClient, rpcClient, storageDB, cfg.DeveloperMode)
	}
	var debug *EVMMultidownloaderDebug
	if cfg.DeveloperMode {
		log.Warnf("NewEVMMultidownloader: enabling debug mode for multidownloader (%s)", name)
		debug = NewEVMMultidownloaderDebug()
	}

	return &EVMMultidownloader{
		log:                  log,
		ethClient:            ethClient,
		rpcClient:            rpcClient,
		storage:              storageDB,
		blockNotifierManager: blockNotifierManager,
		cfg:                  cfg,
		syncersConfig:        mdrtypes.NewSetSyncerConfig(),
		statistics:           NewStatistics(),
		name:                 name,
		reorgProcessor:       reorgProcessor,
		debug:                debug,
	}, nil
}

func (dh *EVMMultidownloader) GetRPCServices() []jRPC.Service {
	logger := log.WithFields("module", "multidownloader-rpc-"+dh.name)
	return []jRPC.Service{
		{
			Name:    "multidownloader-" + dh.name,
			Service: NewEVMMultidownloaderRPC(logger, dh),
		},
	}
}

// RegisterSyncer registers a new syncer config to the multidownloader.
// it must be called before initialization or Start
func (dh *EVMMultidownloader) RegisterSyncer(data aggkittypes.SyncerConfig) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()

	if dh.isInitializedNoMutex() {
		return fmt.Errorf("registerSyncer: cannot add new syncer config after initialization")
	}

	dh.syncersConfig.Add(data)
	return nil
}

// Initialize initializes the multidownloader. At this point all syncers
// must be registered and it will prepare the pendingSync segments
func (dh *EVMMultidownloader) Initialize(ctx context.Context) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	if dh.isInitializedNoMutex() {
		return fmt.Errorf("initialize: already initialized")
	}
	dh.log.Debugf("Initializing multidownloader...")
	// Check DB compatibility
	err := dh.checkDatabaseContentsCompatibility(ctx)
	if err != nil {
		return err
	}
	dh.log.Debugf("Saving syncer configs to storage...")
	// Save syncer configs to storage; it overrides previous ones but keeps
	// the synced segments
	err = dh.storage.UpsertSyncerConfigs(nil, dh.syncersConfig.ContractConfigs())
	if err != nil {
		return err
	}
	newState, err := dh.newStateFromStorage()
	if err != nil {
		return fmt.Errorf("Initialize: error creating new state from storage: %w", err)
	}
	// What is pending to download?
	dh.state = newState
	dh.log.Infof("Initialization completed.configs: %s state: %s",
		dh.syncersConfig.Brief(), dh.state.String())
	return nil
}

// newStateFromStorage creates a new State based on data on storage and the current syncer configs.
// It is used on initialization and after reorgs to recreate the state of pending and synced segments
func (dh *EVMMultidownloader) newStateFromStorage() (*State, error) {
	syncSegments, err := dh.syncersConfig.SyncSegments()
	if err != nil {
		return nil, err
	}
	// Update TargetToBlock from name to real block numbers
	err = syncSegments.UpdateTargetBlockToNumber(context.Background(), dh.blockNotifierManager)
	if err != nil {
		return nil, fmt.Errorf("newStateFromStorage: cannot update TargetToBlock in sync segments: %w", err)
	}
	// Get synced segments from storage
	storageSyncSegments, err := dh.storage.GetSyncedBlockRangePerContract(nil)
	if err != nil {
		return nil, fmt.Errorf("newStateFromStorage: cannot get synced block ranges from storage: %w", err)
	}
	return NewStateFromStorageSyncedBlocks(storageSyncSegments, *syncSegments)
}

const infiniteLoops = -1

func (dh *EVMMultidownloader) Start(ctx context.Context) error {
	return dh.startNumLoops(ctx, infiniteLoops)
}

func (dh *EVMMultidownloader) startNumLoops(ctx context.Context, numLoopsToExecute int) error {
	dh.mutex.Lock()
	if dh.isRunning {
		dh.mutex.Unlock()
		return fmt.Errorf("Start: multidownloader is already running")
	}
	// Create a cancelable context for this run
	runCtx, cancel := context.WithCancel(ctx)
	dh.cancel = cancel
	dh.isRunning = true
	dh.stopRequested = false
	dh.wg.Add(1)
	dh.mutex.Unlock()

	defer func() {
		dh.mutex.Lock()
		dh.isRunning = false
		dh.stopRequested = false
		dh.cancel = nil
		dh.mutex.Unlock()
		dh.wg.Done()
	}()

	if !dh.IsInitialized() {
		dh.log.Infof("EVMMultidownloader.Start: multidownloader not initialized, initializing...")
		err := dh.Initialize(runCtx)
		if err != nil {
			return err
		}
	}

	dh.statistics.StartSyncing()
	numLoops := 0
	for {
		// This is for debug, when reach the number of loops it returns to allow testing
		if numLoops == numLoopsToExecute {
			return nil
		}
		numLoops++
		// check if context is done
		if runCtx.Err() != nil {
			dh.log.Infof("EVMMultidownloader.Start: context done, exiting...")
			return runCtx.Err()
		}
		err := dh.debug.GetInjectedStartStepError()
		if err != nil {
			dh.log.Warnf("EVMMultidownloader.Start: debug forced error set: %s",
				err.Error())
		} else {
			err = dh.StartStep(runCtx)
		}
		if err != nil {
			reorgErr := mdrtypes.CastDetectedReorgError(err)
			if reorgErr == nil {
				dh.log.Warnf("Error running multidownloader: %s ", err.Error())
				time.Sleep(time.Millisecond) // Brief pause before retry
				continue
			}
			dh.log.Warnf("Reorg detected: %s", reorgErr.Error())
			for {
				dh.mutex.Lock()
				// check if context is done during reorg processing
				if runCtx.Err() != nil {
					dh.mutex.Unlock()
					dh.log.Infof("EVMMultidownloader.Start: context done during reorg processing, exiting...")
					return runCtx.Err()
				}

				dh.log.Infof("Processing reorg at block number %d...", reorgErr.OffendingBlockNumber)
				err = dh.reorgProcessor.ProcessReorg(runCtx, *reorgErr)
				if err != nil {
					dh.mutex.Unlock()
					dh.log.Warnf("Error running reorg multidownloader: %s", err.Error())
					time.Sleep(1 * time.Second)
					continue
				}
				newState, err := dh.newStateFromStorage()
				if err != nil {
					dh.mutex.Unlock()
					dh.log.Warnf("Error recreating state after reorg processing: %s", err.Error())
					time.Sleep(1 * time.Second)
					continue
				}
				dh.state = newState
				dh.mutex.Unlock()
				break
			}
		}
	}
}

// Stop gracefully stops the multidownloader if it's running
func (dh *EVMMultidownloader) Stop(ctx context.Context) error {
	dh.mutex.Lock()
	if !dh.isRunning {
		dh.mutex.Unlock()
		return fmt.Errorf("Stop: multidownloader is not running")
	}
	cancel := dh.cancel
	dh.mutex.Unlock()

	dh.log.Infof("Stop: stopping multidownloader...")

	// Cancel the running context
	if cancel != nil {
		cancel()
	}

	// Wait for the goroutine to finish with context timeout
	done := make(chan struct{})
	go func() {
		dh.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		dh.log.Infof("Stop: multidownloader stopped successfully")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("Stop: timeout waiting for multidownloader to stop: %w", ctx.Err())
	}
}
func (dh *EVMMultidownloader) updateTargetBlockNumber(ctx context.Context) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	return dh.state.UpdateTargetBlockToNumber(ctx, dh.blockNotifierManager)
}

func (dh *EVMMultidownloader) checkReorgsUnsafeZone(ctx context.Context) error {
	blockInUnsafeZone, err := dh.storage.GetBlockHeadersNotFinalized(nil, nil)
	if err != nil {
		return fmt.Errorf("checkReorgsUnsafeZone: cannot get unsafe blocks: %w", err)
	}
	return dh.detectReorgs(ctx, blockInUnsafeZone)
}

func (dh *EVMMultidownloader) StartStep(ctx context.Context) error {
	var err error
	// Update ToBlock in pending segments to be able to calculate if finished
	err = dh.updateTargetBlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("cannot update ToBlock: %w", err)
	}

	// There are unsafe blocks that can be moved to safe and checked?
	if err = dh.moveUnsafeToSafeIfPossible(ctx); err != nil {
		return err
	}
	// Check possible reorgs in unsafe zone
	if err = dh.checkReorgsUnsafeZone(ctx); err != nil {
		return err
	}

	// Get the pending blocks to sync
	pendingBlockRange := dh.getTotalPendingBlockRange()
	if pendingBlockRange != nil {
		dh.log.Debugf("StartStep: pendingBlockRange=%s", pendingBlockRange.String())
		// Split into safe and unsafe
		finalizedBlockNumber, err := dh.GetFinalizedBlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("StartStep: cannot get finalized block number: %w", err)
		}
		safePendingBlockRange, unsafePendingBlockRange := pendingBlockRange.SplitByBlockNumber(finalizedBlockNumber)
		if !safePendingBlockRange.IsEmpty() {
			dh.log.Infof("🛡️ StartStep: Safe sync for pending range %s", safePendingBlockRange.String())
			_, err = dh.StepSafe(ctx)
			return err
		}
		if !unsafePendingBlockRange.IsEmpty() {
			dh.log.Infof("😈 StartStep: Unsafe sync for pending range %s", unsafePendingBlockRange.String())
			_, err = dh.StepUnsafe(ctx)
			return err
		}
	} else {
		dh.log.Debugf("StartStep: no pending blocks to sync")
	}
	dh.log.Infof("⏳StartStep: waiting new block...")
	if err = dh.WaitForNewLatestBlocks(ctx); err != nil {
		return err
	}
	return nil
}

func (dh *EVMMultidownloader) WaitForNewLatestBlocks(ctx context.Context) error {
	latestSyncedBlockNumber, lastSyncedBlockTag := dh.state.GetHighestBlockNumberPendingToSync()
	lastBlockHeader, finalized, err := dh.storage.GetBlockHeaderByNumber(nil, latestSyncedBlockNumber)
	if err != nil {
		return fmt.Errorf("WaitForNewLatestBlocks: cannot get block header for latest synced block %d: %w",
			latestSyncedBlockNumber, err)
	}
	dh.log.Infof("waiting new block (%s>%d)...", lastSyncedBlockTag.String(), latestSyncedBlockNumber)
	_, err = dh.waitForNewBlocks(ctx, lastSyncedBlockTag, lastBlockHeader, finalized)
	return err
}

func (dh *EVMMultidownloader) waitForNewBlocks(ctx context.Context,
	blockTag aggkittypes.BlockNumberFinality,
	lastBlockHeader *aggkittypes.BlockHeader,
	finalized mdrtypes.FinalizedType) (uint64, error) {
	// TODO: This var dh.cfg.PeriodToCheckReorgs.Duration is the best choice?
	ticker := time.NewTicker(dh.cfg.PeriodToCheckReorgs.Duration)
	defer ticker.Stop()
	dh.log.Debugf("waitForNewBlocks: waiting for new blocks %s after %d. Check each %s...",
		blockTag.String(),
		lastBlockHeader.Number,
		dh.cfg.PeriodToCheckReorgs.String())
	for {
		select {
		case <-ctx.Done():
			dh.log.Info("context cancelled")
			return lastBlockHeader.Number, ctx.Err()
		case <-ticker.C:
			var currentBlock uint64
			var err error
			if finalized == mdrtypes.NotFinalized {
				// Check reorg
				currentHeader, err := dh.ethClient.CustomHeaderByNumber(ctx, &blockTag)
				if err != nil {
					return lastBlockHeader.Number, fmt.Errorf("WaitForNewBlocks: cannot get current block header: %w", err)
				}
				dh.log.Debugf("waitForNewBlocks: tag:%s currentHeader.Number=%d, lastBlockHeader.Number=%d checking Hash",
					blockTag.String(), currentHeader.Number, lastBlockHeader.Number)
				if currentHeader.Number == lastBlockHeader.Number {
					if currentHeader.Hash != lastBlockHeader.Hash {
						return lastBlockHeader.Number, mdrtypes.NewDetectedReorgError(
							lastBlockHeader.Number,
							mdrtypes.ReorgDetectionReason_BlockHashMismatch,
							lastBlockHeader.Hash,
							currentHeader.Hash,
							fmt.Sprintf("WaitForNewBlocks: reorg detected at block number %d: stored hash %s != current hash %s",
								lastBlockHeader.Number,
								lastBlockHeader.Hash.String(),
								currentHeader.Hash.String()))
					}
				}
				if currentHeader.Number == lastBlockHeader.Number+1 && currentHeader.ParentHash != nil {
					if *currentHeader.ParentHash != lastBlockHeader.Hash {
						return lastBlockHeader.Number, mdrtypes.NewDetectedReorgError(
							lastBlockHeader.Number,
							mdrtypes.ReorgDetectionReason_ParentHashMismatch,
							lastBlockHeader.Hash,
							*currentHeader.ParentHash,
							fmt.Sprintf("WaitForNewBlocks: reorg detected at block number %d: "+
								"stored hash %s != parent hash %s of new block %d",
								lastBlockHeader.Number,
								lastBlockHeader.Hash.String(),
								currentHeader.ParentHash.String(),
								currentHeader.Number))
					}
				}
				if currentHeader.Number < lastBlockHeader.Number {
					return lastBlockHeader.Number, mdrtypes.NewDetectedReorgError(
						lastBlockHeader.Number,
						mdrtypes.ReorgDetectionReason_MissingBlock,
						lastBlockHeader.Hash,
						currentHeader.Hash,
						fmt.Sprintf("WaitForNewBlocks: reorg detected at block number %d: "+
							"current block number %d < last synced block number %d",
							lastBlockHeader.Number,
							currentHeader.Number,
							lastBlockHeader.Number))
				}
				currentBlock = currentHeader.Number
			} else {
				currentBlock, err = dh.blockNotifierManager.GetCurrentBlockNumber(ctx, blockTag)
				if err != nil {
					return lastBlockHeader.Number, fmt.Errorf("WaitForNewBlocks: cannot get current block number: %w", err)
				}
			}
			if currentBlock > lastBlockHeader.Number {
				dh.log.Debugf("waitForNewBlocks: Find new block %d > lastBlockHeader.Number %d",
					currentBlock, lastBlockHeader.Number)
				return currentBlock, nil
			}
		}
	}
}

func getBlockNumbers(logs []types.Log) []uint64 {
	blockNumbers := make(map[uint64]struct{})
	result := make([]uint64, 0)
	for _, lg := range logs {
		if _, exists := blockNumbers[lg.BlockNumber]; exists {
			continue
		}
		blockNumbers[lg.BlockNumber] = struct{}{}
		result = append(result, lg.BlockNumber)
	}
	return result
}
func (dh *EVMMultidownloader) IsInitialized() bool {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	return dh.state != nil
}

func (dh *EVMMultidownloader) isInitializedNoMutex() bool {
	return dh.state != nil
}

func (dh *EVMMultidownloader) IsAvailable(query mdrtypes.LogQuery) bool {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	return dh.state.IsAvailable(query)
}

// Check if the given log query is partially available
func (dh *EVMMultidownloader) IsPartiallyAvailable(query mdrtypes.LogQuery) (bool, *mdrtypes.LogQuery) {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	return dh.state.IsPartiallyAvailable(query)
}

// getTotalPendingBlockRange returns the full pending block range without taking in
// consideration addrs
func (dh *EVMMultidownloader) getTotalPendingBlockRange() *aggkitcommon.BlockRange {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	br := dh.state.GetTotalPendingBlockRange()
	return br
}

func (dh *EVMMultidownloader) getUnsafeLogQueries(blockHeaders []*aggkittypes.BlockHeader) []mdrtypes.LogQuery {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	logQueries := make([]mdrtypes.LogQuery, 0, len(blockHeaders))
	for _, bh := range blockHeaders {
		logQueries = append(logQueries, mdrtypes.NewLogQueryBlockHash(
			bh.Number,
			bh.Hash,
			dh.state.GetAddressesToSyncForBlockNumber(bh.Number),
		))
	}
	return logQueries
}

func (dh *EVMMultidownloader) newState(queries []mdrtypes.LogQuery) (*State, error) {
	dh.mutex.Lock()
	state := dh.state.Clone()
	dh.mutex.Unlock()
	for _, logQueryData := range queries {
		err := state.Synced.AddLogQuery(&logQueryData)
		if err != nil {
			return nil, fmt.Errorf("Safe/Step: cannot extend synced segments: %w", err)
		}
		err = state.Pending.SubtractLogQuery(&logQueryData)
		if err != nil {
			return nil, fmt.Errorf("Safe/Step: cannot subtract log query from pending segments: %w", err)
		}
	}
	return state, nil
}
func getContracts(logQueries []mdrtypes.LogQuery) []common.Address {
	addressMap := make(map[common.Address]struct{})
	for _, lq := range logQueries {
		for _, addr := range lq.Addrs {
			addressMap[addr] = struct{}{}
		}
	}
	addresses := make([]common.Address, 0, len(addressMap))
	for addr := range addressMap {
		addresses = append(addresses, addr)
	}
	// Sort addresses to ensure deterministic output
	sort.Slice(addresses, func(i, j int) bool {
		return addresses[i].Hex() < addresses[j].Hex()
	})
	return addresses
}

func (dh *EVMMultidownloader) checkIntegrityNewLogsBlockHeaders(logs []types.Log,
	blockHeaders aggkittypes.ListBlockHeaders) error {
	blockMap := blockHeaders.ToMap()
	for _, lg := range logs {
		bh, exists := blockMap[lg.BlockNumber]
		if !exists {
			return fmt.Errorf("checkIntegrityNewLogsBlockHeaders: "+
				"block header for log block number %d not found", lg.BlockNumber)
		}
		if bh.Hash != lg.BlockHash {
			return fmt.Errorf("checkIntegrityNewLogsBlockHeaders: "+
				"log block hash %s does not match block header hash %s for block number %d",
				lg.BlockHash.String(), bh.Hash.String(), lg.BlockNumber)
		}
	}
	return nil
}

func (dh *EVMMultidownloader) StepUnsafe(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	pendingBlockRange := dh.getTotalPendingBlockRange()
	if pendingBlockRange == nil {
		dh.log.Debugf("StepUnsafe: no pending blocks to sync")
		return false, nil
	}
	blocks := pendingBlockRange.ListBlockNumbers()
	// TODO: Check that the blocks are all inside unsafe range
	blockHeaders, err := etherman.RetrieveBlockHeaders(ctx, dh.log, dh.ethClient, dh.rpcClient,
		blocks, dh.cfg.MaxParallelBlockHeaderRetrieval)
	if err != nil {
		return false, fmt.Errorf("Unsafe/Step: failed to retrieve %s block headers: %w", pendingBlockRange.String(), err)
	}
	dh.log.Debugf("Unsafe/Step: querying logs for %s", pendingBlockRange.String())
	logQueries := dh.getUnsafeLogQueries(blockHeaders)
	logs, err := dh.requestMultiplesLogs(ctx, logQueries)
	if err != nil {
		return false, fmt.Errorf("Unsafe/Step: failed to retrieve logs for %s: %w", pendingBlockRange.String(), err)
	}
	if err = dh.checkIntegrityNewLogsBlockHeaders(logs, blockHeaders); err != nil {
		return false, err
	}
	newState, err := dh.newState(logQueries)
	if err != nil {
		return false, fmt.Errorf("Unsafe/Step: failed to create new state: %w", err)
	}
	updatedSegments := newState.Synced.SegmentsByContract(getContracts(logQueries))
	// Store data in storage
	dh.log.Debugf("Unsafe/Step: storing data for %s", pendingBlockRange.String())
	err = dh.storeData(ctx, logs, blockHeaders,
		updatedSegments, unsafeMode)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot store data: %w", err)
	}

	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	dh.log.Debugf("Unsafe/Step: updating state in memory %s", pendingBlockRange.String())
	dh.state = newState
	finished := dh.state.IsSyncFinished()
	totalBlocksPendingToSync := dh.state.TotalBlocksPendingToSync()
	dh.log.Infof("Unsafe/Step: elapsed=%s finished br=%s logs=%d blocksHeaders=%d pendingBlocks=%d ETA=%s ",
		dh.statistics.ElapsedSyncing().String(),
		pendingBlockRange.String(),
		len(logs),
		len(blockHeaders),
		totalBlocksPendingToSync,
		dh.statistics.ETA(totalBlocksPendingToSync))
	return finished, nil
}

// StepSafe performs a safe step syncing logs and block headers from historical data
// Returns true when syncing is complete, false if more work remains
func (dh *EVMMultidownloader) StepSafe(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	// Get logs for next segment
	logs, logQueryData, err := dh.requestLogs(ctx)
	if err != nil {
		if errors.Is(err, mdrtypes.ErrFinished) {
			return true, nil
		}
		return false, fmt.Errorf("Safe/Step: failed to retrieve logs: %w", err)
	}
	dh.log.Debugf("Safe/Step: logs (%d) for blockRange=%s, addrs=%v", len(logs),
		logQueryData.BlockRange.String(), logQueryData.Addrs)
	blocks := getBlockNumbers(logs)
	dh.log.Debugf("Safe/Step: querying blockHeaders for %d blocks", len(blocks))
	blockHeaders, err := etherman.RetrieveBlockHeaders(ctx, dh.log, dh.ethClient, dh.rpcClient,
		blocks, dh.cfg.MaxParallelBlockHeaderRetrieval)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: failed to retrieve %d block headers: %w", len(blocks), err)
	}

	// Calculate new state (not set in memory until commit is successful)
	dh.mutex.Lock()
	newState := dh.state.Clone()
	dh.mutex.Unlock()
	// Update synced segments
	err = newState.OnNewSyncedLogQuery(logQueryData)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: fails OnNewSyncedLogQuery(%s): %w",
			logQueryData.String(), err)
	}

	// Store data in storage
	err = dh.storeData(ctx, logs, blockHeaders,
		newState.SyncedSegmentsByContract(logQueryData.Addrs), true)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot store data: %w", err)
	}
	// Update in-memory synced segments (after valid commit)
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	dh.state = newState
	finished := dh.state.IsSyncFinished()
	totalBlocksPendingToSync := dh.state.TotalBlocksPendingToSync()
	dh.log.Infof("Safe/Step: elapsed=%s finished br=%s logs=%d blocksHeaders=%d pendingBlocks=%d ETA=%s ",
		dh.statistics.ElapsedSyncing().String(),
		logQueryData.BlockRange.String(),
		len(logs),
		len(blockHeaders),
		totalBlocksPendingToSync,
		dh.statistics.ETA(totalBlocksPendingToSync))
	return finished, nil
}
func (dh *EVMMultidownloader) storeData(
	ctx context.Context,
	logs []types.Log,
	blocks []*aggkittypes.BlockHeader,
	updatedSegments []mdrtypes.SyncSegment,
	isFinal bool) error {
	var err error
	committed := false
	dh.statistics.StartDBOperation()
	defer func() {
		dh.statistics.FinishDBOperation()
	}()
	tx, err := dh.storage.NewTx(ctx)
	if err != nil {
		return fmt.Errorf("storeData: cannot create new tx: %w", err)
	}
	defer func() {
		if !committed {
			dh.log.Debugf("storeData: rolling back tx")
			if err := tx.Rollback(); err != nil {
				dh.log.Errorf("storeData: error rolling back tx: %v", err)
			}
		}
	}()
	// Save logs and block headers
	err = dh.storage.SaveEthLogsWithHeaders(tx, blocks, logs, isFinal)
	if err != nil {
		return fmt.Errorf("storeData: cannot save eth logs: %w", err)
	}
	// Update synced segments in storage
	err = dh.storage.UpdateSyncedStatus(tx, updatedSegments)
	if err != nil {
		return fmt.Errorf("storeData: cannot update synced segments +%v in storage: %w",
			updatedSegments,
			err)
	}
	committed = true
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("storeData: cannot commit tx: %w", err)
	}
	return nil
}

func ethGetExtendedError(err error) string {
	if err == nil {
		return ""
	}

	var jsonError ethrpc.DataError
	if !errors.As(err, &jsonError) {
		return ""
	}
	return fmt.Sprintf("json_data: %v", jsonError.ErrorData())
}
func isEthClientErrorTooManyResults(err error) bool {
	if err == nil {
		return false
	}
	// Example: "Query returned more than 20000 results. Try with this block range [0x852c16, 0x853273]."
	msg := ethGetExtendedError(err)
	return strings.Contains(msg, "Response size exceeded") || strings.Contains(msg, "Query returned more than")
}

func extractSuggestedBlockRangeFromError(err error) *aggkitcommon.BlockRange {
	if !isEthClientErrorTooManyResults(err) {
		return nil
	}
	msg := ethGetExtendedError(err)
	return extractSuggestedBlockRangeFromErrorMsg(msg)
}

// extractSuggestedBlockRangeFromErrorMsg parses error messages to extract block range suggestions
// Expected format: "Try with this block range [0x852c16, 0x853273]"
func extractSuggestedBlockRangeFromErrorMsg(msg string) *aggkitcommon.BlockRange {
	// Match content within brackets
	re := regexp.MustCompile(`\[([^\]]+)\]`)
	match := re.FindStringSubmatch(msg)
	if len(match) > 1 {
		rangeStr := match[1] // "0x852c16, 0x853273"
		re2 := regexp.MustCompile(`0x[0-9a-fA-F]+`)
		blocks := re2.FindAllString(rangeStr, -1)
		if len(blocks) == 2 { //nolint: mnd
			start, err1 := strconv.ParseUint(blocks[0], 0, 64)
			end, err2 := strconv.ParseUint(blocks[1], 0, 64)
			if err1 == nil && err2 == nil {
				br := aggkitcommon.NewBlockRange(start, end)
				return &br
			}
		}
	}
	return nil
}

func (dh *EVMMultidownloader) GetLatestBlockNumber(ctx context.Context) (uint64, error) {
	bn, err := dh.blockNotifierManager.GetCurrentBlockNumber(ctx, aggkittypes.LatestBlock)
	if err != nil {
		return 0, fmt.Errorf("GetLatestBlockNumber: cannot get latest block (%s): %w",
			aggkittypes.LatestBlock.String(), err)
	}
	return bn, nil
}

func (dh *EVMMultidownloader) GetFinalizedBlockNumber(ctx context.Context) (uint64, error) {
	bn, err := dh.blockNotifierManager.GetCurrentBlockNumber(ctx, dh.cfg.BlockFinality)
	if err != nil {
		return 0, fmt.Errorf("Safe/Step: cannot get finalized block (%s): %w",
			dh.cfg.BlockFinality.String(), err)
	}
	return bn, nil
}

func (dh *EVMMultidownloader) getNextQuery(ctx context.Context, chunk uint32, safe bool) (*mdrtypes.LogQuery, error) {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	var err error
	var maxBlock uint64
	if safe {
		maxBlock, err = dh.GetFinalizedBlockNumber(ctx)
		if err != nil {
			return nil, fmt.Errorf("getNextQuery: cannot get finalized block number: %w", err)
		}
	} else {
		maxBlock = 0
	}
	logQueryData, err := dh.state.NextQueryToSync(chunk, maxBlock)
	if err != nil {
		return nil, fmt.Errorf("getNextQuery: cannot get NextQuery: %w", err)
	}
	return logQueryData, nil
}

func (dh *EVMMultidownloader) requestMultiplesLogs(
	ctx context.Context,
	queries []mdrtypes.LogQuery) ([]types.Log, error) {
	var allLogs []types.Log
	for _, query := range queries {
		dh.log.Debugf("request: querying logs for blockHash=%s", query.String())
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("requestMultiplesLogs: context error: %w", err)
		}
		logs, err := dh.requestLogsSingleTry(ctx, &query)
		if err != nil {
			return nil, fmt.Errorf("requestMultiplesLogs: ethClient.FilterLogs(%v) failed: %w",
				query.String(), err)
		}
		dh.log.Debugf("request: successfully queried logs for blockHash=%s: returned %d logs",
			query.String(), len(logs))
		allLogs = append(allLogs, logs...)
	}
	return allLogs, nil
}

func (dh *EVMMultidownloader) requestLogs(
	ctx context.Context) ([]types.Log, *mdrtypes.LogQuery, error) {
	currentSyncBlockChunkSize := dh.cfg.BlockChunkSize
	try := 0
	logQueryData, err := dh.getNextQuery(ctx, currentSyncBlockChunkSize, safeMode)
	if err != nil {
		return nil, nil, fmt.Errorf("Safe/Step: cannot get NextQuery: %w", err)
	}
	suggestedBlockRange := &logQueryData.BlockRange
	for {
		try++
		logQueryData.BlockRange = *suggestedBlockRange
		dh.log.Debugf("Safe/Step: querying logs for %s", logQueryData.String())
		logs, err := dh.requestLogsSingleTry(ctx, logQueryData)
		if err == nil {
			dh.log.Debugf("Safe/Step: successfully queried logs for %s: returned %d logs", logQueryData.String(), len(logs))
			return logs, logQueryData, nil
		}
		// There is an error; if it's not "too many results" we can't do anything
		if !isEthClientErrorTooManyResults(err) {
			return nil, nil, fmt.Errorf("Safe/Step: ethClient.FilterLogs(%v) failed: %v. err: %w",
				logQueryData.String(), ethGetExtendedError(err), err)
		}
		// The error is "too many results", try to reduce the block range
		suggestedBlockRange = extractSuggestedBlockRangeFromError(err)
		// The suggested block range must be within the current logQueryData.BlockRange; if not,
		// it means that the extraction of blockRange from error message failed
		if logQueryData.BlockRange.Overlaps(*suggestedBlockRange) {
			dh.log.Warnf("Safe/Step: too many results for range=%s, addrs=%v, adjusting block range to %s. Err: %s",
				logQueryData.BlockRange.String(), logQueryData.Addrs, suggestedBlockRange.String(), ethGetExtendedError(err))
			continue
		}
		// We don't have a valid suggested block range, reduce the chunk size by 50%
		prevBlockChunkSize := currentSyncBlockChunkSize
		currentSyncBlockChunkSize /= chunkSizeReductionFactor
		if currentSyncBlockChunkSize < minChunkSize {
			return nil, nil, fmt.Errorf("Safe/Step: cannot reduce block chunk size any further")
		}
		dh.log.Warnf("Safe/Step: too many results for range=%s, addrs=%v, reducing chunk size from %d to %d. Err: %s",
			logQueryData.BlockRange.String(), logQueryData.Addrs, prevBlockChunkSize,
			currentSyncBlockChunkSize, ethGetExtendedError(err))
	}
}

func (dh *EVMMultidownloader) requestLogsSingleTry(ctx context.Context,
	logQueryData *mdrtypes.LogQuery) ([]types.Log, error) {
	rpcFilterQuery := logQueryData.ToRPCFilterQuery()
	dh.statistics.LaunchedEthCall()
	logs, err := dh.ethClient.FilterLogs(ctx, rpcFilterQuery)
	if err != nil {
		dh.statistics.FinishEthCall(err, 0, 0)
		return nil, err
	}
	dh.statistics.FinishEthCall(err, uint64(len(logs)), logQueryData.BlockRange.CountBlocks())
	return logs, nil
}

func (dh *EVMMultidownloader) ShowStatistics(iteration int) {
	dh.statistics.Show(dh.log.Infof, iteration)
}

// checkDatabaseContentsCompatibility checks that the data already in database
// match the data in config/RPC (e.g: contract addresses, chainID, etc)
func (dh *EVMMultidownloader) checkDatabaseContentsCompatibility(ctx context.Context) error {
	chainID, err := dh.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("Initialize: cannot get chainID: %w", err)
	}
	compatibilityStorageChecker := compatibility.NewCompatibilityCheck(
		true,
		func(ctx context.Context) (storage.DBRuntimeData, error) {
			return storage.DBRuntimeData{NetworkID: chainID,
				DataVersion: storage.DataVersionCurrent}, nil
		},
		compatibility.NewKeyValueToCompatibilityStorage[storage.DBRuntimeData](dh.storage, "multidownloader-"+dh.name),
	)

	err = compatibilityStorageChecker.Check(ctx, nil)
	if err != nil {
		return fmt.Errorf("Initialize: compatibility check failed: %w", err)
	}
	return nil
}

// moveUnsafeToSafeIfPossible it's used at start or when finalize block change
// moving the unsafe blocks to safe zone checking that the block is not reorged
// If there are any missmatch it returns an DetectedReorgError
func (dh *EVMMultidownloader) moveUnsafeToSafeIfPossible(ctx context.Context) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()

	finalizedBlockNumber, err := dh.GetFinalizedBlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("moveUnsafeToSafeIfPossible: cannot get finalized block number: %w", err)
	}

	committed := false
	tx, err := dh.storage.NewTx(ctx)
	if err != nil {
		return fmt.Errorf("moveUnsafeToSafeIfPossible: cannot create new tx: %w", err)
	}
	defer func() {
		if !committed {
			dh.log.Debugf("moveUnsafeToSafeIfPossible: rolling back tx")
			if err := tx.Rollback(); err != nil {
				dh.log.Errorf("moveUnsafeToSafeIfPossible: error rolling back tx: %v", err)
			}
		}
	}()

	blocks, err := dh.storage.GetBlockHeadersNotFinalized(tx, &finalizedBlockNumber)
	if err != nil {
		return fmt.Errorf("moveUnsafeToSafeIfPossible: cannot get unsafe block bases: %w", err)
	}
	if blocks.Len() == 0 {
		dh.log.Debugf("moveUnsafeToSafeIfPossible: no unsafe blocks to move to safe")
		return nil
	}

	err = dh.detectReorgs(ctx, blocks)
	if err != nil {
		return fmt.Errorf("moveUnsafeToSafeIfPossible: error detecting reorgs: %w", err)
	}
	err = dh.storage.UpdateBlockToFinalized(tx, blocks.BlockNumbers())
	if err != nil {
		return fmt.Errorf("moveUnsafeToSafeIfPossible: cannot update is_final for block bases: %w", err)
	}
	dh.log.Infof("moveUnsafeToSafeIfPossible: finalizedBlockNumber=%d, "+
		"block moved to safe zone: %s (len=%d)", finalizedBlockNumber, blocks.BlockRange().String(), blocks.Len())
	committed = true
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("moveUnsafeToSafeIfPossible: cannot commit tx: %w", err)
	}

	return nil
}

// detectReorgs check \param blocks that match RPC
// if not return an DetectedReorgError
func (dh *EVMMultidownloader) detectReorgs(ctx context.Context,
	blocks aggkittypes.ListBlockHeaders) error {
	if blocks.Len() == 0 {
		dh.log.Debugf("detectReorgs: no blocks to check for reorgs")
		return nil
	}
	blocksNumber := blocks.BlockNumbers()
	currentBlockHeaders, err := etherman.RetrieveBlockHeaders(ctx, dh.log, dh.ethClient, dh.rpcClient,
		blocksNumber, dh.cfg.MaxParallelBlockHeaderRetrieval)
	if err != nil {
		return fmt.Errorf("detectReorgs: cannot retrieve block headers: %w", err)
	}
	// check blocks vs currentBlockHeaders. Must match by number and hash
	storageBlocks := blocks.ToMap()
	rpcBlocks := currentBlockHeaders.ToMap()
	for _, number := range blocksNumber {
		rpcBlock, exists := rpcBlocks[number]
		if !exists {
			return mdrtypes.NewDetectedReorgError(number,
				mdrtypes.ReorgDetectionReason_MissingBlock,
				common.Hash{}, common.Hash{},
				fmt.Sprintf("detectReorgs: block number %d not found in RPC", number))
		}
		storageBlock, exists := storageBlocks[number]
		if !exists {
			return fmt.Errorf("detectReorgs: block number %d not found in storage", number)
		}
		if storageBlock.Hash != rpcBlock.Hash {
			return mdrtypes.NewDetectedReorgError(storageBlock.Number,
				mdrtypes.ReorgDetectionReason_BlockHashMismatch,
				storageBlock.Hash, rpcBlock.Hash,
				fmt.Sprintf("detectReorgs: reorg detected at block number %d: storage hash %s != rpc hash %s",
					number, storageBlock.Hash.String(), rpcBlock.Hash.String()))
		}
	}
	return nil
}
