package bridgesync

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/db/compatibility"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/sync"
	tree "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	gethvm "github.com/ethereum/go-ethereum/core/vm"
)

// BridgeDeployment represents the type of bridge contract deployment (sovereign vs non-sovereign).
type BridgeDeployment byte

const (
	Unknown BridgeDeployment = iota
	NonSovereignChain
	SovereignChain
)

// BridgeSyncerID represents the type of bridge syncer
type BridgeSyncerID int

const (
	L1BridgeSyncer BridgeSyncerID = iota
	L2BridgeSyncer

	// CurrentDBVersion represents the current version of the bridge syncer's database schema.
	// It is used to ensure the database is reset if an upgrade requires a full resync.
	// Increment this value whenever the database schema changes in a way that is not backward-compatible.
	CurrentDBVersion = 1
)

func (b BridgeSyncerID) String() string {
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

	// ErrMultidownloaderDepsMissing indicates that the multidownloader backend is selected but the
	// explicit downloader/transaction dependencies were not provided to the constructor.
	ErrMultidownloaderDepsMissing = errors.New(
		"multidownloader selected but explicit downloader/transaction dependencies are missing")
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

	originNetwork uint32
	reorgDetector ReorgDetector
	// reorgDetectorID is the id this syncer is subscribed with in the reorg detector
	reorgDetectorID string
	ethClient       aggkittypes.EthClienter
	agglayerBridge  *agglayerbridge.Agglayerbridge
	cfg             Config
}

// NewL1 creates a bridge syncer that synchronizes the mainnet exit tree using the legacy downloader.
//
// If cfg selects the multidownloader this constructor fails: the multidownloader requires explicit
// downloader and transaction dependencies, so callers must use NewL1WithDownloader instead.
func NewL1(
	ctx context.Context,
	cfg Config,
	rd ReorgDetector,
	ethClient aggkittypes.EthClienter,
	originNetwork uint32,
) (*BridgeSync, error) {
	if err := cfg.Downloader.Validate(); err != nil {
		return nil, fmt.Errorf("bridgesync: invalid Downloader configuration: %w", err)
	}
	if cfg.Downloader.IsMultidownloader() {
		return nil, fmt.Errorf("bridgesync: %w: Downloader=%s requires NewL1WithDownloader",
			ErrMultidownloaderDepsMissing, cfg.Downloader)
	}
	syncFromInBridges := cfg.SyncFromInBridges.Resolved != nil && *cfg.SyncFromInBridges.Resolved
	return newBridgeSync(
		ctx,
		cfg,
		cfg.BlockFinality,
		rd,
		ethClient,
		L1BridgeSyncer,
		originNetwork,
		false,
		syncFromInBridges,
		bridgesynctypes.EmptyLER,
		nil,
	)
}

// NewL1WithDownloader creates an L1 bridge syncer that downloads logs and headers through the shared
// multidownloader and resolves bridge transactions through the shared transaction provider.
//
// It requires cfg.Downloader to select the multidownloader and both backend and provider to be
// non-nil; there is no silent fallback to direct RPC. The syncer is registered into the backend
// synchronously, before the backend is initialized/started, so the backend knows which contract,
// block range and (optional) transaction topics to download.
func NewL1WithDownloader(
	ctx context.Context,
	cfg Config,
	rd ReorgDetector,
	ethClient aggkittypes.EthClienter,
	originNetwork uint32,
	backend aggkittypes.MultiDownloaderLegacy,
	provider aggkittypes.TransactionProvider,
) (*BridgeSync, error) {
	if err := cfg.Downloader.Validate(); err != nil {
		return nil, fmt.Errorf("bridgesync: invalid Downloader configuration: %w", err)
	}
	if !cfg.Downloader.IsMultidownloader() {
		return nil, fmt.Errorf("bridgesync: NewL1WithDownloader requires Downloader=%s in config, got %q",
			cfgtypes.Multidownloader, cfg.Downloader)
	}
	if backend == nil {
		return nil, fmt.Errorf("bridgesync: %w: backend is nil", ErrMultidownloaderDepsMissing)
	}
	if provider == nil {
		return nil, fmt.Errorf("bridgesync: %w: transaction provider is nil", ErrMultidownloaderDepsMissing)
	}
	syncFromInBridges := cfg.SyncFromInBridges.Resolved != nil && *cfg.SyncFromInBridges.Resolved
	return newBridgeSync(
		ctx,
		cfg,
		cfg.BlockFinality,
		rd,
		ethClient,
		L1BridgeSyncer,
		originNetwork,
		false,
		syncFromInBridges,
		bridgesynctypes.EmptyLER,
		&multidownloaderDeps{backend: backend, provider: provider},
	)
}

// NewL2 creates a bridge syncer that synchronizes the local exit tree.
//
// The multidownloader is not supported for the L2 bridge syncer, so a config selecting it is
// rejected explicitly instead of being silently ignored.
func NewL2(
	ctx context.Context,
	cfg Config,
	rd ReorgDetector,
	ethClient aggkittypes.EthClienter,
	originNetwork uint32,
	syncFullClaims bool,
	initialLER common.Hash,
) (*BridgeSync, error) {
	if err := cfg.Downloader.Validate(); err != nil {
		return nil, fmt.Errorf("bridgesync: invalid Downloader configuration: %w", err)
	}
	if cfg.Downloader.IsMultidownloader() {
		return nil, fmt.Errorf("bridgesync: multidownloader is not supported for the L2 bridge syncer (Downloader=%s)",
			cfg.Downloader)
	}
	syncFromInBridges := cfg.SyncFromInBridges.Resolved != nil && *cfg.SyncFromInBridges.Resolved
	return newBridgeSync(
		ctx,
		cfg,
		cfg.BlockFinality,
		rd,
		ethClient,
		L2BridgeSyncer,
		originNetwork,
		syncFullClaims,
		syncFromInBridges,
		initialLER,
		nil,
	)
}

// multidownloaderDeps groups the explicit multidownloader dependencies. A nil value means the
// legacy downloader is used.
type multidownloaderDeps struct {
	backend  aggkittypes.MultiDownloaderLegacy
	provider aggkittypes.TransactionProvider
}

func newBridgeSync(
	ctx context.Context,
	cfg Config,
	blockFinality aggkittypes.BlockNumberFinality,
	rd ReorgDetector,
	ethClient aggkittypes.EthClienter,
	syncerID BridgeSyncerID,
	networkID uint32,
	syncFullClaims bool,
	syncFromInBridges bool,
	initialLER common.Hash,
	deps *multidownloaderDeps,
) (*BridgeSync, error) {
	logger := log.WithFields("module", syncerID.String())
	// the same id is used by the driver and the downloader to track blocks and by IsBlockFinalized to query them
	reorgDetectorID := syncerID.String()

	agglayerBridge, err := agglayerbridge.NewAgglayerbridge(cfg.BridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create binding for AgglayerBridge contract: %w", err)
	}

	logger.Infof("Bridge sync %s, syncing full claims: %t, syncFromInBridges: %t",
		syncerID.String(), syncFullClaims, syncFromInBridges)

	err = sanityCheckContract(logger, cfg.BridgeAddr, agglayerBridge)
	if err != nil {
		logger.Errorf("bridge contract on %s address fails sanity check. Err: %w",
			cfg.BridgeAddr.String(), err)
		return nil, err
	}
	database, err := newSqliteDB(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create sqlite database  %s: %w", cfg.DBPath, err)
	}

	processor, err := newProcessor(database, "bridge_sync_"+syncerID.String(), logger, cfg.DBQueryTimeout.Duration)
	if err != nil {
		return nil, err
	}
	processor.initialLER = initialLER

	// Register the syncer in the multidownloader synchronously, before the backend is initialized.
	// The historical FromBlock stays cfg.InitialBlockNum; the transaction requirement starts after the
	// first not-yet-processed block so only future transactions are fetched.
	var txResolver bridgeTxnResolver
	var mdBackend aggkittypes.MultiDownloaderLegacy = sync.NewAdapterEthClientToMultidownloader(ethClient)
	if deps != nil {
		txFromBlock, err := computeTransactionFromBlock(ctx, processor, cfg.InitialBlockNum)
		if err != nil {
			return nil, err
		}
		syncerConfig := aggkittypes.SyncerConfig{
			SyncerID:          reorgDetectorID,
			ContractAddresses: []common.Address{cfg.BridgeAddr},
			FromBlock:         cfg.InitialBlockNum,
			ToBlock:           blockFinality,
			TransactionRequirements: aggkittypes.TransactionRequirement{
				Enabled:   true,
				FromBlock: txFromBlock,
				Topic0:    []common.Hash{bridgeEventSignature},
			},
		}
		if err := deps.backend.RegisterSyncer(syncerConfig); err != nil {
			return nil, fmt.Errorf("failed to register %s in multidownloader: %w", reorgDetectorID, err)
		}
		logger.Infof("registered %s in multidownloader: blockFinality=%s fromBlock=%d txFromBlock=%d",
			reorgDetectorID, blockFinality.String(), cfg.InitialBlockNum, txFromBlock)
		mdBackend = deps.backend
		txResolver = &providerTxnResolver{provider: deps.provider}
	}

	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: cfg.MaxRetryAttemptsAfterError,
		RetryAfterErrorPeriod:      cfg.RetryAfterErrorPeriod.Duration,
	}

	bridgeDeployment, err := resolveBridgeDeployment(ctx, cfg.BridgeAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve bridge deployment. Reason: %w", err)
	}

	appender, err := buildAppender(ctx, ethClient, cfg.BridgeAddr, syncFromInBridges, bridgeDeployment, logger, txResolver)
	if err != nil {
		return nil, err
	}
	downloader, err := sync.NewEVMDownloader(
		syncerID.String(),
		mdBackend,
		cfg.SyncBlockChunkSize,
		blockFinality,
		cfg.WaitForNewBlocksPeriod.Duration,
		appender,
		[]common.Address{cfg.BridgeAddr},
		rh,
		rd.GetFinalizedBlockType(),
		rd,              // reorgDetector
		reorgDetectorID, // reorgDetectorID
	)
	if err != nil {
		return nil, err
	}
	if deps != nil {
		// Warm the transaction cache with the active Download context so the appender can read the
		// transactions from the cache normally. If the downloader does not expose the enrichment hook
		// (yet) the appender still resolves transactions lazily through the provider.
		if !registerLogsEnricher(downloader, bridgeLogsEnricher(deps.provider)) {
			logger.Debugf("downloader does not expose SetLogsEnricher; " +
				"transactions will be warmed lazily by the appender")
		}
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
				ChainID:           tmp.ChainID,
				Addresses:         tmp.Addresses,
				DBVersion:         &ver,
				SyncFromInBridges: &syncFromInBridges,
			}, nil
		},
		processor)

	driver, err := sync.NewEVMDriver(rd, processor, downloader, reorgDetectorID,
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
			"  waitForNewBlocksPeriod: %s\n"+
			"  syncFromInBridges: %t",
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
		syncFromInBridges,
	)

	return &BridgeSync{
		processor:       processor,
		driver:          driver,
		downloader:      downloader,
		originNetwork:   networkID,
		reorgDetector:   rd,
		reorgDetectorID: reorgDetectorID,
		ethClient:       ethClient,
		agglayerBridge:  agglayerBridge,
		cfg:             cfg,
	}, nil
}

// lastProcessedBlockReader exposes the processor checkpoint needed to compute the transaction frontier.
// The concrete *processor satisfies it; tests can substitute lightweight fakes to exercise checkpoint
// values a real storage driver would reject (for example block numbers with the high bit set).
type lastProcessedBlockReader interface {
	GetLastProcessedBlock(ctx context.Context) (uint64, bool, error)
}

// computeTransactionFromBlock returns the first block for which the multidownloader must fetch the
// transactions of the synced bridge logs. The processor storage is committed and inclusive up to the
// last processed block L, which is materialized processed history: those blocks and their transactions
// already belong to the synced state. Only the blocks after L are future-only, so L+1 is the first
// block whose transactions still have to be fetched. This does not mean the multidownloader transaction
// cache already holds the history up to L: switching to that backend does not retroactively enrich the
// transactions of already processed blocks, so the requirement must not start before L+1. When no block
// has been processed yet (fresh database) the configured InitialBlockNum is used as the starting point.
func computeTransactionFromBlock(
	ctx context.Context, proc lastProcessedBlockReader, initialBlockNum uint64,
) (uint64, error) {
	lastProcessed, found, err := proc.GetLastProcessedBlock(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to read last processed block for transaction requirement: %w", err)
	}
	if !found {
		return initialBlockNum, nil
	}
	if lastProcessed == math.MaxUint64 {
		return 0, fmt.Errorf("last processed block %d overflows the transaction requirement from block",
			lastProcessed)
	}
	return lastProcessed + 1, nil
}

type bridgeDeployment struct {
	kind             BridgeDeployment
	agglayerBridge   *agglayerbridge.Agglayerbridge
	agglayerBridgeL2 *agglayerbridgel2.Agglayerbridgel2
}

// resolveBridgeDeployment resolves which bridge contract flavor is deployed:
// AgglayerBridge => NonSovereign bridge
// AgglayerBridgeL2 => Sovereign bridge
func resolveBridgeDeployment(ctx context.Context,
	bridgeAddr common.Address, backend bind.ContractBackend) (*bridgeDeployment, error) {
	agglayerBridge, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to create AgglayerBridge binding (%s): %w", bridgeAddr, err)
	}

	agglayerBridgeL2, err := agglayerbridgel2.NewAgglayerbridgel2(bridgeAddr, backend)
	if err != nil {
		return nil, fmt.Errorf("failed to create AgglayerBridgeL2 binding (%s): %w", bridgeAddr, err)
	}

	callOpts := &bind.CallOpts{Pending: false, Context: ctx}

	// 1. Try calling bridgeManager function — only exists on AgglayerBridgeL2
	if _, err := agglayerBridgeL2.BridgeManager(callOpts); err == nil {
		return &bridgeDeployment{
			kind:             SovereignChain,
			agglayerBridge:   agglayerBridge,
			agglayerBridgeL2: agglayerBridgeL2,
		}, nil
	} else if !strings.Contains(err.Error(), gethvm.ErrExecutionReverted.Error()) {
		return nil, fmt.Errorf("unexpected error querying AgglayerBridgeL2.BRIDGE_SOVEREIGN_VERSION: %w", err)
	}

	// 2. If that failed, try lastUpdatedDepositCount function — exists on base AgglayerBridge
	if _, err := agglayerBridge.LastUpdatedDepositCount(callOpts); err == nil {
		return &bridgeDeployment{
			kind:             NonSovereignChain,
			agglayerBridge:   agglayerBridge,
			agglayerBridgeL2: agglayerBridgeL2,
		}, nil
	} else if !strings.Contains(err.Error(), gethvm.ErrExecutionReverted.Error()) {
		return nil, fmt.Errorf("unexpected error querying AgglayerBridge.lastUpdatedDepositCount: %w", err)
	}

	return nil, fmt.Errorf("unable to determine bridge contract type at address %s", bridgeAddr)
}

// Start starts the synchronization process
func (s *BridgeSync) Start(ctx context.Context) {
	s.processor.log.Infof("starting bridge synchronizer InitialBlockNum: %d", s.cfg.InitialBlockNum)
	s.driver.Sync(ctx, &s.cfg.InitialBlockNum)
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

func (s *BridgeSync) GetLastProcessedBlock(ctx context.Context) (uint64, bool, error) {
	if s.processor.isHalted() {
		s.processor.log.Error("processor is halted, cannot get last processed block")
		return 0, false, sync.ErrInconsistentState
	}
	return s.processor.GetLastProcessedBlock(ctx)
}

func (s *BridgeSync) GetExitRootByHash(ctx context.Context, root common.Hash) (*tree.Root, error) {
	if s.processor.isHalted() {
		return nil, sync.ErrInconsistentState
	}
	return s.processor.exitTree.GetRootByHash(ctx, root)
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
	// the root and the hash of its block are read in the same statement, so the hash can't be the one
	// of a block that replaced the root's block after a reorg
	return s.processor.getExitTreeRootByHash(ctx, ler)
}

// IsBlockFinalized reports whether the block with the given number and hash, as read from this syncer,
// is finalized, i.e. it can no longer be reorged. The legacy syncer doesn't store a finality flag, but the
// reorg detector tracks every block that was not final when processed and only stops tracking it once its
// hash was checked against the canonical chain and it is below the finalized block. So a processed block
// that is not tracked anymore is a finalized one.
// found is false when it can't be determined: no hash was given, or the block is not stored with that
// same hash (unknown, no hash stored for it, or it was replaced after the caller read it).
func (s *BridgeSync) IsBlockFinalized(ctx context.Context, blockNum uint64,
	blockHash common.Hash) (finalized, found bool, err error) {
	if s.processor.isHalted() {
		return false, false, sync.ErrInconsistentState
	}
	return reorgdetector.IsBlockFinalized(ctx, s.reorgDetector, s.reorgDetectorID, blockNum, blockHash,
		s.processor.getBlockHash)
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

// SubscribeToSync allows a subscriber to receive block notifications
func (s *BridgeSync) SubscribeToSync(subscriberID string) <-chan sync.Block {
	return s.driver.SubscribeToNewBlocks(subscriberID)
}

// SubscribeToNewBridge allows a subscriber to receive block number notifications when new bridge events occur
func (s *BridgeSync) SubscribeToNewBridge(subscriberID string) <-chan uint64 {
	return s.processor.bridgeSubscriber.Subscribe(subscriberID)
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

	depositCount, err := s.agglayerBridge.DepositCount(&bind.CallOpts{Context: ctx})
	if err != nil {
		return 0, fmt.Errorf("failed to get deposit count: %w", err)
	}

	depositCountU64, err := aggkitcommon.SafeUint64(depositCount)
	if err != nil {
		return 0, fmt.Errorf("deposit count: %w", err)
	}
	depositCountU32, err := aggkitcommon.SafeUint32(depositCountU64)
	if err != nil {
		return 0, fmt.Errorf("deposit count: %w", err)
	}

	return depositCountU32, nil
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

// GetBridgeByDepositCount returns the bridge with the given deposit count (bridge or bridge_archive).
func (s *BridgeSync) GetBridgeByDepositCount(ctx context.Context, depositCount uint32) (*Bridge, error) {
	return s.processor.GetBridgeByDepositCount(ctx, depositCount)
}

// GetBridgesByContent returns all bridges matching the given content fields.
func (s *BridgeSync) GetBridgesByContent(
	ctx context.Context,
	leafType uint8,
	originAddress common.Address,
	destinationNetwork uint32,
	destinationAddress common.Address,
	amount *big.Int,
	metadata []byte,
) ([]*Bridge, error) {
	return s.processor.GetBridgesByContent(ctx, leafType, originAddress,
		destinationNetwork, destinationAddress, amount, metadata)
}
