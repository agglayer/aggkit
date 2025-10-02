package bridgesync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/db/compatibility"
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

	// CurrentDBVersion represents the current version of the bridge syncer's database schema.
	// It is used to ensure the database is reset if an upgrade requires a full resync.
	// Increment this value whenever the database schema changes in a way that is not backward-compatible.
	CurrentDBVersion = 1
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

	originNetwork  uint32
	reorgDetector  ReorgDetector
	ethClient      aggkittypes.EthClienter
	agglayerBridge *agglayerbridge.Agglayerbridge
}

// NewL1 creates a bridge syncer that synchronizes the mainnet exit tree
func NewL1(
	ctx context.Context,
	cfg Config,
	rd ReorgDetector,
	ethClient aggkittypes.EthClienter,
	originNetwork uint32,
	syncFullClaims bool,
) (*BridgeSync, error) {
	return newBridgeSync(
		ctx,
		cfg,
		cfg.BlockFinality,
		rd,
		ethClient,
		L1BridgeSyncer,
		originNetwork,
		syncFullClaims,
	)
}

func NewL2ReadOnly(
	ctx context.Context,
	dbPath string,
	originNetwork uint32,
	dbQueryTimeout time.Duration,
) (*BridgeSync, error) {
	syncerID := L2BridgeSyncer
	logger := log.WithFields("module", syncerID.String())
	processor, err := newProcessor(dbPath, "bridge_sync_"+syncerID.String(), logger, dbQueryTimeout)
	if err != nil {
		return nil, err
	}
	return &BridgeSync{
		processor:     processor,
		originNetwork: originNetwork,
	}, nil
}

// NewL2 creates a bridge syncer that synchronizes the local exit tree
func NewL2(
	ctx context.Context,
	cfg Config,
	rd ReorgDetector,
	ethClient aggkittypes.EthClienter,
	originNetwork uint32,
	syncFullClaims bool,
) (*BridgeSync, error) {
	return newBridgeSync(
		ctx,
		cfg,
		cfg.BlockFinality,
		rd,
		ethClient,
		L2BridgeSyncer,
		originNetwork,
		syncFullClaims,
	)
}

func newBridgeSync(
	ctx context.Context,
	cfg Config,
	blockFinality aggkittypes.BlockNumberFinality,
	rd ReorgDetector,
	ethClient aggkittypes.EthClienter,
	syncerID BridgeSyncerType,
	networkID uint32,
	syncFullClaims bool,
) (*BridgeSync, error) {
	logger := log.WithFields("module", syncerID.String())

	agglayerBridge, err := agglayerbridge.NewAgglayerbridge(cfg.BridgeAddr, ethClient)
	if err != nil {
		return nil, err
	}

	logger.Infof("Bridge sync %s, syncing full claims: %t", syncerID.String(), syncFullClaims)

	err = sanityCheckContract(logger, cfg.BridgeAddr, agglayerBridge)
	if err != nil {
		logger.Errorf("sanityCheckContract(bridge: %s) fails sanity check. Err: %w",
			cfg.BridgeAddr.String(), err)
		return nil, err
	}

	processor, err := newProcessor(cfg.DBPath, "bridge_sync_"+syncerID.String(), logger, cfg.DBQueryTimeout.Duration)
	if err != nil {
		return nil, err
	}

	lastProcessedBlock, err := processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, err
	}

	if lastProcessedBlock < cfg.InitialBlockNum {
		block, err := ethClient.BlockByNumber(ctx, new(big.Int).SetUint64(cfg.InitialBlockNum))
		if err != nil {
			return nil, fmt.Errorf("failed to get initial block %d: %w", cfg.InitialBlockNum, err)
		}

		err = processor.ProcessBlock(ctx, sync.Block{
			Num:  cfg.InitialBlockNum,
			Hash: block.Hash(),
		})
		if err != nil {
			return nil, err
		}
	}
	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: cfg.MaxRetryAttemptsAfterError,
		RetryAfterErrorPeriod:      cfg.RetryAfterErrorPeriod.Duration,
	}

	appender, err := buildAppender(ethClient, cfg.BridgeAddr, syncFullClaims, agglayerBridge, logger)
	if err != nil {
		return nil, err
	}
	downloader, err := sync.NewEVMDownloader(
		syncerID.String(),
		ethClient,
		cfg.SyncBlockChunkSize,
		blockFinality,
		cfg.WaitForNewBlocksPeriod.Duration,
		appender,
		[]common.Address{cfg.BridgeAddr},
		rh,
		rd.GetFinalizedBlockType(),
	)
	if err != nil {
		return nil, err
	}
	compatibilityChecker := compatibility.NewCompatibilityCheck(
		cfg.RequireStorageContentCompatibility,
		func(ctx context.Context) (BridgeSyncRuntimeData, error) {
			tmp, err := downloader.RuntimeData(ctx)
			if err != nil {
				return BridgeSyncRuntimeData{}, fmt.Errorf("failed to get runtime data: %w", err)
			}
			ver := CurrentDBVersion
			return BridgeSyncRuntimeData{
				ChainID:   tmp.ChainID,
				Addresses: tmp.Addresses,
				DBVersion: &ver,
			}, nil
		},
		processor)

	driver, err := sync.NewEVMDriver(rd, processor, downloader, syncerID.String(),
		downloadBufferSize, rh, compatibilityChecker)
	if err != nil {
		return nil, err
	}

	logger.Infof(
		"%s created:\n"+
			"  dbPath: %s\n"+
			"  initialBlock: %d\n"+
			"  blockFinality: %s\n"+
			"  bridgeAddr: %s\n"+
			"  syncFullClaims: %t\n"+
			"  maxRetryAttemptsAfterError: %d\n"+
			"  retryAfterErrorPeriod: %s\n"+
			"  syncBlockChunkSize: %d\n"+
			"  ReorgDetector: %s\n"+
			"  waitForNewBlocksPeriod: %s",
		syncerID,
		cfg.DBPath,
		cfg.InitialBlockNum,
		blockFinality.String(),
		cfg.BridgeAddr.String(),
		syncFullClaims,
		cfg.MaxRetryAttemptsAfterError,
		cfg.RetryAfterErrorPeriod.String(),
		cfg.SyncBlockChunkSize,
		rd.String(),
		cfg.WaitForNewBlocksPeriod.String(),
	)

	return &BridgeSync{
		processor:      processor,
		driver:         driver,
		downloader:     downloader,
		originNetwork:  networkID,
		reorgDetector:  rd,
		ethClient:      ethClient,
		agglayerBridge: agglayerBridge,
	}, nil
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

func (s *BridgeSync) GetClaimsPaged(
	ctx context.Context,
	page, pageSize uint32, networkIDs []uint32, fromAddress string, globalIndex *big.Int) ([]*Claim, int, error) {
	if s.processor.isHalted() {
		s.processor.log.Error("processor is halted, cannot get claims")
		return nil, 0, sync.ErrInconsistentState
	}
	return s.processor.GetClaimsPaged(ctx, page, pageSize, networkIDs, fromAddress, globalIndex)
}

func (s *BridgeSync) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	if s.processor.isHalted() {
		s.processor.log.Error("processor is halted, cannot get last processed block")
		return 0, sync.ErrInconsistentState
	}
	return s.processor.GetLastProcessedBlock(ctx)
}

func (s *BridgeSync) GetExitRootByHash(ctx context.Context, root common.Hash) (*tree.Root, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.exitTree.GetRootByHash(ctx, root)
}

func (s *BridgeSync) GetClaimsByGlobalIndex(ctx context.Context, globalIndex *big.Int) ([]Claim, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetClaimsByGlobalIndex(ctx, globalIndex)
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

func (s *BridgeSync) GetTokenMappings(ctx context.Context, pageNumber, pageSize uint32, originTokenAddress string,
) ([]*TokenMapping, int, error) {
	if s.processor.isHalted() {
		return nil, 0, sync.ErrInconsistentState
	}

	if pageNumber == 0 {
		return nil, 0, ErrInvalidPageNumber
	}

	if pageSize == 0 {
		return nil, 0, ErrInvalidPageSize
	}

	return s.processor.GetTokenMappings(ctx, pageNumber, pageSize, originTokenAddress)
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

func (s *BridgeSync) GetLastRoot(ctx context.Context) (*tree.Root, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	root, err := s.processor.exitTree.GetLastRoot(s.processor.db)
	if err != nil {
		return nil, err
	}
	return &root, nil
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
	agglayerBridge *agglayerbridge.Agglayerbridge) error {
	lastUpdatedDespositCount, err := agglayerBridge.LastUpdatedDepositCount(nil)
	if err != nil {
		logger.Errorf("failed to get last updated deposit count: %s", err)
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

	depositCount, err := s.agglayerBridge.DepositCount(nil)
	if err != nil {
		return 0, fmt.Errorf("failed to get deposit count: %w", err)
	}

	return uint32(depositCount.Int64()), nil
}

// GetLatestNetworkBlock returns the latest block number from the network
func (s *BridgeSync) GetLatestNetworkBlock(ctx context.Context) (uint64, error) {
	if s.processor.isHalted() {
		return 0, sync.ErrInconsistentState
	}

	blockNumber, err := s.ethClient.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get latest block number: %w", err)
	}

	return blockNumber, nil
}

// IsActive returns true if the syncer is active (not halted)
func (s *BridgeSync) IsActive(ctx context.Context) bool {
	return !s.processor.isHalted()
}
