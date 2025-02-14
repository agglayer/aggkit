package bridgesync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/etrog/polygonzkevmbridgev2"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	l1BridgeSyncer     = "L1BridgeSyncer"
	l2BridgeSyncer     = "L2BridgeSyncer"
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
}

// BridgeSync manages the state of the exit tree for the bridge contract by processing Ethereum blockchain events.
type BridgeSync struct {
	processor  *processor
	driver     *sync.EVMDriver
	downloader *sync.EVMDownloader

	originNetwork uint32
	reorgDetector ReorgDetector
	blockFinality etherman.BlockNumberFinality
}

// NewL1 creates a bridge syncer that synchronizes the mainnet exit tree
func NewL1(
	ctx context.Context,
	dbPath string,
	bridge common.Address,
	syncBlockChunkSize uint64,
	blockFinalityType etherman.BlockNumberFinality,
	rd ReorgDetector,
	ethClient EthClienter,
	initialBlock uint64,
	waitForNewBlocksPeriod time.Duration,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	originNetwork uint32,
	syncFullClaims bool,
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
		l1BridgeSyncer,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		syncFullClaims,
	)
}

// NewL2 creates a bridge syncer that synchronizes the local exit tree
func NewL2(
	ctx context.Context,
	dbPath string,
	bridge common.Address,
	syncBlockChunkSize uint64,
	blockFinalityType etherman.BlockNumberFinality,
	rd ReorgDetector,
	ethClient EthClienter,
	initialBlock uint64,
	waitForNewBlocksPeriod time.Duration,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	originNetwork uint32,
	syncFullClaims bool,
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
		l2BridgeSyncer,
		waitForNewBlocksPeriod,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		originNetwork,
		syncFullClaims,
	)
}

func newBridgeSync(
	ctx context.Context,
	dbPath string,
	bridge common.Address,
	syncBlockChunkSize uint64,
	blockFinalityType etherman.BlockNumberFinality,
	rd ReorgDetector,
	ethClient EthClienter,
	initialBlock uint64,
	syncerID string,
	waitForNewBlocksPeriod time.Duration,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	originNetwork uint32,
	syncFullClaims bool,
) (*BridgeSync, error) {
	logger := log.WithFields("module", syncerID)

	err := sanityCheckContract(logger, bridge, ethClient)
	if err != nil {
		logger.Errorf("sanityCheckContract(bridge:%s) fails sanity check. Err: %w",
			bridge.String(), err)
		return nil, err
	}
	processor, err := newProcessor(dbPath, logger)
	if err != nil {
		return nil, err
	}

	lastProcessedBlock, err := processor.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, err
	}

	if lastProcessedBlock < initialBlock {
		err = processor.ProcessBlock(ctx, sync.Block{
			Num: initialBlock,
		})
		if err != nil {
			return nil, err
		}
	}
	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: maxRetryAttemptsAfterError,
		RetryAfterErrorPeriod:      retryAfterErrorPeriod,
	}

	appender, err := buildAppender(ethClient, bridge, syncFullClaims)
	if err != nil {
		return nil, err
	}
	downloader, err := sync.NewEVMDownloader(
		syncerID,
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

	driver, err := sync.NewEVMDriver(rd, processor, downloader, syncerID, downloadBufferSize, rh)
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
		processor:     processor,
		driver:        driver,
		downloader:    downloader,
		originNetwork: originNetwork,
		reorgDetector: rd,
		blockFinality: blockFinalityType,
	}, nil
}

func (s *BridgeSync) GetClaimsPaged(
	ctx context.Context,
	page, pageSize uint32,
) ([]*Claim, int, error) {
	if s.processor.isHalted() {
		return nil, 0, sync.ErrInconsistentState
	}
	return s.processor.GetClaimsPaged(ctx, page, pageSize)
}

// Start starts the synchronization process
func (s *BridgeSync) Start(ctx context.Context) {
	s.driver.Sync(ctx)
}

func (s *BridgeSync) GetBridgesPaged(
	ctx context.Context,
	page, pageSize uint32,
	depositCount *uint64,
) ([]*BridgeResponse, int, error) {
	if s.processor.isHalted() {
		return nil, 0, sync.ErrInconsistentState
	}
	return s.processor.GetBridgesPaged(ctx, page, pageSize, depositCount)
}

func (s *BridgeSync) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	if s.processor.isHalted() {
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

func (s *BridgeSync) GetBridgesPublished(ctx context.Context, fromBlock, toBlock uint64) ([]Bridge, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.GetBridgesPublished(ctx, fromBlock, toBlock)
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
func (s *BridgeSync) BlockFinality() etherman.BlockNumberFinality {
	return s.blockFinality
}

func sanityCheckContract(logger *log.Logger, bridgeAddr common.Address, ethClient EthClienter) error {
	contract, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeAddr, ethClient)
	if err != nil {
		return fmt.Errorf("sanityCheckContract(bridge:%s) fails creating contract. Err: %w", bridgeAddr.String(), err)
	}
	lastUpdatedDespositCount, err := contract.LastUpdatedDepositCount(nil)
	if err != nil {
		return fmt.Errorf("sanityCheckContract(bridge:%s) fails getting lastUpdatedDespositCount. Err: %w",
			bridgeAddr.String(), err)
	}
	logger.Infof("sanityCheckContract(bridge:%s) OK. lastUpdatedDespositCount: %d",
		bridgeAddr.String(), lastUpdatedDespositCount)
	return nil
}
