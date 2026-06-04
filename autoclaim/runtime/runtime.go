package runtime

import (
	"context"
	"fmt"
	"math/big"
	"net"
	"reflect"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxlog "github.com/0xPolygon/zkevm-ethtx-manager/log"
	aggoracletypes "github.com/agglayer/aggkit/aggoracle/types"
	"github.com/agglayer/aggkit/autoclaim/api"
	"github.com/agglayer/aggkit/autoclaim/claimer"
	autoclaimcfg "github.com/agglayer/aggkit/autoclaim/config"
	"github.com/agglayer/aggkit/autoclaim/policy"
	"github.com/agglayer/aggkit/autoclaim/proof"
	"github.com/agglayer/aggkit/autoclaim/sender"
	autoclaimstorage "github.com/agglayer/aggkit/autoclaim/storage"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/autoclaim/watchdog"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

const defaultAutoClaimDBQueryTimeout = 5 * time.Minute

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
	L1InfoTreeSync proof.L1InfoTreeSyncer
	Logger         aggkitcommon.Logger
}

// Runtime owns the started Auto Claim components.
type Runtime struct {
	Storage       autoclaimtypes.Storage
	Claimers      []autoclaimtypes.Claimer
	Registry      autoclaimtypes.ClaimerRegistry
	Watchdog      *watchdog.L1ToL2
	API           *api.API
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
	NewEthTxManager      func(context.Context, autoclaimcfg.ClaimerConfig) (EthTxManager, error)
	StartEthTxManager    func(context.Context, EthTxManager)
	NewPolicy            func(autoclaimcfg.PolicyName, autoclaimcfg.PolicyConfig) (autoclaimtypes.Policy, error)
	NewTargetClaimReader func(common.Address, aggkittypes.BaseEthereumClienter) (autoclaimtypes.TargetClaimReader, error)
	NewSender            func(
		autoclaimtypes.Storage,
		EthTxManager,
		autoclaimtypes.TargetClaimReader,
	) (autoclaimtypes.ClaimSender, error)
	NewClaimer func(
		autoclaimtypes.ClaimerTarget,
		autoclaimtypes.Storage,
		autoclaimtypes.Policy,
		autoclaimtypes.ProofPreparer,
		autoclaimtypes.ClaimSender,
		...claimer.Option,
	) (autoclaimtypes.Claimer, error)
	StartClaimer func(context.Context, autoclaimtypes.Claimer)
	NewRegistry  func(...autoclaimtypes.Claimer) (autoclaimtypes.ClaimerRegistry, error)
	NewWatchdog  func(
		autoclaimtypes.BridgeSource,
		watchdog.CursorStore,
		autoclaimtypes.ClaimerRegistry,
		...watchdog.Option,
	) (*watchdog.L1ToL2, error)
	StartWatchdog func(context.Context, *watchdog.L1ToL2)
	NewAPI        func(api.Config, api.Storage, autoclaimtypes.ClaimerRegistry, ...api.Option) (*api.API, error)
	StartAPI      func(context.Context, *api.API)
	Go            func(func())
}

// DefaultFactories returns production constructors for Auto Claim runtime startup.
func DefaultFactories(logConfig log.Config) Factories {
	return Factories{
		OpenStorage: func(logger aggkitcommon.Logger, dbPath string, dbQueryTimeout time.Duration) (
			autoclaimtypes.Storage, error,
		) {
			return autoclaimstorage.NewStandalone(logger, dbPath, dbQueryTimeout)
		},
		NewRPCClient: func(ctx context.Context, logger aggkitcommon.Logger, cfg ethermanconfig.RPCClientConfig) (
			aggkittypes.EthClienter, error,
		) {
			return etherman.NewRPCClient(ctx, logger, cfg)
		},
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
		NewPolicy: func(name autoclaimcfg.PolicyName, cfg autoclaimcfg.PolicyConfig) (autoclaimtypes.Policy, error) {
			return policy.NewPolicy(name, cfg)
		},
		NewTargetClaimReader: newTargetClaimReader,
		NewSender: func(
			storage autoclaimtypes.Storage,
			txManager EthTxManager,
			targetClaimReader autoclaimtypes.TargetClaimReader,
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
		NewWatchdog: func(
			bridgeSource autoclaimtypes.BridgeSource,
			cursorStore watchdog.CursorStore,
			registry autoclaimtypes.ClaimerRegistry,
			options ...watchdog.Option,
		) (*watchdog.L1ToL2, error) {
			return watchdog.NewL1ToL2(bridgeSource, cursorStore, registry, options...)
		},
		StartWatchdog: func(ctx context.Context, watchdogRunner *watchdog.L1ToL2) {
			watchdogRunner.Start(ctx)
		},
		NewAPI: func(
			cfg api.Config,
			storage api.Storage,
			registry autoclaimtypes.ClaimerRegistry,
			options ...api.Option,
		) (*api.API, error) {
			return api.New(cfg, storage, registry, options...)
		},
		StartAPI: func(ctx context.Context, apiServer *api.API) {
			if err := apiServer.Start(ctx); err != nil {
				log.Errorf("Auto Claim API stopped with error: %v", err)
			}
		},
		Go: func(fn func()) {
			go fn()
		},
	}
}

// Start creates and starts the Auto Claim runtime when enabled.
func Start(ctx context.Context, deps Dependencies, factories Factories) (*Runtime, error) {
	cfg := deps.Config
	if !cfg.Enabled {
		return nil, nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid AutoClaim config: %w", err)
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

	proofPreparer := proof.NewPreparer(deps.L1BridgeSync, deps.L1InfoTreeSync)
	runtime := &Runtime{Storage: storage}
	claimers := make([]autoclaimtypes.Claimer, 0, len(cfg.Claimers))
	for _, claimerCfg := range cfg.Claimers {
		if !claimerCfg.Enabled {
			continue
		}
		claimer, txManager, err := createClaimer(ctx, claimerCfg, storage, proofPreparer, logger, factories)
		if err != nil {
			return nil, err
		}
		claimers = append(claimers, claimer)
		runtime.EthTxManagers = append(runtime.EthTxManagers, txManager)
	}

	registry, err := factories.NewRegistry(claimers...)
	if err != nil {
		return nil, fmt.Errorf("create AutoClaim claimer registry: %w", err)
	}
	runtime.Registry = registry
	runtime.Claimers = claimers

	cursorStore, ok := storage.(watchdog.CursorStore)
	if !ok {
		return nil, fmt.Errorf("AutoClaim L1-to-L2 watchdog requires storage with cursor methods")
	}
	watchdogRunner, err := factories.NewWatchdog(
		deps.L1BridgeSync,
		cursorStore,
		registry,
		watchdog.WithEnabled(cfg.L1ToL2Watchdog.Enabled),
		watchdog.WithPollPeriod(cfg.L1ToL2Watchdog.PollInterval.Duration),
		watchdog.WithLogger(logger),
	)
	if err != nil {
		return nil, fmt.Errorf("create AutoClaim L1-to-L2 watchdog: %w", err)
	}
	runtime.Watchdog = watchdogRunner

	for _, txManager := range runtime.EthTxManagers {
		txManager := txManager
		factories.Go(func() {
			factories.StartEthTxManager(ctx, txManager)
		})
	}
	for _, claimer := range claimers {
		claimer := claimer
		factories.Go(func() {
			factories.StartClaimer(ctx, claimer)
		})
	}
	factories.Go(func() {
		factories.StartWatchdog(ctx, watchdogRunner)
	})

	if cfg.API.Enabled {
		apiStorage, ok := storage.(api.Storage)
		if !ok {
			return nil, fmt.Errorf("AutoClaim API requires storage with manual decision methods")
		}
		apiServer, err := factories.NewAPI(autoClaimAPIConfig(cfg.API, deps.RESTConfig), apiStorage, registry,
			api.WithLogger(logger))
		if err != nil {
			return nil, fmt.Errorf("create AutoClaim API: %w", err)
		}
		runtime.API = apiServer
		factories.Go(func() {
			factories.StartAPI(ctx, apiServer)
		})
	}

	logger.Info("Auto Claim started")
	return runtime, nil
}

func createClaimer(
	ctx context.Context,
	cfg autoclaimcfg.ClaimerConfig,
	storage autoclaimtypes.Storage,
	proofPreparer autoclaimtypes.ProofPreparer,
	logger aggkitcommon.Logger,
	factories Factories,
) (autoclaimtypes.Claimer, EthTxManager, error) {
	target := targetFromConfig(cfg)
	rpcClientCfg := targetRPCClientConfig(cfg.URLRPC)
	rpcClient, err := factories.NewRPCClient(ctx, logger, rpcClientCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("create AutoClaim RPC client for claimer %s: %w", cfg.ID, err)
	}
	txManager, err := factories.NewEthTxManager(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("create AutoClaim EthTxManager for claimer %s: %w", cfg.ID, err)
	}
	claimReader, err := factories.NewTargetClaimReader(cfg.BridgeAddr, rpcClient)
	if err != nil {
		return nil, nil, fmt.Errorf("create AutoClaim target claim reader for claimer %s: %w", cfg.ID, err)
	}
	claimPolicy, err := factories.NewPolicy(cfg.PolicyName, cfg.Policy)
	if err != nil {
		return nil, nil, fmt.Errorf("create AutoClaim policy for claimer %s: %w", cfg.ID, err)
	}
	claimSender, err := factories.NewSender(storage, txManager, claimReader)
	if err != nil {
		return nil, nil, fmt.Errorf("create AutoClaim sender for claimer %s: %w", cfg.ID, err)
	}
	targetClaimer, err := factories.NewClaimer(
		target,
		storage,
		claimPolicy,
		proofPreparer,
		claimSender,
		claimer.WithPollPeriod(cfg.WaitPeriod.Duration),
		claimer.WithLogger(logger),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create AutoClaim claimer %s: %w", cfg.ID, err)
	}

	return targetClaimer, txManager, nil
}

func withDefaultFactories(factories Factories, logConfig log.Config) Factories {
	defaults := DefaultFactories(logConfig)
	if factories.OpenStorage == nil {
		factories.OpenStorage = defaults.OpenStorage
	}
	if factories.NewRPCClient == nil {
		factories.NewRPCClient = defaults.NewRPCClient
	}
	if factories.NewEthTxManager == nil {
		factories.NewEthTxManager = defaults.NewEthTxManager
	}
	if factories.StartEthTxManager == nil {
		factories.StartEthTxManager = defaults.StartEthTxManager
	}
	if factories.NewPolicy == nil {
		factories.NewPolicy = defaults.NewPolicy
	}
	if factories.NewTargetClaimReader == nil {
		factories.NewTargetClaimReader = defaults.NewTargetClaimReader
	}
	if factories.NewSender == nil {
		factories.NewSender = defaults.NewSender
	}
	if factories.NewClaimer == nil {
		factories.NewClaimer = defaults.NewClaimer
	}
	if factories.StartClaimer == nil {
		factories.StartClaimer = defaults.StartClaimer
	}
	if factories.NewRegistry == nil {
		factories.NewRegistry = defaults.NewRegistry
	}
	if factories.NewWatchdog == nil {
		factories.NewWatchdog = defaults.NewWatchdog
	}
	if factories.StartWatchdog == nil {
		factories.StartWatchdog = defaults.StartWatchdog
	}
	if factories.NewAPI == nil {
		factories.NewAPI = defaults.NewAPI
	}
	if factories.StartAPI == nil {
		factories.StartAPI = defaults.StartAPI
	}
	if factories.Go == nil {
		factories.Go = defaults.Go
	}
	return factories
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
	return autoclaimtypes.ClaimerTarget{
		ID:                 cfg.ID,
		DestinationNetwork: cfg.NetworkID,
		NetworkType:        string(cfg.NetworkType),
		BridgeAddr:         cfg.BridgeAddr,
		GasOffset:          cfg.GasOffset,
		WaitPeriod:         cfg.WaitPeriod.Duration,
		RetryAfter:         cfg.WaitPeriod.Duration,
	}
}

func autoClaimAPIConfig(cfg autoclaimcfg.APIConfig, restCfg aggkitcommon.RESTConfig) api.Config {
	return api.Config{
		Enabled:      cfg.Enabled,
		Address:      net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port)),
		ReadTimeout:  restCfg.ReadTimeout.Duration,
		WriteTimeout: restCfg.WriteTimeout.Duration,
	}
}

type targetClaimReader struct {
	bridge isClaimedContract
}

type isClaimedContract interface {
	IsClaimed(opts *bind.CallOpts, leafIndex uint32, sourceBridgeNetwork uint32) (bool, error)
}

func newTargetClaimReader(
	bridgeAddr common.Address,
	client aggkittypes.BaseEthereumClienter,
) (autoclaimtypes.TargetClaimReader, error) {
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
