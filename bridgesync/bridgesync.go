package bridgesync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmbridgev2"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
	tree "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// BridgeSyncerType represents the type of bridge syncer
type BridgeSyncerType int

const (
	L1BridgeSyncer BridgeSyncerType = iota
	L2BridgeSyncer
)

func (b BridgeSyncerType) String() string {
	return [...]string{"L1BridgeSyncer", "L2BridgeSyncer"}[b]
}

const (
	downloadBufferSize = 1000
)

var (
	// ErrInvalidPageSize indicates that the page size is invalid
	ErrInvalidPageSize = errors.New("page size must be greater than 0")

	// ErrInvalidPageNumber indicates that the page number is invalid
	ErrInvalidPageNumber = errors.New("page number must be greater than 0")
)

type ReorgDetector interface {
	sync.ReorgDetector
	GetLastReorgEvent(ctx context.Context) (reorgdetector.ReorgEvent, error)
}

// BridgeSync manages the state of the exit tree for the bridge contract by processing Ethereum blockchain events.
type BridgeSync struct {
	processor  *processor
	driver     *sync.EVMDriver
	downloader *sync.EVMDownloader

	originNetwork    uint32
	reorgDetector    ReorgDetector
	blockFinality    aggkittypes.BlockNumberFinality
	ethClient        aggkittypes.EthClienter
	bridgeContractV2 *polygonzkevmbridgev2.Polygonzkevmbridgev2
}

// NewL1 creates a bridge syncer that synchronizes the mainnet exit tree
func NewL1(
	ctx context.Context,
	dbPath string,
	bridge common.Address,
	syncBlockChunkSize uint64,
	blockFinalityType aggkittypes.BlockNumberFinality,
	rd ReorgDetector,
	ethClient aggkittypes.EthClienter,
	initialBlock uint64,
	waitForNewBlocksPeriod time.Duration,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	originNetwork uint32,
	syncFullClaims bool,
	requireStorageContentCompatibility bool,
) (*BridgeSync, error) {
	return newBridgeSync(
		ctx,
		dbPath,
		bridge,
		syncBlockChunkSize,
		blockFinalityType,
		rd,
		ethClient,
		initialBlock,
		L1BridgeSyncer,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		syncFullClaims,
		requireStorageContentCompatibility,
	)
}

// NewL2 creates a bridge syncer that synchronizes the local exit tree
func NewL2(
	ctx context.Context,
	dbPath string,
	bridge common.Address,
	syncBlockChunkSize uint64,
	blockFinalityType aggkittypes.BlockNumberFinality,
	rd ReorgDetector,
	ethClient aggkittypes.EthClienter,
	initialBlock uint64,
	waitForNewBlocksPeriod time.Duration,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	originNetwork uint32,
	syncFullClaims bool,
	requireStorageContentCompatibility bool,
) (*BridgeSync, error) {
	return newBridgeSync(
		ctx,
		dbPath,
		bridge,
		syncBlockChunkSize,
		blockFinalityType,
		rd,
		ethClient,
		initialBlock,
		L2BridgeSyncer,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		syncFullClaims,
		requireStorageContentCompatibility,
	)
}

func newBridgeSync(
	ctx context.Context,
	dbPath string,
	bridge common.Address,
	syncBlockChunkSize uint64,
	blockFinalityType aggkittypes.BlockNumberFinality,
	rd ReorgDetector,
	ethClient aggkittypes.EthClienter,
	initialBlock uint64,
	syncerID BridgeSyncerType,
	waitForNewBlocksPeriod time.Duration,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	originNetwork uint32,
	syncFullClaims bool,
	requireStorageContentCompatibility bool,
) (*BridgeSync, error) {
	logger := log.WithFields("module", syncerID.String())

	bridgeContractV2, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridge, ethClient)
	if err != nil {
		return nil, err
	}

	err = sanityCheckContract(logger, bridge, bridgeContractV2)
	if err != nil {
		logger.Errorf("sanityCheckContract(bridge:%s) fails sanity check. Err: %w",
			bridge.String(), err)
		return nil, err
	}
	processor, err := newProcessor(dbPath, "bridge_sync_"+syncerID.String(), logger)
	if err != nil {
		return nil, err
	}

	lastProcessedBlock, err := processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, err
	}

	if lastProcessedBlock < initialBlock {
		block, err := ethClient.BlockByNumber(ctx, new(big.Int).SetUint64(initialBlock))
		if err != nil {
			return nil, fmt.Errorf("failed to get initial block %d: %w", initialBlock, err)
		}

		err = processor.ProcessBlock(ctx, sync.Block{
			Num:  initialBlock,
			Hash: block.Hash(),
		})
		if err != nil {
			return nil, err
		}
	}
	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: maxRetryAttemptsAfterError,
		RetryAfterErrorPeriod:      retryAfterErrorPeriod,
	}

	appender, err := buildAppender(ethClient, bridge, syncFullClaims, bridgeContractV2)
	if err != nil {
		return nil, err
	}
	downloader, err := sync.NewEVMDownloader(
		syncerID.String(),
		ethClient,
		syncBlockChunkSize,
		blockFinalityType,
		waitForNewBlocksPeriod,
		appender,
		[]common.Address{bridge},
		rh,
		rd.GetFinalizedBlockType(),
	)
	if err != nil {
		return nil, err
	}

	driver, err := sync.NewEVMDriver(rd, processor, downloader, syncerID.String(),
		downloadBufferSize, rh, requireStorageContentCompatibility)
	if err != nil {
		return nil, err
	}

	logger.Infof(
		"%s created:\n"+
			"  dbPath: %s\n"+
			"  initialBlock: %d\n"+
			"  bridgeAddr: %s\n"+
			"  syncFullClaims: %t\n"+
			"  maxRetryAttemptsAfterError: %d\n"+
			"  retryAfterErrorPeriod: %s\n"+
			"  syncBlockChunkSize: %d\n"+
			"  ReorgDetector: %s\n"+
			"  waitForNewBlocksPeriod: %s",
		syncerID,
		dbPath,
		initialBlock,
		bridge.String(),
		syncFullClaims,
		maxRetryAttemptsAfterError,
		retryAfterErrorPeriod.String(),
		syncBlockChunkSize,
		rd.String(),
		waitForNewBlocksPeriod.String(),
	)

	return &BridgeSync{
		processor:        processor,
		driver:           driver,
		downloader:       downloader,
		originNetwork:    originNetwork,
		reorgDetector:    rd,
		blockFinality:    blockFinalityType,
		ethClient:        ethClient,
		bridgeContractV2: bridgeContractV2,
	}, nil
}

func (s *BridgeSync) GetClaimsPaged(
	ctx context.Context,
	page, pageSize uint32, networkIDs []uint32, fromAddress string) ([]*Claim, int, error) {
	if s.processor.isHalted() {
		s.processor.log.Error("processor is halted, cannot get claims")
		return nil, 0, sync.ErrInconsistentState
	}
	return s.processor.GetClaimsPaged(ctx, page, pageSize, networkIDs, fromAddress)
}

// Start starts the synchronization process
func (s *BridgeSync) Start(ctx context.Context) {
	s.processor.log.Info("starting bridge synchronizer")
	s.driver.Sync(ctx)
}

func (s *BridgeSync) GetBridgesPaged(
	ctx context.Context,
	page, pageSize uint32,
	depositCount *uint64, networkIDs []uint32, fromAddress string) ([]*Bridge, int, error) {
	if s.processor.isHalted() {
		return nil, 0, sync.ErrInconsistentState
	}
	return s.processor.GetBridgesPaged(ctx, page, pageSize, depositCount, networkIDs, fromAddress)
}

func (s *BridgeSync) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	if s.processor.isHalted() {
		s.processor.log.Error("processor is halted, cannot get last processed block")
		return 0, sync.ErrInconsistentState
	}
	return s.processor.GetLastProcessedBlock(ctx)
}

func (s *BridgeSync) GetBridgeRootByHash(ctx context.Context, root common.Hash) (*tree.Root, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.exitTree.GetRootByHash(ctx, root)
}

func (s *BridgeSync) GetClaims(ctx context.Context, fromBlock, toBlock uint64) ([]Claim, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetClaims(ctx, fromBlock, toBlock)
}

func (s *BridgeSync) GetBridges(ctx context.Context, fromBlock, toBlock uint64) ([]Bridge, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetBridges(ctx, fromBlock, toBlock)
}

func (s *BridgeSync) GetTokenMappings(ctx context.Context, pageNumber, pageSize uint32) ([]*TokenMapping, int, error) {
	if s.processor.isHalted() {
		return nil, 0, sync.ErrInconsistentState
	}

	if pageNumber == 0 {
		return nil, 0, ErrInvalidPageNumber
	}

	if pageSize == 0 {
		return nil, 0, ErrInvalidPageSize
	}

	return s.processor.GetTokenMappings(ctx, pageNumber, pageSize)
}

func (s *BridgeSync) GetLegacyTokenMigrations(
	ctx context.Context, pageNumber, pageSize uint32) ([]*LegacyTokenMigration, int, error) {
	if s.processor.isHalted() {
		return nil, 0, sync.ErrInconsistentState
	}

	if pageNumber == 0 {
		return nil, 0, ErrInvalidPageNumber
	}

	if pageSize == 0 {
		return nil, 0, ErrInvalidPageSize
	}

	return s.processor.GetLegacyTokenMigrations(ctx, pageNumber, pageSize)
}

func (s *BridgeSync) GetProof(ctx context.Context, depositCount uint32, localExitRoot common.Hash) (tree.Proof, error) {
	if s.processor.isHalted() {
		return tree.Proof{}, sync.ErrInconsistentState
	}
	return s.processor.exitTree.GetProof(ctx, depositCount, localExitRoot)
}

func (s *BridgeSync) GetBlockByLER(ctx context.Context, ler common.Hash) (uint64, error) {
	if s.processor.isHalted() {
		return 0, sync.ErrInconsistentState
	}
	root, err := s.processor.exitTree.GetRootByHash(ctx, ler)
	if err != nil {
		return 0, err
	}
	return root.BlockNum, nil
}

func (s *BridgeSync) GetRootByLER(ctx context.Context, ler common.Hash) (*tree.Root, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	root, err := s.processor.exitTree.GetRootByHash(ctx, ler)
	if err != nil {
		return root, err
	}
	return root, nil
}

// GetExitRootByIndex returns the root of the exit tree at the moment the leaf with the given index was added
func (s *BridgeSync) GetExitRootByIndex(ctx context.Context, index uint32) (tree.Root, error) {
	if s.processor.isHalted() {
		return tree.Root{}, sync.ErrInconsistentState
	}
	return s.processor.exitTree.GetRootByIndex(ctx, index)
}

// OriginNetwork returns the network ID of the origin chain
func (s *BridgeSync) OriginNetwork() uint32 {
	return s.originNetwork
}

// BlockFinality returns the block finality type
func (s *BridgeSync) BlockFinality() aggkittypes.BlockNumberFinality {
	return s.blockFinality
}

type LastReorg struct {
	DetectedAt int64  `json:"detected_at"`
	FromBlock  uint64 `json:"from_block"`
	ToBlock    uint64 `json:"to_block"`
}

func (s *BridgeSync) GetLastReorgEvent(ctx context.Context) (*LastReorg, error) {
	rEvent, err := s.reorgDetector.GetLastReorgEvent(ctx)
	if err != nil {
		s.processor.log.Errorf("failed to get last reorg event: %v", err)
		return nil, err
	}

	return &LastReorg{
		DetectedAt: rEvent.DetectedAt,
		FromBlock:  rEvent.FromBlock,
		ToBlock:    rEvent.ToBlock,
	}, nil
}

func sanityCheckContract(logger *log.Logger, bridgeAddr common.Address,
	bridgeContractV2 *polygonzkevmbridgev2.Polygonzkevmbridgev2) error {
	lastUpdatedDespositCount, err := bridgeContractV2.LastUpdatedDepositCount(nil)
	if err != nil {
		logger.Error("failed to get last updated deposit count", "error", err)
		return fmt.Errorf("sanityCheckContract(bridge:%s) fails getting lastUpdatedDespositCount. Err: %w",
			bridgeAddr.String(), err)
	}
	logger.Infof("sanityCheckContract(bridge:%s) OK. lastUpdatedDespositCount: %d",
		bridgeAddr.String(), lastUpdatedDespositCount)
	return nil
}

// GetContractDepositCount returns the last deposit count from the bridge contract
func (s *BridgeSync) GetContractDepositCount(ctx context.Context) (uint32, error) {
	if s.processor.isHalted() {
		return 0, sync.ErrInconsistentState
	}

	depositCount, err := s.bridgeContractV2.DepositCount(nil)
	if err != nil {
		return 0, fmt.Errorf("failed to get deposit count: %w", err)
	}

	return uint32(depositCount.Int64()), nil
}

// IsEmpty returns true if the bridge database is empty (no data has been processed)
func (s *BridgeSync) IsEmpty(ctx context.Context) (bool, error) {
	if s.processor.isHalted() {
		return false, sync.ErrInconsistentState
	}

	lastProcessedBlock, err := s.processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return false, err
	}

	return lastProcessedBlock == 0, nil
}

func (s *BridgeSync) GetDatabase() *sql.DB {
	return s.processor.db
}
