package multidownloader

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	dbtypes "github.com/agglayer/aggkit/db/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/core/types"
	ethrpc "github.com/ethereum/go-ethereum/rpc"
)

const syncBlockChunkSize = uint32(10000)

type StorageInterface interface {
	// GetSyncedBlockRangePerContract It returns the synced block range stored in DB
	GetSyncedBlockRangePerContract(tx dbtypes.Querier) (mdrtypes.SetSyncSegment, error)
	// TODO: Change ethereum type types.Log to aggkittypes.Log
	SaveEthLogs(tx dbtypes.Querier, logs []types.Log, isFinal bool) error
	GetEthLogs(tx dbtypes.Querier, query mdrtypes.LogQuery) ([]types.Log, error)
	UpdateSyncerConfigs(tx dbtypes.Querier, configs []mdrtypes.ContractConfig) error
	UpdateSyncingStatus(tx dbtypes.Querier, logQuery *mdrtypes.LogQuery) error
	SaveUnsafeBlock(tx dbtypes.Querier, block *types.Header, logs []types.Log) error
	GetBlockHeaderByNumber(tx dbtypes.Querier, blockNumber uint64) (*aggkittypes.BlockHeader, error)

	SaveBlockHeader(tx dbtypes.Querier, header *aggkittypes.BlockHeader, isFinal bool) error

	NewTx(ctx context.Context) (dbtypes.Txer, error)
}

type EVMMultidownloader struct {
	log                  aggkitcommon.Logger
	ethClient            aggkittypes.BaseEthereumClienter
	storage              StorageInterface
	blockNotifierManager mdrtypes.BlockNotifierManagerGetter
	blockFinality        aggkittypes.BlockNumberFinality

	syncersConfig mdrtypes.SetSyncerConfig

	mutex         sync.Mutex
	isInitialized bool
	// These are the real segments that we are pendingSync
	pendingSync *mdrtypes.SetSyncSegment
	// These are the segments that we have already synced
	// when a syncer do a `FilterLogs`is used to check what is already synced
	syncedSegments mdrtypes.SetSyncSegment
	statistics     *Statistics
}

func NewEVMMultidownloader(log aggkitcommon.Logger,
	blockFinality aggkittypes.BlockNumberFinality,
	ethClient aggkittypes.BaseEthereumClienter,
	storage StorageInterface,
	blockNotifierManager mdrtypes.BlockNotifierManagerGetter) *EVMMultidownloader {
	if blockNotifierManager == nil {
		blockNotifierManager = NewBlockNotifierManager(log,
			func(finality aggkittypes.BlockNumberFinality) (mdrtypes.BlockNotifier, error) {
				bn, er := NewBlockNotifierPolling(ethClient, ConfigBlockNotifierPolling{
					BlockFinalityType: finality,
				}, log, nil)
				return bn, er
			})
	}
	return &EVMMultidownloader{
		log:                  log,
		ethClient:            ethClient,
		storage:              storage,
		blockNotifierManager: blockNotifierManager,
		blockFinality:        blockFinality,
		syncersConfig:        mdrtypes.NewSetSyncerConfig(),
		statistics:           NewStatistics(),
	}
}

func (dh *EVMMultidownloader) RegisterSyncer(data aggkittypes.SyncerConfig) {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()

	if dh.isInitialized {
		dh.log.Fatalf("Cannot add new syncer config after initialization")
	}
	dh.syncersConfig.Add(mdrtypes.NewSyncerConfig(data))
}

func (dh *EVMMultidownloader) Start(ctx context.Context) error {
	err := dh.Initialize(ctx)
	if err != nil {
		return err
	}
	err = dh.Sync(ctx, dh.Step)
	if err != nil {
		return err
	}
	// dh.log.Infof("Safe sync completed. Starting tip sync...")
	// err = dh.Sync(ctx, dh.StepTip)
	// if err != nil {
	// 	return err
	// }

	return nil
}

func (dh *EVMMultidownloader) Initialize(ctx context.Context) error {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	if dh.isInitialized {
		return nil
	}
	err := dh.storage.UpdateSyncerConfigs(nil, dh.syncersConfig.ContractConfigs())
	if err != nil {
		return err
	}
	// Get required segments from syncer config
	syncSegments, err := dh.syncersConfig.SyncSegments()
	if err != nil {
		return err
	}

	syncSegments.UpdateToBlock(ctx, dh.blockNotifierManager)
	// Get synced segments from storage
	storageSyncSegments, err := dh.storage.GetSyncedBlockRangePerContract(nil)
	if err != nil {
		return err
	}
	// What is pending to download?
	dh.pendingSync = syncSegments.Substract(&storageSyncSegments)
	dh.syncedSegments = storageSyncSegments
	dh.isInitialized = true
	return nil
}

func (dh *EVMMultidownloader) Sync(ctx context.Context,
	stepFunc func(ctx context.Context) (bool, error)) error {
	dh.statistics.StartSyncing()
	iteration := 0
	// Execute steps until done or error
	for done, err := stepFunc(ctx); !done; done, err = stepFunc(ctx) {
		if err != nil {
			return err
		}
		iteration++

	}
	dh.log.Infof("🎉🎉🎉🎉🎉 Safe sync completed after %d iterations.", iteration)
	dh.statistics.FinishSyncing()
	dh.ShowStatistics(iteration)
	return nil
}

func (dh *EVMMultidownloader) StepTip(ctx context.Context) (bool, error) {
	committed := false
	tx, err := dh.storage.NewTx(ctx)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot create new tx: %w", err)
	}
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	logQueryData, err := dh.pendingSync.NextQuery(1, 0)
	rpcQuery := logQueryData.ToRPCFilterQuery()
	blockHeader, err := dh.ethClient.HeaderByNumber(ctx, rpcQuery.ToBlock)
	if err != nil {
		return false, fmt.Errorf("Unsafe/StepTip: cannot get block header for block %d: %w", rpcQuery.ToBlock.Uint64(), err)
	}
	logs, err := dh.ethClient.FilterLogs(ctx, logQueryData.ToRPCFilterQuery())
	if err != nil {
		return false, fmt.Errorf("Unsafe/StepTip: ethClient.FilterLogs: %w", err)
	}
	dh.log.Infof("Unsafe/StepTip: reached block %d/%s logs len=%d", blockHeader.Number.Uint64(), blockHeader.Hash().Hex(), len(logs))
	err = dh.storage.SaveUnsafeBlock(tx, blockHeader, logs)
	if err != nil {
		return false, fmt.Errorf("Unsafe/StepTip: cannot save unsafe block: %w", err)
	}
	newSyncing := dh.pendingSync.UpdateSyncingAfterDoingQuery(logQueryData)
	if err = dh.storage.UpdateSyncingStatus(tx, logQueryData); err != nil {
		return false, fmt.Errorf("Unsafe/StepTip: cannot update syncing status: %w", err)
	}
	committed = true
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("Unsafe/Step: cannot commit tx: %w", err)
	}
	dh.pendingSync = newSyncing
	dh.log.Debugf("Unsafe/Step: finished block=%d syncing=%s",
		blockHeader.Number.Uint64(),
		dh.pendingSync.String())
	dh.pendingSync.UpdateToBlock(ctx, dh.blockNotifierManager)
	return dh.pendingSync.Finished(), nil
}

func (dh *EVMMultidownloader) Step(ctx context.Context) (bool, error) {
	committed := false
	tx, err := dh.storage.NewTx(ctx)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot create new tx: %w", err)
	}
	defer func() {
		if !committed {
			dh.log.Debugf("Safe/Step: rolling back tx")
			tx.Rollback()
		}
	}()
	logs, logQueryData, err := dh.filterLogsAdaptingBlockRange(ctx)
	if err != nil {
		if errors.Is(err, mdrtypes.ErrFinished) {
			return true, nil
		}
		return false, fmt.Errorf("Safe/Step: cannot filter logs adapting block range: %w", err)
	}

	dh.log.Debugf("Safe/Step:: querying logs for blockRange=%s, addrs=%v",
		logQueryData.BlockRange.String(), logQueryData.Addrs)

	dh.statistics.StartDBOperation()
	defer dh.statistics.FinishDBOperation(errors.New("fails"))
	err = dh.storage.SaveEthLogs(tx, logs, true)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot save eth logs: %w", err)
	}

	if err = dh.storage.UpdateSyncingStatus(tx, logQueryData); err != nil {
		return false, fmt.Errorf("Safe/Step: cannot update syncing status: %w", err)
	}
	committed = true
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("Safe/Step: cannot commit tx: %w", err)
	}
	storageSyncSegments, err := dh.storage.GetSyncedBlockRangePerContract(nil)
	dh.statistics.FinishDBOperation(nil)
	if err != nil {
		return false, fmt.Errorf("Safe/Step: cannot get synced block range per contract: %w", err)
	}

	dh.mutex.Lock()
	defer dh.mutex.Unlock()
	// Update synced segments
	dh.syncedSegments = storageSyncSegments
	dh.pendingSync = dh.pendingSync.UpdateSyncingAfterDoingQuery(logQueryData)
	dh.log.Infof("Safe/Step: finished br=%s logs=%d pendingBlocks=%d ETA=%s",
		logQueryData.BlockRange.String(),
		len(logs),
		dh.pendingSync.TotalBlocks(),
		dh.statistics.ETA(dh.pendingSync.TotalBlocks()))
	dh.pendingSync.UpdateToBlock(ctx, dh.blockNotifierManager)
	return dh.pendingSync.Finished(), nil
}

func ethGetExtendendError(err error) string {
	if err == nil {
		return ""
	}

	jsonError, ok := err.(ethrpc.DataError)
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
		fmt.Println("Block range:", rangeStr)

		// Si quieres separarlos en dos valores
		re2 := regexp.MustCompile(`0x[0-9a-fA-F]+`)
		blocks := re2.FindAllString(rangeStr, -1)
		if len(blocks) == 2 {
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
	bn, err := dh.blockNotifierManager.GetBlockNotifier(ctx, dh.blockFinality)
	if err != nil {
		return 0, fmt.Errorf("Safe/Step: cannot get finalized BlockNotifier: %w", err)
	}
	return bn.GetCurrentBlockNumber(), nil
}

func (dh *EVMMultidownloader) getNextQuery(ctx context.Context, chunck uint32) (*mdrtypes.LogQuery, error) {
	dh.mutex.Lock()
	defer dh.mutex.Unlock()

	maxBlock, err := dh.getFinalizedBlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("Safe/Step: cannot get finalized block number: %w", err)
	}
	logQueryData, err := dh.pendingSync.NextQuery(chunck, maxBlock)
	if err != nil {
		return nil, fmt.Errorf("Safe/Step: cannot get NextQuery: %w", err)
	}
	return logQueryData, nil
}

func (dh *EVMMultidownloader) filterLogsAdaptingBlockRange(ctx context.Context) ([]types.Log, *mdrtypes.LogQuery, error) {
	initialsyncBlockChunkSize := syncBlockChunkSize
	try := 0
	var err error
	var logQueryData *mdrtypes.LogQuery
	var suggestedBlockRange *aggkitcommon.BlockRange
	for {
		try++

		if suggestedBlockRange == nil {
			logQueryData, err = dh.getNextQuery(ctx, initialsyncBlockChunkSize)
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
			initialsyncBlockChunkSize /= 10
			if initialsyncBlockChunkSize < 1 {
				return nil, nil, fmt.Errorf("Safe/Step: cannot reduce block chunk size anymore")
			}

			dh.log.Warnf("Safe/Step: too many results for blockRange=%s, addrs=%v, reducing chunk size from %d to %d. Err: %s",
				logQueryData.BlockRange.String(), logQueryData.Addrs, initialsyncBlockChunkSize*2, initialsyncBlockChunkSize, ethGetExtendendError(err))
		} else {
			dh.log.Warnf("Safe/Step: too many results for blockRange=%s, addrs=%v, adjusting to suggested block range %s. Err: %s",
				logQueryData.BlockRange.String(), logQueryData.Addrs, suggestedBlockRange.String(), ethGetExtendendError(err))
		}
	}

}

func (dh *EVMMultidownloader) ShowStatistics(iteration int) {
	dh.statistics.Show(dh.log.Infof, iteration)
}
