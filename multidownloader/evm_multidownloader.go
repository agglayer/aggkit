package multidownloader

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

	mutex         sync.Mutex
	isInitialized bool
	state         *State // current state of synced and pending segments

	statistics *Statistics
}

var _ aggkittypes.MultiDownloader = (*EVMMultidownloader)(nil)

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
		reorgProcessor = NewReorgProcessor(log, ethClient, rpcClient, storageDB)
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
	}, nil
}

func (dh *EVMMultidownloader) RegisterSyncer(data aggkittypes.SyncerConfig) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()

	if dh.isInitialized {
		return fmt.Errorf("registerSyncer: cannot add new syncer config after initialization")
	}
	dh.syncersConfig.Add(data)
	return nil
}

func (dh *EVMMultidownloader) MoveUnsafeToSafeIfPossible(ctx context.Context) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()

	finalizedBlockNumber, err := dh.GetFinalizedBlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("MoveUnsafeToSafeIfPossible: cannot get finalized block number: %w", err)
	}

	committed := false
	tx, err := dh.storage.NewTx(ctx)
	if err != nil {
		return fmt.Errorf("MoveUnsafeToSafeIfPossible: cannot create new tx: %w", err)
	}
	defer func() {
		if !committed {
			dh.log.Debugf("MoveUnsafeToSafeIfPossible: rolling back tx")
			if err := tx.Rollback(); err != nil {
				dh.log.Errorf("MoveUnsafeToSafeIfPossible: error rolling back tx: %v", err)
			}
		}
	}()

	blocks, err := dh.storage.GetBlockHeadersNotFinalized(tx, finalizedBlockNumber)
	if err != nil {
		return fmt.Errorf("MoveUnsafeToSafeIfPossible: cannot get unsafe block bases: %w", err)
	}
	dh.log.Infof("MoveUnsafeToSafeIfPossible: finalizedBlockNumber=%d, unsafe blocks to finalize=%d", finalizedBlockNumber, len(blocks))
	err = dh.detectReorgs(ctx, blocks)
	if err != nil {
		return fmt.Errorf("MoveUnsafeToSafeIfPossible: error detecting reorgs: %w", err)
	}
	err = dh.storage.UpdateBlockToFinalized(tx, blocks.BlockNumbers())
	if err != nil {
		return fmt.Errorf("MoveUnsafeToSafeIfPossible: cannot update is_final for block bases: %w", err)
	}
	committed = true
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("MoveUnsafeToSafeIfPossible: cannot commit tx: %w", err)
	}

	return nil
}

func (dh *EVMMultidownloader) detectReorgs(ctx context.Context,
	blocks aggkittypes.ListBlockHeaders) error {
	// TODO: optimize this to don't check all blocks
	// TODO: Find the first block to reorg
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
			return fmt.Errorf("detectReorgs: block number %d not found in RPC", number)
		}
		storageBlock, exists := storageBlocks[number]
		if !exists {
			return fmt.Errorf("detectReorgs: block number %d not found in storage", number)
		}
		if storageBlock.Hash != rpcBlock.Hash {
			return mdrtypes.NewReorgError(storageBlock.Number, storageBlock.Hash, rpcBlock.Hash,
				fmt.Sprintf("detectReorgs: reorg detected at block number %d: storage hash %s != rpc hash %s",
					number, storageBlock.Hash.String(), rpcBlock.Hash.String()))
		}
	}
	return nil
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
func (dh *EVMMultidownloader) CheckDatabase(ctx context.Context) error {
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

// Initialize initializes the multidownloader. At this point all syncers
// must be registered and it will prepare the pendingSync segments
func (dh *EVMMultidownloader) Initialize(ctx context.Context) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	if dh.isInitialized {
		return fmt.Errorf("initialize: already initialized")
	}
	dh.log.Infof("Initializing multidownloader...")
	// Check DB compatibility
	err := dh.CheckDatabase(ctx)
	if err != nil {
		return err
	}
	dh.log.Infof("Saving syncer configs to storage...")
	// Save syncer configs to storage; it overrides previous ones but keeps
	// the synced segments
	err = dh.storage.UpsertSyncerConfigs(nil, dh.syncersConfig.ContractConfigs())
	if err != nil {
		return err
	}
	// Get synced segments per contract
	syncSegments, err := dh.syncersConfig.SyncSegments()
	if err != nil {
		return err
	}
	// Update TargetToBlock from name to real block numbers
	err = syncSegments.UpdateTargetBlockToNumber(ctx, dh.blockNotifierManager)
	if err != nil {
		return fmt.Errorf("Initialize: cannot update TargetToBlock in sync segments: %w", err)
	}
	// Get synced segments from storage
	storageSyncSegments, err := dh.storage.GetSyncedBlockRangePerContract(nil)
	if err != nil {
		return err
	}
	newState, err := NewStateFromStorageSyncedBlocks(storageSyncSegments, *syncSegments)
	if err != nil {
		return err
	}
	// What is pending to download?
	dh.state = newState
	dh.isInitialized = true
	dh.log.Infof("Initialization completed. state: %s",
		dh.state.String())
	return nil
}
func (dh *EVMMultidownloader) Start(ctx context.Context) error {
	err := dh.Initialize(ctx)
	if err != nil {
		return err
	}
	for {
		err = dh.StartStep(ctx)
		if err != nil {
			reorgErr := mdrtypes.CastReorgError(err)
			if reorgErr == nil {
				panic("Error running multidownloader: " + err.Error())
			}
			dh.log.Warnf("Reorg detected: %s", reorgErr.Error())
			err = dh.reorgProcessor.ProcessReorg(ctx, reorgErr.OffendingBlockNumber)
			if err != nil {
				panic("Error running multidownloader: " + err.Error())
			}
		}
		// Breathing, just in case
		dh.log.Infof("relauncing sync loop... (waiting 1 second)")
		time.Sleep(1 * time.Second)
	}
}

func (dh *EVMMultidownloader) StartStep(ctx context.Context) error {
	dh.log.Infof("checking unsafe blocks on DB...")
	var err error
	if err = dh.MoveUnsafeToSafeIfPossible(ctx); err != nil {
		return err
	}
	if err = dh.sync(ctx, dh.StepSafe, "safe"); err != nil {
		return err
	}
	for {
		dh.log.Infof("Unsafe sync iteration starting...")
		if err = dh.sync(ctx, dh.StepUnsafe, "unsafe"); err != nil {
			return err
		}
		dh.log.Infof("waiting new block...")
		if err = dh.checkReorgUntilNewBlock(ctx); err != nil {
			return err
		}
	}
}

// This function check the tip of the chain to prevent any reorg, meanwhile
// wait for a new block to arrive
func (dh *EVMMultidownloader) checkReorgUntilNewBlock(ctx context.Context) error {
	initialFinalizedBlockNumber, err := dh.GetFinalizedBlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("checkReorgUntilNewBlock: cannot get finalized block number: %w", err)
	}
	lowestBlock, highestBlock, err := dh.storage.GetRangeBlockHeader(nil, mdrtypes.NotFinalized)
	if err != nil {
		return fmt.Errorf("checkReorgUntilNewBlock: cannot get highest unsafe block: %w", err)
	}
	if lowestBlock == nil || highestBlock == nil {
		dh.log.Infof("checkReorgUntilNewBlock: no unsafe blocks to check for reorgs")
		return nil
	}

	for {
		select {
		case <-time.After(dh.cfg.PeriodToCheckReorgs.Duration):
			if err := dh.detectReorgs(ctx, []*aggkittypes.BlockHeader{highestBlock}); err != nil {
				return fmt.Errorf("checkReorgUntilNewBlock: cannot check reorg on tip block %d: %w",
					highestBlock.Number, err)
			}
			if err := dh.state.UpdateTargetBlockToNumber(ctx, dh.blockNotifierManager); err != nil {
				return fmt.Errorf("checkReorgUntilNewBlock: cannot update TargetToBlock in pendingSync: %w", err)
			}
			highestBlockPendingToSync := dh.state.GetHighestBlockNumberPendingToSync()
			if highestBlockPendingToSync > highestBlock.Number {
				dh.log.Infof("checkReorgUntilNewBlock: new block to sync (old: %d, new: %d), ",
					highestBlock.Number, highestBlockPendingToSync)
				return nil
			}
			finalizedBlockNumber, err := dh.GetFinalizedBlockNumber(ctx)
			if err != nil {
				return fmt.Errorf("checkReorgUntilNewBlock: cannot get finalized block number: %w", err)
			}
			if finalizedBlockNumber != initialFinalizedBlockNumber {
				dh.log.Infof("checkReorgUntilNewBlock: finalized block advanced from %d to %d, re-checking reorgs",
					initialFinalizedBlockNumber, finalizedBlockNumber)
				return nil
			}
		case <-ctx.Done():
			return fmt.Errorf("checkReorgUntilNewBlock: context done: %w", ctx.Err())
		}
	}
}

// sync is an internal function that executes the given stepFunc until it returns done=true or error
func (dh *EVMMultidownloader) sync(ctx context.Context,
	stepFunc func(ctx context.Context) (bool, error), name string) error {
	dh.statistics.StartSyncing()

	iteration := 0
	dh.log.Infof("🚀🚀🚀🚀🚀🚀 start syncing %s ...", name)
	// Execute steps until done or error
	for done, err := stepFunc(ctx); !done; done, err = stepFunc(ctx) {
		if err != nil {
			dh.log.Warnf("🐞🐞🐞🐞🐞 sync %s fails after %d iterations. err: %w",
				name, iteration, err)
			return err
		}
		if ctx.Err() != nil {
			dh.log.Infof("🐞🐞🐞🐞🐞 sync %s fails after %d iterations. err: %w",
				name, iteration, ctx.Err())
			return ctx.Err()
		}
		iteration++
	}
	dh.log.Infof("🎉🎉🎉🎉🎉 sync %s completed after %d iterations.", name, iteration)
	dh.statistics.FinishSyncing()
	//dh.ShowStatistics(iteration)
	return nil
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

func (dh *EVMMultidownloader) IsAvailable(query mdrtypes.LogQuery) bool {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	return dh.state.IsAvailable(query)
}

// getTotalPendingBlockRange returns the full pending block range without taking in
// consideration addrs
func (dh *EVMMultidownloader) getTotalPendingBlockRange(ctx context.Context) *aggkitcommon.BlockRange {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	br := dh.state.GetTotalPendingBlockRange()
	return br
}

func (dh *EVMMultidownloader) getUnsafeLogQueries(ctx context.Context, blockHeaders []*aggkittypes.BlockHeader) []mdrtypes.LogQuery {
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
	return addresses
}

func (dh *EVMMultidownloader) checkIntegrityNewLogsBlockHeaders(logs []types.Log,
	blockHeaders aggkittypes.ListBlockHeaders) error {
	blockMap := blockHeaders.ToMap()
	for _, lg := range logs {
		bh, exists := blockMap[lg.BlockNumber]
		if !exists {
			return fmt.Errorf("checkIntegrityNewLogsBlockHeaders: block header for log block number %d not found", lg.BlockNumber)
		}
		if bh.Hash != lg.BlockHash {
			return fmt.Errorf("checkIntegrityNewLogsBlockHeaders: log block hash %s does not match block header hash %s for block number %d",
				lg.BlockHash.String(), bh.Hash.String(), lg.BlockNumber)
		}
	}
	return nil
}

func (dh *EVMMultidownloader) checkParent(ctx context.Context, blockHeader *aggkittypes.BlockHeader) error {
	if blockHeader.Number == 0 {
		return nil
	}
	parentHeader, isFinalized, err := dh.storage.GetBlockHeaderByNumber(nil, blockHeader.Number-1)
	if err != nil {
		return fmt.Errorf("checkParent: cannot get parent block header for block number %d: %w", blockHeader.Number, err)
	}
	if parentHeader == nil {
		return fmt.Errorf("checkParent: parent block header for block number %d not found in storage", blockHeader.Number-1)
	}
	// Parenthash (from DB) doesn't match parent Hash of first blockHeader, but parent is finalized
	// so the discrepancy is the new block that is discarded without reorg (still not in DB)
	if isFinalized && blockHeader.ParentHash != nil && parentHeader.Hash != *blockHeader.ParentHash {
		return fmt.Errorf("checkParent: parent hash mismatch for block number %d: expected %s, got %s (but parent is finalized)",
			blockHeader.Number, blockHeader.ParentHash.String(), parentHeader.Hash.String())
	}
	if blockHeader.ParentHash != nil && parentHeader.Hash != *blockHeader.ParentHash {
		// Parenthash mismatch, reorg detected
		return mdrtypes.NewReorgError(parentHeader.Number, parentHeader.Hash, *blockHeader.ParentHash, fmt.Sprintf("checkParent: parent hash mismatch for block number %d: expected %s, got %s",
			blockHeader.Number, blockHeader.ParentHash.String(), parentHeader.Hash.String()))
	}
	return nil
}

func (dh *EVMMultidownloader) StepUnsafe(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	pendingBlockRange := dh.getTotalPendingBlockRange(ctx)
	blocks := pendingBlockRange.ListBlockNumbers()
	// TODO: Check that the blocks are all inside unsafe range
	blockHeaders, err := etherman.RetrieveBlockHeaders(ctx, dh.log, dh.ethClient, dh.rpcClient,
		blocks, dh.cfg.MaxParallelBlockHeaderRetrieval)
	if err != nil {
		return false, fmt.Errorf("Unsafe/Step: failed to retrieve %s block headers: %w", pendingBlockRange.String(), err)
	}
	dh.log.Debugf("Unsafe/Step: querying logs for %s", pendingBlockRange.String())
	logQueries := dh.getUnsafeLogQueries(ctx, blockHeaders)
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

	// Update ToBlock in pending segments to be able to calculate if finished
	err = newState.UpdateTargetBlockToNumber(ctx, dh.blockNotifierManager)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot update ToBlock in pendingSync: %w", err)
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
	logs []types.Log, blocks []*aggkittypes.BlockHeader,
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
		return fmt.Errorf("Safe/Step: cannot create new tx: %w", err)
	}
	defer func() {
		if !committed {
			dh.log.Debugf("Safe/Step: rolling back tx")
			if err := tx.Rollback(); err != nil {
				dh.log.Errorf("Safe/Step: error rolling back tx: %v", err)
			}
		}
	}()
	// Save logs and block headers
	err = dh.storage.SaveEthLogsWithHeaders(tx, blocks, logs, isFinal)
	if err != nil {
		return fmt.Errorf("Safe/Step: cannot save eth logs: %w", err)
	}
	// Update synced segments in storage
	err = dh.storage.UpdateSyncedStatus(tx, updatedSegments)
	if err != nil {
		return fmt.Errorf("Safe/Step: cannot update synced segments +%v in storage: %w",
			updatedSegments,
			err)
	}
	committed = true
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("Safe/Step: cannot commit tx: %w", err)
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
