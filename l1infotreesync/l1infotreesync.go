package l1infotreesync

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader"
	mdrsync "github.com/agglayer/aggkit/multidownloader/sync"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/tree"
	"github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
)

const (
	syncerID           = "l1infotreesync"
	downloadBufferSize = 1000
)

type CreationFlags uint64

const (
	FlagNone                        CreationFlags = 1 << iota // Check for correct contracts addresses
	FlagAllowWrongContractsAddrs                              // Allow to set wrong contracts addresses
	FlagStopOnFinalizedBlockReached                           // Stop syncing when finalized block is reached
)

var (
	ErrNotFound = errors.New("l1infotreesync: not found")
)

type DriverInterface interface {
	Sync(ctx context.Context, firstBlockNumber *uint64)
	GetCompletionPercentage() *float64
}

type DownloaderInterface interface {
	Finality() aggkittypes.BlockNumberFinality
}

type L1InfoTreeSync struct {
	processor  *processor
	driver     DriverInterface
	downloader DownloaderInterface
	cfg        Config
}

type RuntimeData = mdrsync.RuntimeData

func NewReadOnly(
	ctx context.Context,
	dbPath string,
) (*L1InfoTreeSync, error) {
	processor, err := newProcessor(dbPath)
	if err != nil {
		return nil, err
	}
	return &L1InfoTreeSync{
		processor: processor,
		driver:    nil,
	}, nil
}

// New creates a L1 Info tree syncer that syncs the L1 info tree and the rollup exit tree
func NewMultidownloadBased(
	ctx context.Context,
	cfg Config,
	l1Multidownloader *multidownloader.EVMMultidownloader,
	flags CreationFlags,
) (*L1InfoTreeSync, error) {
	processor, err := newProcessor(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      cfg.RetryAfterErrorPeriod.Duration,
		MaxRetryAttemptsAfterError: cfg.MaxRetryAttemptsAfterError,
	}

	appender, err := buildAppender(l1Multidownloader.EthClient(), cfg.GlobalExitRootAddr, cfg.RollupManagerAddr, flags)
	if err != nil {
		return nil, err
	}
	addressesToQuery := []common.Address{cfg.GlobalExitRootAddr, cfg.RollupManagerAddr}
	syncerConfig := aggkittypes.SyncerConfig{
		SyncerID:          "l1infotreesync",
		ContractAddresses: addressesToQuery,
		FromBlock:         cfg.InitialBlock,
		ToBlock:           cfg.BlockFinality,
	}
	err = l1Multidownloader.RegisterSyncer(syncerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to register l1infotreesync in multidownloader: %w", err)
	}
	logger := log.WithFields("syncer", syncerID)
	// TODO: move the durations to config file (mdrsync.NewEVMDownloader)
	logger.Infof("Creating L1InfoTreeSync with WaitForNewBlocksPeriod: %s, RetryAfterErrorPeriod: %s",
		cfg.WaitForNewBlocksPeriod.String(),
		cfg.RetryAfterErrorPeriod.String(),
	)
	downloader := mdrsync.NewEVMDownloader(
		l1Multidownloader,
		logger,
		rh,
		appender,
		cfg.RetryAfterErrorPeriod.Duration,
		cfg.WaitForNewBlocksPeriod.Duration,
	)

	compatibilityChecker := compatibility.NewCompatibilityCheck(
		cfg.RequireStorageContentCompatibility,
		func(ctx context.Context) (RuntimeData, error) {
			chainID, err := downloader.ChainID(ctx)
			if err != nil {
				return RuntimeData{}, err
			}
			return RuntimeData{
				ChainID:   chainID,
				Addresses: addressesToQuery,
			}, nil
		},
		compatibility.NewKeyValueToCompatibilityStorage[RuntimeData](
			db.NewKeyValueStorage(processor.getDB()),
			aggkitcommon.L1INFOTREESYNC,
		))

	driver := mdrsync.NewEVMDriver(logger, processor, downloader, syncerConfig,
		cfg.SyncBlockChunkSize, rh, compatibilityChecker)
	if err != nil {
		return nil, err
	}
	return &L1InfoTreeSync{
		processor:  processor,
		driver:     driver,
		downloader: downloader,
		cfg:        cfg,
	}, nil
}

// NewLegacy creates a L1 Info tree syncer that syncs the L1 info tree and the rollup exit tree
func NewLegacy(
	ctx context.Context,
	cfg Config,
	l1Client aggkittypes.MultiDownloaderLegacy,
	reorgDetector sync.ReorgDetector,
	flags CreationFlags,
) (*L1InfoTreeSync, error) {
	processor, err := newProcessor(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	rh := &sync.RetryHandler{
		RetryAfterErrorPeriod:      cfg.RetryAfterErrorPeriod.Duration,
		MaxRetryAttemptsAfterError: cfg.MaxRetryAttemptsAfterError,
	}

	appender, err := buildAppender(l1Client.EthClient(), cfg.GlobalExitRootAddr, cfg.RollupManagerAddr, flags)
	if err != nil {
		return nil, err
	}
	addressesToQuery := []common.Address{cfg.GlobalExitRootAddr, cfg.RollupManagerAddr}
	err = l1Client.RegisterSyncer(
		aggkittypes.SyncerConfig{
			SyncerID:          "l1infotreesync",
			ContractAddresses: addressesToQuery,
			FromBlock:         cfg.InitialBlock,
			ToBlock:           cfg.BlockFinality,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to register l1infotreesync in multidownloader: %w", err)
	}

	downloader, err := sync.NewEVMDownloader(
		"l1infotreesync",
		l1Client,
		cfg.SyncBlockChunkSize,
		cfg.BlockFinality,
		cfg.WaitForNewBlocksPeriod.Duration,
		appender,
		addressesToQuery,
		rh,
		reorgDetector.GetFinalizedBlockType(),
		reorgDetector, // reorgDetector
		syncerID,      // reorgDetectorID
	)
	if err != nil {
		return nil, err
	}
	if flags&FlagStopOnFinalizedBlockReached != 0 {
		downloader.SetStopOnFinalizedBlockReachedFlag()
	}
	compatibilityChecker := compatibility.NewCompatibilityCheck(
		cfg.RequireStorageContentCompatibility,
		downloader.RuntimeData,
		compatibility.NewKeyValueToCompatibilityStorage[sync.RuntimeData](
			db.NewKeyValueStorage(processor.getDB()),
			aggkitcommon.L1INFOTREESYNC,
		))

	driver, err := sync.NewEVMDriver(reorgDetector, processor, downloader, syncerID,
		downloadBufferSize, rh, compatibilityChecker)
	if err != nil {
		return nil, err
	}

	return &L1InfoTreeSync{
		processor:  processor,
		driver:     driver,
		downloader: downloader,
		cfg:        cfg,
	}, nil
}

// Finality returns the block finality of the downloader
func (d *L1InfoTreeSync) Finality() aggkittypes.BlockNumberFinality {
	return d.downloader.Finality()
}

func (d *L1InfoTreeSync) GetCompletionPercentage() *float64 {
	return d.driver.GetCompletionPercentage()
}

// GetRPCServices returns the list of services that the RPC provider exposes
func (a *L1InfoTreeSync) GetRPCServices() []jRPC.Service {
	logger := log.WithFields("module", "l1infotreesync-rpc")
	return []jRPC.Service{
		{
			Name:    "l1infotreesync",
			Service: NewL1InfoTreeSyncRPC(logger, a),
		},
	}
}

// Start starts the synchronization process
func (s *L1InfoTreeSync) Start(ctx context.Context) {
	s.processor.log.Info("starting l1infotreesync")
	s.driver.Sync(ctx, &s.cfg.InitialBlock)
}

// GetRollupExitTreeMerkleProof creates a merkle proof for the rollup exit tree
func (s *L1InfoTreeSync) GetRollupExitTreeMerkleProof(
	ctx context.Context,
	networkID uint32,
	root common.Hash,
) (types.Proof, error) {
	if s.processor.isHalted() {
		return types.Proof{}, sync.ErrInconsistentState
	}
	if networkID == 0 {
		return tree.EmptyProof, nil
	}

	return s.processor.rollupExitTree.GetProof(ctx, networkID-1, root)
}

func translateError(err error) error {
	if errors.Is(err, db.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// GetLatestL1InfoLeafUntilBlock returns the most recent L1InfoTreeLeaf that occurred before or at blockNum.
// If the blockNum has not been processed yet the error ErrBlockNotProcessed will be returned
// It can returns next errors:
// - ErrBlockNotProcessed,
// - ErrNotFound
func (s *L1InfoTreeSync) GetLatestL1InfoLeafUntilBlock(ctx context.Context, blockNum uint64) (*L1InfoTreeLeaf, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	leaf, err := s.processor.GetLatestL1InfoLeafUntilBlock(ctx, &blockNum)
	return leaf, translateError(err)
}

// GetLatestL1InfoLeaf returns the most recent L1InfoTreeLeaf that has been indexed
// It can return the following errors:
// - ErrInconsistentState
// - ErrNotFound
func (s *L1InfoTreeSync) GetLatestL1InfoLeaf(ctx context.Context) (*L1InfoTreeLeaf, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	leaf, err := s.processor.GetLatestL1InfoLeafUntilBlock(ctx, nil)
	return leaf, translateError(err)
}

// GetLatestL1InfoGER returns the most recent Global Exit Root that has been indexed
// It can return the following errors:
// - ErrInconsistentState
// - ErrNotFound
func (s *L1InfoTreeSync) GetLatestL1InfoGER(ctx context.Context) (common.Hash, error) {
	if s.processor.isHalted() {
		return common.Hash{}, sync.ErrInconsistentState
	}
	ger, err := s.processor.GetLatestL1InfoGER(ctx)
	return ger, translateError(err)
}

// GetInfoByIndex returns the value of a leaf (not the hash) of the L1 info tree
func (s *L1InfoTreeSync) GetInfoByIndex(ctx context.Context, index uint32) (*L1InfoTreeLeaf, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetInfoByIndex(ctx, index)
}

// GetL1InfoTreeRootByIndex returns the root of the L1 info tree at the moment the leaf with the given index was added
func (s *L1InfoTreeSync) GetL1InfoTreeRootByIndex(ctx context.Context, index uint32) (types.Root, error) {
	if s.processor.isHalted() {
		return types.Root{}, sync.ErrInconsistentState
	}
	return s.processor.l1InfoTree.GetRootByIndex(ctx, index)
}

// GetLastRollupExitRoot return the last rollup exit root processed
func (s *L1InfoTreeSync) GetLastRollupExitRoot(ctx context.Context) (types.Root, error) {
	if s.processor.isHalted() {
		return types.Root{}, sync.ErrInconsistentState
	}
	return s.processor.rollupExitTree.GetLastRoot(s.processor.db)
}

// GetLastL1InfoTreeRoot return the last root and index processed from the L1 Info tree
func (s *L1InfoTreeSync) GetLastL1InfoTreeRoot(ctx context.Context) (types.Root, error) {
	if s.processor.isHalted() {
		return types.Root{}, sync.ErrInconsistentState
	}
	return s.processor.l1InfoTree.GetLastRoot(s.processor.db)
}

// GetLastProcessedBlock return the last processed block
func (s *L1InfoTreeSync) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	if s.processor.isHalted() {
		return 0, sync.ErrInconsistentState
	}
	num, _, err := s.processor.GetLastProcessedBlock(ctx)
	return num, err
}

func (s *L1InfoTreeSync) GetLocalExitRoot(
	ctx context.Context, networkID uint32, rollupExitRoot common.Hash,
) (common.Hash, error) {
	if s.processor.isHalted() {
		return common.Hash{}, sync.ErrInconsistentState
	}
	if networkID == 0 {
		return common.Hash{}, errors.New("network 0 is not a rollup, and it's not part of the rollup exit tree")
	}

	return s.processor.rollupExitTree.GetLeaf(s.processor.db, networkID-1, rollupExitRoot)
}

func (s *L1InfoTreeSync) GetLastVerifiedBatches(rollupID uint32) (*VerifyBatches, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetLastVerifiedBatches(rollupID)
}

func (s *L1InfoTreeSync) GetFirstVerifiedBatches(rollupID uint32) (*VerifyBatches, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetFirstVerifiedBatches(rollupID)
}

func (s *L1InfoTreeSync) GetFirstVerifiedBatchesAfterBlock(rollupID uint32, blockNum uint64) (*VerifyBatches, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetFirstVerifiedBatchesAfterBlock(rollupID, blockNum)
}

// GetVerifiedBatchesInBlockRange returns all verified-batches rows (both zkEVM
// VerifyBatchesTrustedAggregator and pessimistic VerifyPessimisticStateTransition transitions,
// across all rollups) whose block_num is in the inclusive range [fromBlock, toBlock], ordered by
// block_num ASC, block_pos ASC.
func (s *L1InfoTreeSync) GetVerifiedBatchesInBlockRange(fromBlock, toBlock uint64) ([]*VerifyBatches, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetVerifiedBatchesInBlockRange(fromBlock, toBlock)
}

func (s *L1InfoTreeSync) GetFirstL1InfoWithRollupExitRoot(rollupExitRoot common.Hash) (*L1InfoTreeLeaf, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetFirstL1InfoWithRollupExitRoot(rollupExitRoot)
}

func (s *L1InfoTreeSync) GetLastInfo() (*L1InfoTreeLeaf, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetLastInfo()
}

func (s *L1InfoTreeSync) GetFirstInfo() (*L1InfoTreeLeaf, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetFirstInfo()
}

func (s *L1InfoTreeSync) GetInfoByRoot(root common.Hash) (*L1InfoTreeLeaf, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetInfoByRoot(root)
}

func (s *L1InfoTreeSync) GetFirstInfoAfterBlock(blockNum uint64) (*L1InfoTreeLeaf, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetFirstInfoAfterBlock(blockNum)
}

func (s *L1InfoTreeSync) GetInfoByGlobalExitRoot(ger common.Hash) (*L1InfoTreeLeaf, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetInfoByGlobalExitRoot(ger)
}

// GetL1InfoTreeMerkleProofFromIndexToRoot creates a merkle proof for the L1 Info tree
func (s *L1InfoTreeSync) GetL1InfoTreeMerkleProofFromIndexToRoot(
	ctx context.Context, index uint32, root common.Hash,
) (types.Proof, error) {
	if s.processor.isHalted() {
		return types.Proof{}, sync.ErrInconsistentState
	}
	return s.processor.l1InfoTree.GetProof(ctx, index, root)
}

// GetInitL1InfoRootMap returns the initial L1 info root map, nil if no root map has been set
func (s *L1InfoTreeSync) GetInitL1InfoRootMap(ctx context.Context) (*L1InfoTreeInitial, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetInitL1InfoRootMap(nil)
}

// GetProcessedBlockUntil returns the last block processed before the given block number or
// the exact block num and hash associated to given block if it was processed
func (s *L1InfoTreeSync) GetProcessedBlockUntil(ctx context.Context, blockNum uint64) (uint64, common.Hash, error) {
	if s.processor.isHalted() {
		return 0, common.Hash{}, sync.ErrInconsistentState
	}
	return s.processor.GetProcessedBlockUntil(ctx, blockNum)
}

// IsUpToDate checks if the L1InfoTreeSync is up to date with the finalized L1 blocks
func (s *L1InfoTreeSync) IsUpToDate(ctx context.Context, l1Client aggkittypes.BaseEthereumClienter) (bool, error) {
	if s.processor.isHalted() {
		return false, sync.ErrInconsistentState
	}

	lastProcessedBlock, _, err := s.processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get last processed block: %w", err)
	}

	finalizedBlock, err := l1Client.HeaderByNumber(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber)))
	if err != nil {
		return false, fmt.Errorf("failed to get the latest finalized L1 block: %w", err)
	}

	return lastProcessedBlock >= finalizedBlock.Number.Uint64(), nil
}
