package runtime

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxlog "github.com/0xPolygon/zkevm-ethtx-manager/log"
	aggoracletypes "github.com/agglayer/aggkit/aggoracle/types"
	"github.com/agglayer/aggkit/autoclaim/api"
	"github.com/agglayer/aggkit/autoclaim/bridgedetector"
	"github.com/agglayer/aggkit/autoclaim/claimer"
	autoclaimcfg "github.com/agglayer/aggkit/autoclaim/config"
	"github.com/agglayer/aggkit/autoclaim/policy"
	"github.com/agglayer/aggkit/autoclaim/proof"
	"github.com/agglayer/aggkit/autoclaim/sender"
	"github.com/agglayer/aggkit/autoclaim/simulator"
	autoclaimstorage "github.com/agglayer/aggkit/autoclaim/storage"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

const defaultAutoClaimDBQueryTimeout = 5 * time.Minute

// gerSyncerDirName is the subdirectory (under the AutoClaim storage directory) that holds each
// claimer's isolated l2gersync and reorg detector databases.
const gerSyncerDirName = "autoclaim-gersync"

// defaultGERSyncBlockChunkSize is used when the shared l2gersync template leaves SyncBlockChunkSize
// unset (l2gersync.New rejects a zero chunk size). It mirrors the [L2GERSync] config default.
const defaultGERSyncBlockChunkSize = 100

// gerSyncerDirPerm is the permission used for the per-claimer GER syncer storage directories.
const gerSyncerDirPerm os.FileMode = 0o750

// Dependencies contains runtime dependencies created by cmd startup before Auto Claim starts.
type Dependencies struct {
	Config         autoclaimcfg.Config
	LogConfig      log.Config
	DBQueryTimeout time.Duration
	RESTConfig     aggkitcommon.RESTConfig
	L1BridgeSync   interface {
		autoclaimtypes.BridgeSource
		proof.L1BridgeSyncer
	}
	L1InfoTreeSync interface {
		proof.L1InfoTreeSyncer
		l2gersync.L1InfoTreeQuerier
	}
	// L1Client is the L1 JSON-RPC client shared by every per-claimer l2gersync instance.
	L1Client aggkittypes.EthClienter
	// L2GERSyncConfig is the shared l2gersync template; per-claimer DB path and L2 GER manager
	// address are overridden when each claimer's l2gersync instance is built.
	L2GERSyncConfig l2gersync.Config
	// ReorgDetectorL2Config is the shared L2 reorg detector template; the DB path is overridden
	// per claimer so each destination L2 gets an isolated reorg detector.
	ReorgDetectorL2Config reorgdetector.Config
	Logger                aggkitcommon.Logger
}

// Runtime owns the started Auto Claim components.
type Runtime struct {
	Storage        autoclaimtypes.Storage
	Claimers       []autoclaimtypes.Claimer
	Registry       autoclaimtypes.ClaimerRegistry
	BridgeDetector *bridgedetector.L1ToL2
	// AdminREST registers admin routes (approve/reject) on the shared admin HTTP server.
	AdminREST *api.API
	// PublicREST registers read routes on the shared public HTTP server.
	PublicREST     *api.PublicREST
	EthTxManagers []EthTxManager
}

// EthTxManager is the concrete transaction-manager boundary used by senders and startup lifecycle.
type EthTxManager interface {
	aggoracletypes.EthTxManager
	Start()
	Stop()
}

type ethTxManagerAdapter struct {
	aggoracletypes.EthTxManager
	start func()
	stop  func()
}

func (a ethTxManagerAdapter) Start() {
	a.start()
}

func (a ethTxManagerAdapter) Stop() {
	if a.stop != nil {
		a.stop()
	}
}

// Factories contains injectable constructors for focused startup tests.
type Factories struct {
	OpenStorage  func(aggkitcommon.Logger, string, time.Duration) (autoclaimtypes.Storage, error)
	NewRPCClient func(context.Context, aggkitcommon.Logger, ethermanconfig.RPCClientConfig) (
		aggkittypes.EthClienter, error,
	)
	NewEthTxManager   func(context.Context, autoclaimcfg.ClaimerConfig) (EthTxManager, error)
	StartEthTxManager func(context.Context, EthTxManager)
	NewPolicy         func(
		autoclaimcfg.PolicyName,
		autoclaimcfg.PolicyConfig,
		...policy.RegistryOption,
	) (autoclaimtypes.Policy, error)
	NewTargetClaimReader func(common.Address, aggkittypes.BaseEthereumClienter) (autoclaimtypes.ClaimChecker, error)
	// NewGERSyncer builds the per-claimer destination-L2 GER syncer and returns a start function that
	// runs its reorg detector and sync loop (the start function blocks and is meant to run in a goroutine).
	NewGERSyncer     func(ctx context.Context, deps GERSyncerDeps) (proof.L2GERSyncer, func(context.Context), error)
	NewProofPreparer func(
		l1BridgeSync proof.L1BridgeSyncer,
		l1InfoTreeSync proof.L1InfoTreeSyncer,
		gerSyncer proof.L2GERSyncer,
	) (autoclaimtypes.ProofPreparer, error)
	NewTargetSimulator func(
		simulator.GasEstimator,
		autoclaimtypes.ProofPreparer,
		autoclaimtypes.ClaimerTarget,
		common.Address,
	) (policy.TargetSimulator, error)
	NewSender func(
		autoclaimtypes.Storage,
		EthTxManager,
		autoclaimtypes.ClaimChecker,
	) (autoclaimtypes.ClaimSender, error)
	NewClaimer func(
		autoclaimtypes.ClaimerTarget,
		autoclaimtypes.Storage,
		autoclaimtypes.Policy,
		autoclaimtypes.ProofPreparer,
		autoclaimtypes.ClaimSender,
		...claimer.Option,
	) (autoclaimtypes.Claimer, error)
	StartClaimer      func(context.Context, autoclaimtypes.Claimer)
	NewRegistry       func(...autoclaimtypes.Claimer) (autoclaimtypes.ClaimerRegistry, error)
	NewBridgeDetector func(
		autoclaimtypes.BridgeSource,
		bridgedetector.CursorStore,
		autoclaimtypes.ClaimerRegistry,
		...bridgedetector.Option,
	) (*bridgedetector.L1ToL2, error)
	StartBridgeDetector func(context.Context, *bridgedetector.L1ToL2)
	NewAPI              func(api.Config, api.Storage, autoclaimtypes.ClaimerRegistry, ...api.Option) (*api.API, error)
	Go                  func(func())
}

// DefaultFactories returns production constructors for Auto Claim runtime startup.
func DefaultFactories(logConfig log.Config) Factories {
	return Factories{
		OpenStorage: func(logger aggkitcommon.Logger, dbPath string, dbQueryTimeout time.Duration) (
			autoclaimtypes.Storage, error,
		) {
			return autoclaimstorage.NewStandalone(logger, dbPath, dbQueryTimeout)
		},
		NewRPCClient: etherman.NewRPCClient,
		NewEthTxManager: func(_ context.Context, cfg autoclaimcfg.ClaimerConfig) (EthTxManager, error) {
			ethTxManagerConfig := cfg.EthTxManager
			ethTxManagerConfig.Log = ethtxlog.Config{
				Environment: ethtxlog.LogEnvironment(logConfig.Environment),
				Level:       logConfig.Level,
				Outputs:     logConfig.Outputs,
			}
			txManager, err := ethtxmanager.New(ethTxManagerConfig)
			if err != nil {
				return nil, err
			}
			return ethTxManagerAdapter{
				EthTxManager: txManager,
				start:        txManager.Start,
				stop:         txManager.Stop,
			}, nil
		},
		StartEthTxManager: func(ctx context.Context, txManager EthTxManager) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				txManager.Start()
			}()
			<-ctx.Done()
			txManager.Stop()
			<-done
		},
		NewPolicy:            policy.NewPolicy,
		NewTargetClaimReader: newTargetClaimReader,
		NewGERSyncer:         newGERSyncer,
		NewProofPreparer: func(
			l1BridgeSync proof.L1BridgeSyncer,
			l1InfoTreeSync proof.L1InfoTreeSyncer,
			gerSyncer proof.L2GERSyncer,
		) (autoclaimtypes.ProofPreparer, error) {
			return proof.NewPreparer(l1BridgeSync, l1InfoTreeSync, gerSyncer), nil
		},
		NewTargetSimulator: func(
			client simulator.GasEstimator,
			proofPreparer autoclaimtypes.ProofPreparer,
			target autoclaimtypes.ClaimerTarget,
			from common.Address,
		) (policy.TargetSimulator, error) {
			return simulator.New(client, proofPreparer, target, from)
		},
		NewSender: func(
			storage autoclaimtypes.Storage,
			txManager EthTxManager,
			targetClaimReader autoclaimtypes.ClaimChecker,
		) (autoclaimtypes.ClaimSender, error) {
			return sender.New(storage, txManager, targetClaimReader)
		},
		NewClaimer: func(
			target autoclaimtypes.ClaimerTarget,
			storage autoclaimtypes.Storage,
			policy autoclaimtypes.Policy,
			proofPreparer autoclaimtypes.ProofPreparer,
			claimSender autoclaimtypes.ClaimSender,
			options ...claimer.Option,
		) (autoclaimtypes.Claimer, error) {
			return claimer.New(target, storage, policy, proofPreparer, claimSender, options...)
		},
		StartClaimer: func(ctx context.Context, runtimeClaimer autoclaimtypes.Claimer) {
			starter, ok := runtimeClaimer.(interface {
				Start(context.Context)
			})
			if !ok {
				log.Errorf("Auto Claim claimer %s cannot be started", runtimeClaimer.Target().ID)
				return
			}
			starter.Start(ctx)
		},
		NewRegistry: func(claimers ...autoclaimtypes.Claimer) (autoclaimtypes.ClaimerRegistry, error) {
			return claimer.NewRegistry(claimers...)
		},
		NewBridgeDetector: bridgedetector.NewL1ToL2,
		StartBridgeDetector: func(ctx context.Context, bd *bridgedetector.L1ToL2) {
			bd.Start(ctx)
		},
		NewAPI: api.New,
		Go: func(fn func()) {
			go fn()
		},
	}
}

func hasEnabledClaimer(cfg autoclaimcfg.Config) bool {
	for _, claimer := range cfg.Claimers {
		if claimer.Enabled {
			return true
		}
	}
	return false
}

// Start creates and starts the Auto Claim runtime when enabled.
func Start(ctx context.Context, deps Dependencies, factories Factories) (*Runtime, error) {
	cfg := deps.Config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid AutoClaim config: %w", err)
	}
	// With no enabled claimer there is nothing to run, so the runtime is a no-op (it does not even
	// open storage). Whether Auto Claim runs at all is decided by the process components list.
	if !hasEnabledClaimer(cfg) {
		return nil, nil
	}
	if isNil(deps.L1BridgeSync) {
		return nil, fmt.Errorf("AutoClaim requires l1bridgesync / L1 bridge sync when enabled")
	}
	if isNil(deps.L1InfoTreeSync) {
		return nil, fmt.Errorf("AutoClaim requires l1infotreesync / L1 info tree sync when enabled")
	}

	factories = withDefaultFactories(factories, deps.LogConfig)
	logger := deps.Logger
	if logger == nil {
		logger = log.WithFields("module", aggkitcommon.AUTOCLAIM)
	}
	dbQueryTimeout := deps.DBQueryTimeout
	if dbQueryTimeout <= 0 {
		dbQueryTimeout = defaultAutoClaimDBQueryTimeout
	}

	storage, err := factories.OpenStorage(logger, cfg.StoragePath, dbQueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("open AutoClaim storage: %w", err)
	}

	runtime, gerSyncerStarts, err := createAndRegisterClaimers(ctx, cfg, deps, storage, logger, factories)
	if err != nil {
		return nil, err
	}

	startRuntimeComponents(ctx, runtime, gerSyncerStarts, factories)

	// Public read API — always created when the runtime is active.
	runtime.PublicREST = api.NewPublicREST(storage, deps.RESTConfig.ReadTimeout.Duration)

	// Admin API — only created when enabled.
	if cfg.API.Enabled {
		apiStorage, ok := storage.(api.Storage)
		if !ok {
			return nil, fmt.Errorf("AutoClaim API requires storage with manual decision methods")
		}
		apiServer, err := factories.NewAPI(api.Config{Enabled: true}, apiStorage, runtime.Registry,
			api.WithLogger(logger))
		if err != nil {
			return nil, fmt.Errorf("create AutoClaim admin API: %w", err)
		}
		runtime.AdminREST = apiServer
	}

	logger.Info("Auto Claim started")
	return runtime, nil
}

// createAndRegisterClaimers builds all enabled claimers, registers them, and creates the bridge
// detector. It returns the partially-populated Runtime and the list of GER-syncer start functions.
func createAndRegisterClaimers(
	ctx context.Context,
	cfg autoclaimcfg.Config,
	deps Dependencies,
	storage autoclaimtypes.Storage,
	logger aggkitcommon.Logger,
	factories Factories,
) (*Runtime, []func(context.Context), error) {
	runtime := &Runtime{Storage: storage}
	storageBaseDir := filepath.Dir(cfg.StoragePath)
	claimers := make([]autoclaimtypes.Claimer, 0, len(cfg.Claimers))
	gerSyncerStarts := make([]func(context.Context), 0, len(cfg.Claimers))

	for _, claimerCfg := range cfg.Claimers {
		if !claimerCfg.Enabled {
			continue
		}
		c, txManager, gerSyncerStart, err := createClaimer(
			ctx, claimerCfg, storage, deps, storageBaseDir, logger, factories)
		if err != nil {
			return nil, nil, err
		}
		claimers = append(claimers, c)
		runtime.EthTxManagers = append(runtime.EthTxManagers, txManager)
		gerSyncerStarts = append(gerSyncerStarts, gerSyncerStart)
	}

	registry, err := factories.NewRegistry(claimers...)
	if err != nil {
		return nil, nil, fmt.Errorf("create AutoClaim claimer registry: %w", err)
	}
	runtime.Registry = registry
	runtime.Claimers = claimers

	cursorStore, ok := storage.(bridgedetector.CursorStore)
	if !ok {
		return nil, nil, fmt.Errorf("AutoClaim L1-to-L2 bridge detector requires storage with cursor methods")
	}
	bd, err := factories.NewBridgeDetector(
		deps.L1BridgeSync,
		cursorStore,
		registry,
		bridgedetector.WithEnabled(cfg.L1ToL2BridgeDetector.Enabled),
		bridgedetector.WithStartBlock(cfg.L1ToL2BridgeDetector.StartBlock),
		bridgedetector.WithPollPeriod(cfg.L1ToL2BridgeDetector.PollInterval.Duration),
		bridgedetector.WithEtrogL1UpgradeBlock(cfg.L1ToL2BridgeDetector.EtrogL1UpgradeBlock),
		bridgedetector.WithLogger(logger),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create AutoClaim L1-to-L2 bridge detector: %w", err)
	}
	runtime.BridgeDetector = bd

	return runtime, gerSyncerStarts, nil
}

// startRuntimeComponents launches the goroutines for tx managers, GER syncers, claimers, and the
// bridge detector.
func startRuntimeComponents(
	ctx context.Context,
	runtime *Runtime,
	gerSyncerStarts []func(context.Context),
	factories Factories,
) {
	for _, txManager := range runtime.EthTxManagers {
		txManager := txManager
		factories.Go(func() {
			factories.StartEthTxManager(ctx, txManager)
		})
	}
	for _, gerSyncerStart := range gerSyncerStarts {
		gerSyncerStart := gerSyncerStart
		if gerSyncerStart == nil {
			continue
		}
		factories.Go(func() {
			gerSyncerStart(ctx)
		})
	}
	for _, c := range runtime.Claimers {
		c := c
		factories.Go(func() {
			factories.StartClaimer(ctx, c)
		})
	}
	factories.Go(func() {
		factories.StartBridgeDetector(ctx, runtime.BridgeDetector)
	})
}

func createClaimer(
	ctx context.Context,
	cfg autoclaimcfg.ClaimerConfig,
	storage autoclaimtypes.Storage,
	deps Dependencies,
	storageBaseDir string,
	logger aggkitcommon.Logger,
	factories Factories,
) (autoclaimtypes.Claimer, EthTxManager, func(context.Context), error) {
	target := targetFromConfig(cfg)
	target.DryRun = deps.Config.DryRun
	rpcClientCfg := targetRPCClientConfig(cfg.URLRPC)
	rpcClient, err := factories.NewRPCClient(ctx, logger, rpcClientCfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create AutoClaim RPC client for claimer %s: %w", cfg.ID, err)
	}

	gerSyncer, gerSyncerStart, err := factories.NewGERSyncer(ctx, GERSyncerDeps{
		ClaimerCfg:     cfg,
		SharedGERCfg:   deps.L2GERSyncConfig,
		SharedRDCfg:    deps.ReorgDetectorL2Config,
		StorageBaseDir: storageBaseDir,
		L2Client:       rpcClient,
		L1InfoTreeSync: deps.L1InfoTreeSync,
		L1Client:       deps.L1Client,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create GER syncer for claimer %s: %w", cfg.ID, err)
	}

	proofPreparer, err := factories.NewProofPreparer(deps.L1BridgeSync, deps.L1InfoTreeSync, gerSyncer)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create proof preparer for claimer %s: %w", cfg.ID, err)
	}

	txManager, err := factories.NewEthTxManager(ctx, cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create AutoClaim EthTxManager for claimer %s: %w", cfg.ID, err)
	}
	claimReader, err := factories.NewTargetClaimReader(cfg.BridgeAddr, rpcClient)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create AutoClaim target claim reader for claimer %s: %w", cfg.ID, err)
	}
	claimSender, err := factories.NewSender(storage, txManager, claimReader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create AutoClaim sender for claimer %s: %w", cfg.ID, err)
	}
	claimPolicy, err := newRuntimePolicy(ctx, cfg, rpcClient, proofPreparer, target, claimSender, factories)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create AutoClaim policy for claimer %s: %w", cfg.ID, err)
	}
	targetClaimer, err := factories.NewClaimer(
		target,
		storage,
		claimPolicy,
		proofPreparer,
		claimSender,
		claimer.WithClaimChecker(claimReader),
		claimer.WithPollPeriod(cfg.WaitPeriod.Duration),
		claimer.WithLogger(logger),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create AutoClaim claimer %s: %w", cfg.ID, err)
	}

	return targetClaimer, txManager, gerSyncerStart, nil
}

func withDefaultFactories(factories Factories, logConfig log.Config) Factories {
	defaults := DefaultFactories(logConfig)
	factories = withDefaultComponentFactories(factories, defaults)
	factories = withDefaultLifecycleFactories(factories, defaults)
	return factories
}

// withDefaultComponentFactories fills in nil component-constructor fields from defaults.
func withDefaultComponentFactories(factories, defaults Factories) Factories {
	if factories.OpenStorage == nil {
		factories.OpenStorage = defaults.OpenStorage
	}
	if factories.NewRPCClient == nil {
		factories.NewRPCClient = defaults.NewRPCClient
	}
	if factories.NewEthTxManager == nil {
		factories.NewEthTxManager = defaults.NewEthTxManager
	}
	if factories.NewPolicy == nil {
		factories.NewPolicy = defaults.NewPolicy
	}
	if factories.NewTargetClaimReader == nil {
		factories.NewTargetClaimReader = defaults.NewTargetClaimReader
	}
	if factories.NewGERSyncer == nil {
		factories.NewGERSyncer = defaults.NewGERSyncer
	}
	if factories.NewProofPreparer == nil {
		factories.NewProofPreparer = defaults.NewProofPreparer
	}
	if factories.NewTargetSimulator == nil {
		factories.NewTargetSimulator = defaults.NewTargetSimulator
	}
	if factories.NewSender == nil {
		factories.NewSender = defaults.NewSender
	}
	if factories.NewClaimer == nil {
		factories.NewClaimer = defaults.NewClaimer
	}
	if factories.NewRegistry == nil {
		factories.NewRegistry = defaults.NewRegistry
	}
	if factories.NewBridgeDetector == nil {
		factories.NewBridgeDetector = defaults.NewBridgeDetector
	}
	if factories.NewAPI == nil {
		factories.NewAPI = defaults.NewAPI
	}
	return factories
}

// withDefaultLifecycleFactories fills in nil lifecycle (start/run) fields from defaults.
func withDefaultLifecycleFactories(factories, defaults Factories) Factories {
	if factories.StartEthTxManager == nil {
		factories.StartEthTxManager = defaults.StartEthTxManager
	}
	if factories.StartClaimer == nil {
		factories.StartClaimer = defaults.StartClaimer
	}
	if factories.StartBridgeDetector == nil {
		factories.StartBridgeDetector = defaults.StartBridgeDetector
	}
	if factories.Go == nil {
		factories.Go = defaults.Go
	}
	return factories
}

func newRuntimePolicy(
	_ context.Context,
	cfg autoclaimcfg.ClaimerConfig,
	rpcClient aggkittypes.EthClienter,
	proofPreparer autoclaimtypes.ProofPreparer,
	target autoclaimtypes.ClaimerTarget,
	claimSender autoclaimtypes.ClaimSender,
	factories Factories,
) (autoclaimtypes.Policy, error) {
	if cfg.PolicyName != autoclaimcfg.PolicyNameBasicFilter {
		return factories.NewPolicy(cfg.PolicyName, cfg.Policy)
	}

	if claimSender.EthTxManager() == nil {
		return nil, fmt.Errorf("basic-filter simulator requires sender EthTxManager")
	}
	targetSimulator, err := factories.NewTargetSimulator(
		rpcClient,
		proofPreparer,
		target,
		claimSender.EthTxManager().From(),
	)
	if err != nil {
		return nil, fmt.Errorf("create basic-filter target simulator: %w", err)
	}
	return factories.NewPolicy(cfg.PolicyName, cfg.Policy, policy.WithTargetSimulator(targetSimulator))
}

func targetRPCClientConfig(url string) ethermanconfig.RPCClientConfig {
	cfg := ethermanconfig.NewDefaultRPCClientConfig()
	cfg.URL = url
	cfg.Mode = ethermanconfig.RPCModeBasic
	return *cfg
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func targetFromConfig(cfg autoclaimcfg.ClaimerConfig) autoclaimtypes.ClaimerTarget {
	retryAfter := cfg.RetryAfter.Duration
	if retryAfter <= 0 {
		retryAfter = cfg.WaitPeriod.Duration
	}
	return autoclaimtypes.ClaimerTarget{
		ID:                 cfg.ID,
		DestinationNetwork: cfg.NetworkID,
		NetworkType:        string(cfg.NetworkType),
		BridgeAddr:         cfg.BridgeAddr,
		GasOffset:          cfg.GasOffset,
		WaitPeriod:         cfg.WaitPeriod.Duration,
		RetryAfter:         retryAfter,
		MaxRetries:         cfg.MaxRetries,
	}
}

type targetClaimReader struct {
	bridge claimChecker
}

type claimChecker interface {
	IsClaimed(opts *bind.CallOpts, leafIndex uint32, sourceBridgeNetwork uint32) (bool, error)
}

// GERSyncerDeps groups the non-context dependencies of newGERSyncer to keep the parameter list
// within the project's function-arity limit.
type GERSyncerDeps struct {
	ClaimerCfg     autoclaimcfg.ClaimerConfig
	SharedGERCfg   l2gersync.Config
	SharedRDCfg    reorgdetector.Config
	StorageBaseDir string
	L2Client       aggkittypes.EthClienter
	L1InfoTreeSync l2gersync.L1InfoTreeQuerier
	L1Client       aggkittypes.EthClienter
}

// newGERSyncer builds the per-claimer destination-L2 GER syncer — an l2gersync.L2GERSync instance with
// its own reorg detector and SQLite databases — and returns a start function that runs the reorg
// detector and the GER sync loop. The start function blocks and is intended to run in a goroutine.
//
// The L2 GER manager address is auto-resolved from the destination bridge contract's
// GlobalExitRootManager() getter, so no additional configuration is required. Shared sync settings come
// from deps.SharedGERCfg / deps.SharedRDCfg; the database paths are namespaced per claimer under
// deps.StorageBaseDir so each destination L2 gets isolated storage that never collides with the global
// L2GERSync component.
func newGERSyncer(
	ctx context.Context,
	deps GERSyncerDeps,
) (proof.L2GERSyncer, func(context.Context), error) {
	cfg := deps.ClaimerCfg
	sharedGERCfg := deps.SharedGERCfg
	sharedRDCfg := deps.SharedRDCfg
	storageBaseDir := deps.StorageBaseDir
	l2Client := deps.L2Client
	l1InfoTreeSync := deps.L1InfoTreeSync
	l1Client := deps.L1Client
	bridgeBinding, err := agglayerbridgel2.NewAgglayerbridgel2(cfg.BridgeAddr, l2Client)
	if err != nil {
		return nil, nil, fmt.Errorf("create bridge binding for claimer %s: %w", cfg.ID, err)
	}
	gerManagerAddr, err := bridgeBinding.GlobalExitRootManager(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve GER manager address for claimer %s: %w", cfg.ID, err)
	}

	claimerDir := filepath.Join(storageBaseDir, gerSyncerDirName, cfg.ID)
	if err := os.MkdirAll(claimerDir, gerSyncerDirPerm); err != nil {
		return nil, nil, fmt.Errorf("create GER syncer storage dir for claimer %s: %w", cfg.ID, err)
	}

	gerCfg := sharedGERCfg
	gerCfg.GlobalExitRootL2Addr = gerManagerAddr
	gerCfg.DBPath = filepath.Join(claimerDir, "l2gersync.sqlite")
	if gerCfg.SyncBlockChunkSize == 0 {
		gerCfg.SyncBlockChunkSize = defaultGERSyncBlockChunkSize
	}
	// Per-claimer overrides of the shared [L2GERSync] values, applied when explicitly set.
	if cfg.BlockFinality != (aggkittypes.BlockNumberFinality{}) {
		gerCfg.BlockFinality = cfg.BlockFinality
	}
	if cfg.InitialBlockNum != 0 {
		gerCfg.InitialBlockNum = cfg.InitialBlockNum
	}

	rdCfg := sharedRDCfg
	rdCfg.DBPath = filepath.Join(claimerDir, "reorgdetector.sqlite")

	reorgDetector, err := reorgdetector.New(l2Client, rdCfg, reorgdetector.L2)
	if err != nil {
		return nil, nil, fmt.Errorf("create L2 reorg detector for claimer %s: %w", cfg.ID, err)
	}

	// Start the reorg detector before creating l2gersync (which calls Subscribe internally).
	// reorgDetector.Start runs loadTrackedHeaders, which replaces the in-memory trackedBlocks map
	// from the DB. If Subscribe ran first, that entry would be wiped, causing every
	// AddBlockToTrack call to fail with "subscriber not subscribed" forever.
	if err := reorgDetector.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start L2 reorg detector for claimer %s: %w", cfg.ID, err)
	}

	gerSync, err := l2gersync.New(ctx, gerCfg, reorgDetector, l2Client, l1InfoTreeSync, l1Client)
	if err != nil {
		return nil, nil, fmt.Errorf("create l2gersync for claimer %s: %w", cfg.ID, err)
	}

	start := func(ctx context.Context) {
		gerSync.Start(ctx)
	}

	return gerSync, start, nil
}

func newTargetClaimReader(
	bridgeAddr common.Address,
	client aggkittypes.BaseEthereumClienter,
) (autoclaimtypes.ClaimChecker, error) {
	bridge, err := agglayerbridgel2.NewAgglayerbridgel2(bridgeAddr, client)
	if err != nil {
		return nil, err
	}
	return targetClaimReader{bridge: bridge}, nil
}

func (r targetClaimReader) IsClaimed(ctx context.Context, globalIndex *big.Int) (bool, error) {
	if globalIndex == nil {
		return false, fmt.Errorf("AutoClaim target claim reader global index is nil")
	}
	mainnetFlag, rollupIndex, localExitRootIndex, err := bridgesync.DecodeGlobalIndex(globalIndex)
	if err != nil {
		return false, fmt.Errorf("decode AutoClaim global index: %w", err)
	}
	originNetwork := rollupIndex + 1
	if mainnetFlag {
		originNetwork = autoclaimtypes.L1OriginNetwork
	}
	return r.bridge.IsClaimed(&bind.CallOpts{Context: ctx}, localExitRootIndex, originNetwork)
}
