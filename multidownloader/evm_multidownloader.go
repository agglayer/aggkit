package multidownloader

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/compatibility"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/etherman"
	ethermanblocknotifier "github.com/agglayer/aggkit/etherman/block_notifier"
	ethermantypes "github.com/agglayer/aggkit/etherman/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/core/types"
	ethrpc "github.com/ethereum/go-ethereum/rpc"
)

const safeMode = true

// const unsafeMode = false

type EVMMultidownloader struct {
	log                  aggkitcommon.Logger
	cfg                  Config
	ethClient            aggkittypes.BaseEthereumClienter
	rpcClient            aggkittypes.RPCClienter
	storage              mdrtypes.Storager
	blockNotifierManager ethermantypes.BlockNotifierManager
	name                 string
	syncersConfig        mdrtypes.SetSyncerConfig

	mutex         sync.Mutex
	isInitialized bool
	// These are the  segments that we need to sync
	pendingSync *mdrtypes.SetSyncSegment
	// These are the segments that we have already synced
	// when a syncer do a `FilterLogs`is used to check what is already synced
	syncedSegments mdrtypes.SetSyncSegment
	statistics     *Statistics
}

var _ aggkittypes.MultiDownloader = (*EVMMultidownloader)(nil)

func NewEVMMultidownloader(log aggkitcommon.Logger,
	cfg Config,
	name string,
	ethClient aggkittypes.BaseEthereumClienter,
	rpcClient aggkittypes.RPCClienter,
	storageDB mdrtypes.Storager,
	blockNotifierManager ethermantypes.BlockNotifierManager,
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
	}, nil
}

func (dh *EVMMultidownloader) RegisterSyncer(data aggkittypes.SyncerConfig) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()

	if dh.isInitialized {
		return fmt.Errorf("registerSyncer: cannot add new syncer config after initialization")
	}
	dh.syncersConfig.Add(mdrtypes.NewSyncerConfig(data))
	return nil
}

func (dh *EVMMultidownloader) MoveUnsafeToSafeIfPossible(ctx context.Context) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()

	finalizedBlockNumber, err := dh.getFinalizedBlockNumber(ctx)
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

	blocks, err := dh.storage.GetBlockHeaderNotFinal(tx, finalizedBlockNumber)
	if err != nil {
		return fmt.Errorf("MoveUnsafeToSafeIfPossible: cannot get unsafe block bases: %w", err)
	}
	err = dh.detectReorgs(ctx, tx, blocks)
	if err != nil {
		return fmt.Errorf("MoveUnsafeToSafeIfPossible: cannot detect reorgs: %w", err)
	}
	blockNumbers := make([]uint64, 0, len(blocks))
	for _, block := range blocks {
		blockNumbers = append(blockNumbers, block.Number)
	}

	err = dh.storage.UpdateIsFinal(tx, blockNumbers)
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
	tx dbtypes.Querier, blocks []*aggkittypes.BlockHeader) error {
	// TODO: implement reorg detection
	return nil
}

func (dh *EVMMultidownloader) Start(ctx context.Context) error {
	err := dh.Initialize(ctx)
	if err != nil {
		return err
	}
	// dh.log.Infof("checking unsafe blocks on DB...")
	// err = dh.MoveUnsafeToSafeIfPossible(ctx)
	// if err != nil {
	// 	return err
	// }

	err = dh.sync(ctx, dh.StepSafe, "safe")
	if err != nil {
		return err
	}
	// TODO: Implement unsafe mode syncing
	// err = dh.Sync(ctx, dh.StepUnsafe, "unsafe")
	// if err != nil {
	// 	return err
	// }

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
	compatibilityStoragedChecker := compatibility.NewCompatibilityCheck(
		true,
		func(ctx context.Context) (storage.DBRuntimeData, error) {
			return storage.DBRuntimeData{NetworkID: chainID,
				DataVersion: storage.DataVersionCurrent}, nil
		},
		compatibility.NewKeyValueToCompatibilityStorage[storage.DBRuntimeData](dh.storage, "multidownloader-"+dh.name),
	)

	err = compatibilityStoragedChecker.Check(ctx, nil)
	if err != nil {
		return fmt.Errorf("Initialize: compatibility check failed: %w", err)
	}
	return nil
}

// Initialize initializes the multidownloader, in this point all syncer
// must be registered and it will prepare the pendingSync segments
func (dh *EVMMultidownloader) Initialize(ctx context.Context) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	if dh.isInitialized {
		return fmt.Errorf("initialize: already initialized")
	}
	// Check DB compatibility
	err := dh.CheckDatabase(ctx)
	if err != nil {
		return err
	}
	// Save syncer configs to storage, it override previous ones but keep
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
	err = syncSegments.UpdateToBlock(ctx, dh.blockNotifierManager)
	if err != nil {
		return fmt.Errorf("Initialize: cannot update TargetToBlock in sync segments: %w", err)
	}
	// Get synced segments from storage
	storageSyncSegments, err := dh.storage.GetSyncedBlockRangePerContract(nil)
	if err != nil {
		return err
	}
	// What is pending to download?
	dh.pendingSync = syncSegments.Clone()
	err = dh.pendingSync.SubtractSegments(&storageSyncSegments)
	if err != nil {
		return fmt.Errorf("Initialize: cannot calculate pendingSync: %w", err)
	}
	dh.syncedSegments = storageSyncSegments
	dh.isInitialized = true
	return nil
}

// sync it's an internal function that executes the given stepFunc until it returns done=true or error
func (dh *EVMMultidownloader) sync(ctx context.Context,
	stepFunc func(ctx context.Context) (bool, error), name string) error {
	dh.statistics.StartSyncing()

	iteration := 0
	dh.log.Infof("🚀🚀🚀🚀🚀🚀 start syncing %s ...", name)
	// Execute steps until done or error
	for done, err := stepFunc(ctx); !done; done, err = stepFunc(ctx) {
		if err != nil {
			return err
		}
		iteration++
	}
	dh.log.Infof("🎉🎉🎉🎉🎉 sync %s completed after %d iterations.", name, iteration)
	dh.statistics.FinishSyncing()
	dh.ShowStatistics(iteration)
	return nil
}

// TODO: Implement unsafe mode syncing
// func (dh *EVMMultidownloader) StepUnsafe(ctx context.Context) (bool, error) {
// 	err := dh.pendingSync.UpdateToBlock(ctx, dh.blockNotifierManager)
// 	if err != nil {
// 		return false, fmt.Errorf("Unsafe/Step: cannot update ToBlock in pendingSync: %w", err)
// 	}
// 	if dh.pendingSync.Finished() {
// 		return true, nil
// 	}
// 	committed := false
// 	tx, err := dh.storage.NewTx(ctx)
// 	if err != nil {
// 		return false, fmt.Errorf("Unsafe/Step: cannot create new tx: %w", err)
// 	}
// 	defer func() {
// 		if !committed {
// 			dh.log.Debugf("Unsafe/Step: rolling back tx")
// 			if err := tx.Rollback(); err != nil {
// 				dh.log.Errorf("Unsafe/Step: error rolling back tx: %v", err)
// 			}
// 		}
// 	}()

// 	logQueryData, err := dh.pendingSync.NextQuery(1, 0)
// 	if err != nil {
// 		return false, fmt.Errorf("Unsafe/Step: cannot get next query: %w", err)
// 	}
// 	if logQueryData.BlockRange.CountBlocks() != 1 {
// 		return false, fmt.Errorf("Unsafe/Step: invalid block range for Step: %s", logQueryData.BlockRange.String())
// 	}
// 	blockHeader, err := dh.ethClient.HeaderByNumber(ctx, big.NewInt(int64(logQueryData.BlockRange.ToBlock)))
// 	if err != nil {
// 		return false, fmt.Errorf("Unsafe/Step: cannot get block header for block %d: %w",
// 			logQueryData.BlockRange.ToBlock, err)
// 	}
// 	blockHash := blockHeader.Hash()
// 	rpcFilter := ethereum.FilterQuery{
// 		Addresses: logQueryData.Addrs,
// 		BlockHash: &blockHash,
// 	}

// 	logs, err := dh.ethClient.FilterLogs(ctx, rpcFilter)
// 	if err != nil {
// 		return false, fmt.Errorf("Unsafe/Step: ethClient.FilterLogs: %w", err)
// 	}
// 	dh.log.Infof("Unsafe/Step: reached block %d/%s logs len=%d",
// 		blockHeader.Number.Uint64(), blockHeader.Hash().Hex(), len(logs))
// 	blockHeaders := []*aggkittypes.BlockHeader{aggkittypes.NewBlockHeaderFromEthHeader(blockHeader)}
// 	err = dh.storage.SaveEthLogsWithHeaders(tx, blockHeaders, logs, false)
// 	if err != nil {
// 		return false, fmt.Errorf("Unsafe/Step: cannot save unsafe block: %w", err)
// 	}
// 	newSyncing := dh.pendingSync.UpdateSyncingAfterDoingQuery(logQueryData)
// 	if err = dh.storage.UpdateSyncingStatus(tx, logQueryData); err != nil {
// 		return false, fmt.Errorf("Unsafe/Step: cannot update syncing status: %w", err)
// 	}
// 	committed = true
// 	if err := tx.Commit(); err != nil {
// 		return false, fmt.Errorf("Unsafe/Step: cannot commit tx: %w", err)
// 	}
// 	dh.pendingSync = newSyncing
// 	dh.log.Debugf("Unsafe/Step: finished block=%d syncing=%s",
// 		blockHeader.Number.Uint64(),
// 		dh.pendingSync.String())
// 	err = dh.pendingSync.UpdateToBlock(ctx, dh.blockNotifierManager)
// 	if err != nil {
// 		return false, fmt.Errorf("Unsafe/Step: cannot update ToBlock in pendingSync: %w", err)
// 	}
// 	return dh.pendingSync.Finished(), nil
// }

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
	return dh.syncedSegments.IsAvailable(query)
}

func mapBlockHeadersToList(blocks map[uint64]*aggkittypes.BlockHeader) []*aggkittypes.BlockHeader {
	headers := make([]*aggkittypes.BlockHeader, 0, len(blocks))
	for _, header := range blocks {
		headers = append(headers, header)
	}
	return headers
}

// StepSafe performs a safe step syncing logs and block headers from historical data
func (dh *EVMMultidownloader) StepSafe(ctx context.Context) (bool, error) {
	logs, logQueryData, err := dh.requestLogs(ctx)
	if err != nil {
		if errors.Is(err, mdrtypes.ErrFinished) {
			return true, nil
		}
		return false, fmt.Errorf("Safe/Step: cannot filter logs adapting block range: %w", err)
	}
	dh.log.Debugf("Safe/Step:: logs (%d) for blockRange=%s, addrs=%v", len(logs),
		logQueryData.BlockRange.String(), logQueryData.Addrs)
	blocks := getBlockNumbers(logs)
	dh.log.Debugf("Safe/Step:: querying blockHeaders for %d blocks", len(blocks))
	blockHeaders, err := etherman.RetrieveBlockHeaders(ctx, dh.log, dh.ethClient, dh.rpcClient,
		blocks, dh.cfg.MaxParallelBlockHeaderRetrieval)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot retrieve block headers (%d): %w", len(blockHeaders), err)
	}

	// Calculate new state (not set in memory until commit is successful)
	dh.mutex.Lock()
	newSyncedSegments := dh.syncedSegments.Clone()
	newPendingSegments := dh.pendingSync.Clone()
	dh.mutex.Unlock()
	// Update synced segments
	err = newSyncedSegments.AddLogQuery(logQueryData)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot extend synced segments: %w", err)
	}
	// from pending blocks remove current query
	err = newPendingSegments.SubtractLogQuery(logQueryData)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot subtract log query from pending segments: %w", err)
	}
	// Update ToBlock in pending segments to be able to calculate if finished
	err = newPendingSegments.UpdateToBlock(ctx, dh.blockNotifierManager)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot update ToBlock in pendingSync: %w", err)
	}
	// Store data in storage
	err = dh.storeData(ctx, logs, blockHeaders,
		newSyncedSegments.SegmentsByContract(logQueryData.Addrs), true)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot store data: %w", err)
	}
	// Update in-memory synced segments (after valid commit)
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	dh.syncedSegments = *newSyncedSegments
	dh.pendingSync = newPendingSegments
	finished := dh.pendingSync.Finished()
	dh.log.Infof("Safe/Step: elapsed=%s finished br=%s logs=%d blocksHeaders=%d pendingBlocks=%d ETA=%s ",
		dh.statistics.ElapsedSyncing().String(),
		logQueryData.BlockRange.String(),
		len(logs),
		len(blockHeaders),
		dh.pendingSync.TotalBlocks(),
		dh.statistics.ETA(dh.pendingSync.TotalBlocks()))
	return finished, nil
}
func (dh *EVMMultidownloader) storeData(
	ctx context.Context,
	logs []types.Log, blocks map[uint64]*aggkittypes.BlockHeader,
	updatedSegments []mdrtypes.SyncSegment,
	isFinal bool) error {
	var err error
	committed := false
	dh.statistics.StartDBOperation()
	defer func() {
		dh.statistics.FinishDBOperation(err)
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
	err = dh.storage.SaveEthLogsWithHeaders(tx, mapBlockHeadersToList(blocks), logs, isFinal)
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

func ethGetExtendendError(err error) string {
	if err == nil {
		return ""
	}

	jsonError, ok := err.(ethrpc.DataError) //nolint:errorlint
	if !ok {
		return ""
	}
	return fmt.Sprintf("json_data: %v", jsonError.ErrorData())
}
func isEthClientErrorTooManyResults(err error) bool {
	if err == nil {
		return false
	}
	// Example: "Query returned more than 20000 results. Try with this block range [0x852c16, 0x853273]."
	msg := ethGetExtendendError(err)
	return strings.Contains(msg, "Response size exceeded") || strings.Contains(msg, "Query returned more than")
}

func extractSuggestedBlockRangeFromError(err error) *aggkitcommon.BlockRange {
	if !isEthClientErrorTooManyResults(err) {
		return nil
	}
	msg := ethGetExtendendError(err)
	return extractSuggestedBlockRangeFromErrorMsg(msg)
}

func extractSuggestedBlockRangeFromErrorMsg(msg string) *aggkitcommon.BlockRange {
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

func (dh *EVMMultidownloader) getFinalizedBlockNumber(ctx context.Context) (uint64, error) {
	bn, err := dh.blockNotifierManager.GetCurrentBlockNumber(ctx, dh.cfg.BlockFinality)
	if err != nil {
		return 0, fmt.Errorf("Safe/Step: cannot get finalized block (%s): %w",
			dh.cfg.BlockFinality.String(), err)
	}
	return bn, nil
}

func (dh *EVMMultidownloader) getNextQuery(ctx context.Context, chunck uint32, safe bool) (*mdrtypes.LogQuery, error) {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	var err error
	var maxBlock uint64
	if safe {
		maxBlock, err = dh.getFinalizedBlockNumber(ctx)
		if err != nil {
			return nil, fmt.Errorf("getNextQuery: cannot get finalized block number: %w", err)
		}
	} else {
		maxBlock = 0
	}
	logQueryData, err := dh.pendingSync.NextQuery(chunck, maxBlock)
	if err != nil {
		return nil, fmt.Errorf("getNextQuery: cannot get NextQuery: %w", err)
	}
	return logQueryData, nil
}

func (dh *EVMMultidownloader) requestLogs(
	ctx context.Context) ([]types.Log, *mdrtypes.LogQuery, error) {
	initialsyncBlockChunkSize := dh.cfg.BlockChunkSize
	try := 0
	var err error
	var logQueryData *mdrtypes.LogQuery
	var suggestedBlockRange *aggkitcommon.BlockRange
	for {
		try++

		if suggestedBlockRange == nil {
			logQueryData, err = dh.getNextQuery(ctx, initialsyncBlockChunkSize, safeMode)
			if err != nil {
				return nil, nil, fmt.Errorf("Safe/Step: cannot get NextQuery: %w", err)
			}
		} else {
			logQueryData.BlockRange = *suggestedBlockRange
			dh.log.Warnf("Safe/Step: adjusting block range to suggested by error: %s", logQueryData.BlockRange.String())
		}
		rpcFilterQuery := logQueryData.ToRPCFilterQuery()
		dh.log.Debugf("Safe/Step:: querying logs for %s",
			logQueryData.String())
		dh.statistics.LaunchedEthCall()
		logs, err := dh.ethClient.FilterLogs(ctx, rpcFilterQuery)
		dh.statistics.FinishEthCall(err, uint64(len(logs)), logQueryData.BlockRange.CountBlocks())
		if err == nil {
			return logs, logQueryData, nil
		}
		if err != nil && !isEthClientErrorTooManyResults(err) {
			return nil, nil, fmt.Errorf("Safe/Step: fails ethClient.FilterLogs(%v): %v. err: %w",
				rpcFilterQuery, ethGetExtendendError(err), err)
		}
		suggestedBlockRange = extractSuggestedBlockRangeFromError(err)
		if suggestedBlockRange == nil || !logQueryData.BlockRange.Overlaps(*suggestedBlockRange) {
			prevBlockChunkSize := initialsyncBlockChunkSize
			initialsyncBlockChunkSize /= 10
			if initialsyncBlockChunkSize < 1 {
				return nil, nil, fmt.Errorf("Safe/Step: cannot reduce block chunk size anymore")
			}
			dh.log.Warnf("Safe/Step: too many results for range=%s, addrs=%v, reducing chunk from %d to %d. Err: %s",
				logQueryData.BlockRange.String(), logQueryData.Addrs, prevBlockChunkSize,
				initialsyncBlockChunkSize, ethGetExtendendError(err))
		} else {
			dh.log.Warnf("Safe/Step: too many results for range=%s, addrs=%v, adjusting block range %s. Err: %s",
				logQueryData.BlockRange.String(), logQueryData.Addrs, suggestedBlockRange.String(), ethGetExtendendError(err))
		}
	}
}

func (dh *EVMMultidownloader) ShowStatistics(iteration int) {
	dh.statistics.Show(dh.log.Infof, iteration)
}
