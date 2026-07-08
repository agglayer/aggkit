package runtime

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	aggoracletypes "github.com/agglayer/aggkit/aggoracle/types"
	"github.com/agglayer/aggkit/autoclaim/api"
	"github.com/agglayer/aggkit/autoclaim/bridgedetector"
	"github.com/agglayer/aggkit/autoclaim/claimer"
	autoclaimcfg "github.com/agglayer/aggkit/autoclaim/config"
	"github.com/agglayer/aggkit/autoclaim/policy"
	"github.com/agglayer/aggkit/autoclaim/proof"
	"github.com/agglayer/aggkit/autoclaim/simulator"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	coretypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func TestStartDisabledAutoClaimNoop(t *testing.T) {
	ctx := context.Background()
	called := false

	// No enabled claimer => the runtime is a no-op and must not open storage.
	runtime, err := Start(ctx, Dependencies{Config: autoclaimcfg.Config{}}, Factories{
		OpenStorage: func(aggkitcommon.Logger, string, time.Duration) (autoclaimtypes.Storage, error) {
			called = true
			return nil, nil
		},
	})

	require.NoError(t, err)
	require.Nil(t, runtime)
	require.False(t, called)
}

func TestStartEnabledAutoClaimMissingDependencies(t *testing.T) {
	cfg := validConfig()

	_, err := Start(context.Background(), Dependencies{Config: cfg}, Factories{})
	require.ErrorContains(t, err, "AutoClaim requires l1bridgesync")

	var typedNilL1BridgeSync *fakeL1BridgeSync
	_, err = Start(context.Background(), Dependencies{
		Config:       cfg,
		L1BridgeSync: typedNilL1BridgeSync,
	}, Factories{})
	require.ErrorContains(t, err, "AutoClaim requires l1bridgesync")

	_, err = Start(context.Background(), Dependencies{
		Config:       cfg,
		L1BridgeSync: fakeL1BridgeSync{},
	}, Factories{})
	require.ErrorContains(t, err, "AutoClaim requires l1infotreesync")

	var typedNilL1InfoTreeSync *fakeL1InfoTreeSync
	_, err = Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: typedNilL1InfoTreeSync,
	}, Factories{})
	require.ErrorContains(t, err, "AutoClaim requires l1infotreesync")
}

func TestStartRejectsInvalidClaimerConfig(t *testing.T) {
	cfg := validConfig()
	cfg.Claimers[0].URLRPC = ""

	_, err := Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
	}, Factories{})

	require.ErrorContains(t, err, "invalid AutoClaim config")
	require.ErrorContains(t, err, "URLRPC is required")
}

func TestStartBuildsAndStartsOneTransactionManagerPerEnabledClaimer(t *testing.T) {
	cfg := validConfig()
	cfg.Claimers = append(cfg.Claimers,
		validClaimer("disabled", 2, false),
		validClaimer("secondary", 3, true),
	)
	var mu sync.Mutex
	rpcURLs := make([]string, 0)
	txManagerIDs := make([]string, 0)
	txManagerStoragePaths := make([]string, 0)
	claimerTargets := make([]autoclaimtypes.ClaimerTarget, 0)
	startedManagers := 0
	startedClaimers := 0
	startedBridgeDetector := 0
	apiCreated := 0

	runtime, err := Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
	}, testFactories(&factoryHooks{
		newRPCClient: func(_ context.Context, _ aggkitcommon.Logger, cfg ethermanconfig.RPCClientConfig) (
			aggkittypes.EthClienter, error,
		) {
			mu.Lock()
			defer mu.Unlock()
			rpcURLs = append(rpcURLs, cfg.URL)
			return nil, nil
		},
		newEthTxManager: func(_ context.Context, cfg autoclaimcfg.ClaimerConfig) (EthTxManager, error) {
			mu.Lock()
			defer mu.Unlock()
			txManagerIDs = append(txManagerIDs, cfg.ID)
			txManagerStoragePaths = append(txManagerStoragePaths, cfg.EthTxManager.StoragePath)
			return &fakeEthTxManager{}, nil
		},
		startEthTxManager: func(context.Context, EthTxManager) {
			mu.Lock()
			defer mu.Unlock()
			startedManagers++
		},
		startClaimer: func(context.Context, autoclaimtypes.Claimer) {
			mu.Lock()
			defer mu.Unlock()
			startedClaimers++
		},
		startBridgeDetector: func(context.Context, *bridgedetector.L1ToL2) {
			mu.Lock()
			defer mu.Unlock()
			startedBridgeDetector++
		},
		newAPI: func() {
			apiCreated++
		},
		newClaimer: func(target autoclaimtypes.ClaimerTarget) {
			mu.Lock()
			defer mu.Unlock()
			claimerTargets = append(claimerTargets, target)
		},
	}))

	require.NoError(t, err)
	require.NotNil(t, runtime)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return startedManagers == 2 && startedClaimers == 2 && startedBridgeDetector == 1
	}, time.Second, 10*time.Millisecond)
	require.ElementsMatch(t, []string{"http://claimer-1.example", "http://claimer-3.example"}, rpcURLs)
	require.ElementsMatch(t, []string{"primary", "secondary"}, txManagerIDs)
	require.ElementsMatch(t, []string{"/tmp/ethtx-primary.sqlite", "/tmp/ethtx-secondary.sqlite"}, txManagerStoragePaths)
	require.Len(t, claimerTargets, 2)
	require.ElementsMatch(t, []uint32{1, 3}, []uint32{
		claimerTargets[0].DestinationNetwork,
		claimerTargets[1].DestinationNetwork,
	})
	for _, target := range claimerTargets {
		require.Equal(t, 2*time.Second, target.RetryAfter)
		require.Equal(t, uint64(3), target.MaxRetries)
	}
	require.Zero(t, apiCreated)
}

func TestStartStopsBackgroundWorkOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	txStopped := make(chan struct{})
	claimerStopped := make(chan struct{})
	bridgeDetectorStopped := make(chan struct{})

	_, err := Start(ctx, Dependencies{
		Config:         validConfig(),
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
	}, testFactories(&factoryHooks{
		startEthTxManager: func(ctx context.Context, _ EthTxManager) {
			<-ctx.Done()
			close(txStopped)
		},
		startClaimer: func(ctx context.Context, _ autoclaimtypes.Claimer) {
			<-ctx.Done()
			close(claimerStopped)
		},
		startBridgeDetector: func(ctx context.Context, _ *bridgedetector.L1ToL2) {
			<-ctx.Done()
			close(bridgeDetectorStopped)
		},
	}))
	require.NoError(t, err)

	cancel()
	requireClosed(t, txStopped)
	requireClosed(t, claimerStopped)
	requireClosed(t, bridgeDetectorStopped)
}

func TestStartDoesNotCreateAPIWhenDisabled(t *testing.T) {
	cfg := validConfig()
	cfg.API.Enabled = false
	apiCreated := false

	_, err := Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
	}, testFactories(&factoryHooks{
		newAPI: func() {
			apiCreated = true
		},
	}))

	require.NoError(t, err)
	require.False(t, apiCreated)
}

func TestStartCreatesTargetSimulatorOnlyForBasicFilter(t *testing.T) {
	cfg := validConfig()
	cfg.Claimers = []autoclaimcfg.ClaimerConfig{
		validClaimer("allow-all", 1, true),
		validClaimer("basic-filter", 2, true),
	}
	cfg.Claimers[1].PolicyName = autoclaimcfg.PolicyNameBasicFilter
	simulatorTargets := make([]autoclaimtypes.ClaimerTarget, 0)
	policyOptions := make([]int, 0)

	_, err := Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
	}, testFactories(&factoryHooks{
		newTargetSimulator: func(
			_ simulator.GasEstimator,
			_ autoclaimtypes.ProofPreparer,
			target autoclaimtypes.ClaimerTarget,
			_ common.Address,
		) {
			simulatorTargets = append(simulatorTargets, target)
		},
		newPolicy: func(
			_ autoclaimcfg.PolicyName,
			_ autoclaimcfg.PolicyConfig,
			options ...policy.RegistryOption,
		) {
			policyOptions = append(policyOptions, len(options))
		},
	}))

	require.NoError(t, err)
	require.Len(t, simulatorTargets, 1)
	require.Equal(t, uint32(2), simulatorTargets[0].DestinationNetwork)
	require.ElementsMatch(t, []int{0, 1}, policyOptions)
}

func TestStartFailsWhenBasicFilterSimulatorConstructionFails(t *testing.T) {
	cfg := validConfig()
	cfg.Claimers[0].PolicyName = autoclaimcfg.PolicyNameBasicFilter

	_, err := Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
	}, testFactories(&factoryHooks{
		targetSimulatorErr: errors.New("simulator unavailable"),
	}))

	require.ErrorContains(t, err, "create basic-filter target simulator")
	require.ErrorContains(t, err, "simulator unavailable")
}

// fakeBridgeServiceFinder is a harmless stand-in for a real bridgeservicefinder.Finder, injected via
// Factories.NewBridgeServiceFinder so tests never make real RPC calls.
type fakeBridgeServiceFinder struct{}

func (fakeBridgeServiceFinder) Start(context.Context) error { return nil }

func (fakeBridgeServiceFinder) GetURL(uint32) (string, error) { return "http://fake-source", nil }

func withL2ToLxEnabled(cfg autoclaimcfg.Config) autoclaimcfg.Config {
	cfg.L2ToLxBridgeDetector = autoclaimcfg.L2ToLxBridgeDetector{
		Enabled:                    true,
		PollInterval:               cfgtypes.Duration{Duration: time.Second},
		RetryAfterErrorPeriod:      cfgtypes.Duration{Duration: time.Second},
		MaxRetryAttemptsAfterError: -1,
	}
	cfg.BridgeServiceFinder = bridgeservicefinder.Config{
		RollupManagerAddr: common.HexToAddress("0x2000000000000000000000000000000000000002"),
	}
	return cfg
}

func TestCreateClaimerSkipsGERSyncerForL1DestinationClaimerOnly(t *testing.T) {
	cfg := withL2ToLxEnabled(validConfig())
	cfg.Claimers = []autoclaimcfg.ClaimerConfig{
		validClaimer("l1-dest", 0, true),
		validClaimer("l2-dest", 5, true),
	}

	var mu sync.Mutex
	gerSyncerCalledFor := make([]uint32, 0)

	factories := testFactories(&factoryHooks{})
	factories.NewBridgeServiceFinder = func(bridgeservicefinder.Config, aggkittypes.EthClienter) (
		bridgeservicefinder.Finder, error,
	) {
		return fakeBridgeServiceFinder{}, nil
	}
	factories.StartBridgeServiceFinder = func(context.Context, bridgeservicefinder.Finder) error {
		return nil
	}
	factories.NewGERSyncer = func(_ context.Context, deps GERSyncerDeps) (proof.L2GERSyncer, func(context.Context), error) {
		mu.Lock()
		defer mu.Unlock()
		gerSyncerCalledFor = append(gerSyncerCalledFor, deps.ClaimerCfg.NetworkID)
		return nil, nil, nil
	}

	rt, err := Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
	}, factories)

	require.NoError(t, err)
	require.NotNil(t, rt)
	// The L1-destination (NetworkID 0) claimer must not trigger a GER syncer; the L2-destination one
	// (NetworkID 5) still gets its own.
	require.ElementsMatch(t, []uint32{5}, gerSyncerCalledFor)
}

func TestStartL2ToLxDetectorDisabledNeverBuildsRealFinder(t *testing.T) {
	// validConfig() leaves L2ToLxBridgeDetector at its zero value (Enabled=false), matching every
	// pre-existing config that never opted into it.
	cfg := validConfig()
	finderBuilt := false

	factories := testFactories(&factoryHooks{})
	factories.NewBridgeServiceFinder = func(bridgeservicefinder.Config, aggkittypes.EthClienter) (
		bridgeservicefinder.Finder, error,
	) {
		finderBuilt = true
		return fakeBridgeServiceFinder{}, nil
	}

	rt, err := Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
	}, factories)

	require.NoError(t, err)
	require.NotNil(t, rt)
	require.False(t, finderBuilt, "NewBridgeServiceFinder must not be called when the detector is disabled")
	require.NotNil(t, rt.L2ToLxBridgeDetector)
	require.NotNil(t, rt.BridgeServiceFinder)
}

func TestEthTxManagerAdapterMethods(t *testing.T) {
	startCalled := false
	stopCalled := false

	adapter := ethTxManagerAdapter{
		start: func() { startCalled = true },
		stop:  func() { stopCalled = true },
	}

	adapter.Start()
	require.True(t, startCalled)

	adapter.Stop()
	require.True(t, stopCalled)

	// Stop with nil stop func should not panic.
	adapterNoStop := ethTxManagerAdapter{
		start: func() {},
		stop:  nil,
	}
	require.NotPanics(t, func() { adapterNoStop.Stop() })
}

func TestDefaultFactoriesSimpleConstructors(t *testing.T) {
	factories := DefaultFactories(log.Config{})

	// NewProofPreparer wraps a SourceAwarePreparer over proof.NewPreparer/proof.NewRollupPreparer.
	preparer, err := factories.NewProofPreparer(fakeL1BridgeSync{}, fakeL1InfoTreeSync{}, nil, fakeLeafProofRefresher{})
	require.NoError(t, err)
	require.NotNil(t, preparer)

	// NewTargetSimulator delegates to simulator.New and propagates nil-client error.
	_, err = factories.NewTargetSimulator(nil, nil, autoclaimtypes.ClaimerTarget{}, common.Address{})
	require.Error(t, err)

	// NewSender delegates to sender.New and propagates nil-storage error.
	_, err = factories.NewSender(nil, nil, nil)
	require.Error(t, err)

	// NewClaimer delegates to claimer.New and propagates empty-ID error.
	_, err = factories.NewClaimer(autoclaimtypes.ClaimerTarget{}, nil, nil, nil, nil)
	require.Error(t, err)

	// NewRegistry delegates to claimer.NewRegistry and propagates nil-claimer error.
	_, err = factories.NewRegistry(nil)
	require.Error(t, err)
}

func requireClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func validConfig() autoclaimcfg.Config {
	return autoclaimcfg.Config{
		StoragePath: "/tmp/autoclaim.sqlite",
		API: autoclaimcfg.APIConfig{
			Enabled: false,
		},
		L1ToL2BridgeDetector: autoclaimcfg.L1ToL2BridgeDetector{
			Enabled:                    true,
			PollInterval:               cfgtypes.Duration{Duration: time.Hour},
			RetryAfterErrorPeriod:      cfgtypes.Duration{Duration: time.Second},
			MaxRetryAttemptsAfterError: -1,
		},
		Claimers: []autoclaimcfg.ClaimerConfig{validClaimer("primary", 1, true)},
	}
}

func validClaimer(id string, networkID uint32, enabled bool) autoclaimcfg.ClaimerConfig {
	return autoclaimcfg.ClaimerConfig{
		Enabled:     enabled,
		ID:          id,
		NetworkType: autoclaimcfg.NetworkTypeEVM,
		NetworkID:   networkID,
		URLRPC:      "http://claimer-" + string(rune('0'+networkID)) + ".example",
		BridgeAddr:  common.HexToAddress("0x1000000000000000000000000000000000000001"),
		PolicyName:  autoclaimcfg.PolicyNameAllowAll,
		WaitPeriod:  cfgtypes.Duration{Duration: time.Second},
		RetryAfter:  cfgtypes.Duration{Duration: 2 * time.Second},
		MaxRetries:  3,
		EthTxManager: ethtxmanager.Config{
			StoragePath: "/tmp/ethtx-" + id + ".sqlite",
		},
	}
}

type factoryHooks struct {
	newRPCClient func(context.Context, aggkitcommon.Logger, ethermanconfig.RPCClientConfig) (
		aggkittypes.EthClienter, error,
	)
	newEthTxManager     func(context.Context, autoclaimcfg.ClaimerConfig) (EthTxManager, error)
	startEthTxManager   func(context.Context, EthTxManager)
	startClaimer        func(context.Context, autoclaimtypes.Claimer)
	startBridgeDetector func(context.Context, *bridgedetector.L1ToL2)
	newClaimer          func(autoclaimtypes.ClaimerTarget)
	newPolicy           func(autoclaimcfg.PolicyName, autoclaimcfg.PolicyConfig, ...policy.RegistryOption)
	newTargetSimulator  func(
		simulator.GasEstimator,
		autoclaimtypes.ProofPreparer,
		autoclaimtypes.ClaimerTarget,
		common.Address,
	)
	targetSimulatorErr error
	newAPI             func()
}

func newRPCClientFactory(hooks *factoryHooks) func(context.Context, aggkitcommon.Logger, ethermanconfig.RPCClientConfig) (aggkittypes.EthClienter, error) {
	return func(ctx context.Context, logger aggkitcommon.Logger, cfg ethermanconfig.RPCClientConfig) (aggkittypes.EthClienter, error) {
		if hooks.newRPCClient != nil {
			return hooks.newRPCClient(ctx, logger, cfg)
		}
		return nil, nil
	}
}

func newEthTxManagerFactory(hooks *factoryHooks) func(context.Context, autoclaimcfg.ClaimerConfig) (EthTxManager, error) {
	return func(ctx context.Context, cfg autoclaimcfg.ClaimerConfig) (EthTxManager, error) {
		if hooks.newEthTxManager != nil {
			return hooks.newEthTxManager(ctx, cfg)
		}
		return &fakeEthTxManager{}, nil
	}
}

func newPolicyFactory(hooks *factoryHooks) func(autoclaimcfg.PolicyName, autoclaimcfg.PolicyConfig, ...policy.RegistryOption) (autoclaimtypes.Policy, error) {
	return func(name autoclaimcfg.PolicyName, cfg autoclaimcfg.PolicyConfig, options ...policy.RegistryOption) (autoclaimtypes.Policy, error) {
		if hooks.newPolicy != nil {
			hooks.newPolicy(name, cfg, options...)
		}
		return fakePolicy{}, nil
	}
}

func newTargetSimulatorFactory(hooks *factoryHooks) func(simulator.GasEstimator, autoclaimtypes.ProofPreparer, autoclaimtypes.ClaimerTarget, common.Address) (policy.TargetSimulator, error) {
	return func(client simulator.GasEstimator, proofPreparer autoclaimtypes.ProofPreparer, target autoclaimtypes.ClaimerTarget, from common.Address) (policy.TargetSimulator, error) {
		if hooks.newTargetSimulator != nil {
			hooks.newTargetSimulator(client, proofPreparer, target, from)
		}
		if hooks.targetSimulatorErr != nil {
			return nil, hooks.targetSimulatorErr
		}
		return fakeTargetSimulator{}, nil
	}
}

func newClaimerFactory(hooks *factoryHooks) func(autoclaimtypes.ClaimerTarget, autoclaimtypes.Storage, autoclaimtypes.Policy, autoclaimtypes.ProofPreparer, autoclaimtypes.ClaimSender, ...claimer.Option) (autoclaimtypes.Claimer, error) {
	return func(target autoclaimtypes.ClaimerTarget, _ autoclaimtypes.Storage, _ autoclaimtypes.Policy, _ autoclaimtypes.ProofPreparer, _ autoclaimtypes.ClaimSender, _ ...claimer.Option) (autoclaimtypes.Claimer, error) {
		if hooks.newClaimer != nil {
			hooks.newClaimer(target)
		}
		return fakeClaimer{target: target}, nil
	}
}

func testFactories(hooks *factoryHooks) Factories {
	if hooks == nil {
		hooks = &factoryHooks{}
	}
	return Factories{
		OpenStorage: func(aggkitcommon.Logger, string, time.Duration) (autoclaimtypes.Storage, error) {
			return &fakeStorage{}, nil
		},
		NewRPCClient:    newRPCClientFactory(hooks),
		NewEthTxManager: newEthTxManagerFactory(hooks),
		StartEthTxManager: func(ctx context.Context, txManager EthTxManager) {
			if hooks.startEthTxManager != nil {
				hooks.startEthTxManager(ctx, txManager)
			}
		},
		NewPolicy: newPolicyFactory(hooks),
		NewTargetClaimReader: func(common.Address, aggkittypes.BaseEthereumClienter) (
			autoclaimtypes.ClaimChecker, error,
		) {
			return fakeTargetClaimReader{}, nil
		},
		NewGERSyncer: func(_ context.Context, _ GERSyncerDeps) (proof.L2GERSyncer, func(context.Context), error) {
			return nil, nil, nil
		},
		NewProofPreparer: func(
			l1BridgeSync proof.L1BridgeSyncer,
			l1InfoTreeSync ProofL1InfoTreeSyncer,
			gerSyncer proof.L2GERSyncer,
			_ proof.LeafProofRefresher,
		) (autoclaimtypes.ProofPreparer, error) {
			return proof.NewPreparer(l1BridgeSync, l1InfoTreeSync, gerSyncer), nil
		},
		NewTargetSimulator: newTargetSimulatorFactory(hooks),
		NewSender: func(
			autoclaimtypes.Storage,
			EthTxManager,
			autoclaimtypes.ClaimChecker,
		) (autoclaimtypes.ClaimSender, error) {
			return fakeSender{}, nil
		},
		NewClaimer: newClaimerFactory(hooks),
		StartClaimer: func(ctx context.Context, runtimeClaimer autoclaimtypes.Claimer) {
			if hooks.startClaimer != nil {
				hooks.startClaimer(ctx, runtimeClaimer)
			}
		},
		StartBridgeDetector: func(ctx context.Context, runner *bridgedetector.L1ToL2) {
			if hooks.startBridgeDetector != nil {
				hooks.startBridgeDetector(ctx, runner)
			}
		},
		NewAPI: func(
			cfg api.Config,
			storage api.Storage,
			registry autoclaimtypes.ClaimerRegistry,
			options ...api.Option,
		) (*api.API, error) {
			if hooks.newAPI != nil {
				hooks.newAPI()
			}
			return api.New(cfg, storage, registry, options...)
		},
		Go: func(fn func()) {
			go fn()
		},
	}
}

type fakeL1BridgeSync struct{}

func (fakeL1BridgeSync) GetBridges(context.Context, uint64, uint64) ([]bridgesync.Bridge, error) {
	return nil, nil
}

func (fakeL1BridgeSync) GetLastProcessedBlock(context.Context) (uint64, bool, error) {
	return 0, false, nil
}

func (fakeL1BridgeSync) GetProof(context.Context, uint32, common.Hash) (treetypes.Proof, error) {
	return treetypes.Proof{}, nil
}

func (fakeL1BridgeSync) GetRootByLER(context.Context, common.Hash) (*treetypes.Root, error) {
	return &treetypes.Root{}, nil
}

func (fakeL1BridgeSync) GetLastRoot(context.Context) (*treetypes.Root, error) {
	return &treetypes.Root{}, nil
}

type fakeL1InfoTreeSync struct{}

func (fakeL1InfoTreeSync) GetInfoByIndex(context.Context, uint32) (*l1infotreesync.L1InfoTreeLeaf, error) {
	return nil, nil
}

func (fakeL1InfoTreeSync) GetRollupExitTreeMerkleProof(context.Context, uint32, common.Hash) (treetypes.Proof, error) {
	return treetypes.Proof{}, nil
}

func (fakeL1InfoTreeSync) GetLastInfo() (*l1infotreesync.L1InfoTreeLeaf, error) {
	return nil, nil
}

func (fakeL1InfoTreeSync) GetFirstInfo() (*l1infotreesync.L1InfoTreeLeaf, error) {
	return nil, nil
}

func (fakeL1InfoTreeSync) GetFirstInfoAfterBlock(uint64) (*l1infotreesync.L1InfoTreeLeaf, error) {
	return nil, nil
}

func (fakeL1InfoTreeSync) GetInfoByGlobalExitRoot(common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error) {
	return nil, nil
}

func (fakeL1InfoTreeSync) GetLastL1InfoTreeRoot(context.Context) (treetypes.Root, error) {
	return treetypes.Root{}, nil
}

func (fakeL1InfoTreeSync) IsUpToDate(context.Context, aggkittypes.BaseEthereumClienter) (bool, error) {
	return true, nil
}

func (fakeL1InfoTreeSync) GetLocalExitRoot(context.Context, uint32, common.Hash) (common.Hash, error) {
	return common.Hash{}, nil
}

func (fakeL1InfoTreeSync) GetLastProcessedBlock(context.Context) (uint64, error) {
	return 0, nil
}

func (fakeL1InfoTreeSync) GetVerifiedBatchesInBlockRange(uint64, uint64) ([]*l1infotreesync.VerifyBatches, error) {
	return nil, nil
}

func (fakeL1InfoTreeSync) GetLatestL1InfoLeafUntilBlock(
	context.Context, uint64,
) (*l1infotreesync.L1InfoTreeLeaf, error) {
	return nil, nil
}

type fakeLeafProofRefresher struct{}

func (fakeLeafProofRefresher) RefreshLeafProof(
	context.Context, uint32, uint32, uint32,
) (treetypes.Proof, error) {
	return treetypes.Proof{}, nil
}

type fakeStorage struct{}

func (*fakeStorage) EnqueueRequest(
	context.Context,
	autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.AutoClaimRequest, bool, error) {
	return nil, false, nil
}

func (*fakeStorage) GetRequest(context.Context, autoclaimtypes.RequestKey) (*autoclaimtypes.AutoClaimRequest, error) {
	return nil, nil
}

func (*fakeStorage) ListRequests(
	context.Context,
	autoclaimtypes.RequestFilter,
) (*autoclaimtypes.RequestPage, error) {
	return &autoclaimtypes.RequestPage{}, nil
}

func (*fakeStorage) ListRecoverableRequests(
	context.Context,
	autoclaimtypes.RecoveryFilter,
) (*autoclaimtypes.RequestPage, error) {
	return &autoclaimtypes.RequestPage{}, nil
}

func (*fakeStorage) RecordPolicyDecision(
	context.Context,
	autoclaimtypes.RequestKey,
	autoclaimtypes.PolicyDecision,
) error {
	return nil
}

func (*fakeStorage) RecordManualDecision(
	context.Context,
	autoclaimtypes.RequestKey,
	autoclaimtypes.PolicyDecision,
) error {
	return nil
}

func (*fakeStorage) SaveProof(context.Context, autoclaimtypes.RequestKey, autoclaimtypes.ClaimProof) error {
	return nil
}

func (*fakeStorage) RecordTransactionAttempt(
	context.Context,
	autoclaimtypes.RequestKey,
	autoclaimtypes.TransactionAttempt,
) error {
	return nil
}

func (*fakeStorage) TransitionRequest(
	context.Context,
	autoclaimtypes.RequestKey,
	autoclaimtypes.RequestStatus,
	autoclaimtypes.RequestStatus,
	time.Time,
) (*autoclaimtypes.AutoClaimRequest, error) {
	return nil, nil
}

func (*fakeStorage) UpdateLastError(context.Context, autoclaimtypes.RequestKey, string, time.Time) error {
	return nil
}

func (*fakeStorage) GetBridgeCursor(
	context.Context,
	string,
) (*autoclaimtypes.BridgeCursor, bool, error) {
	return nil, false, nil
}

func (*fakeStorage) SaveBridgeCursor(
	context.Context,
	string,
	autoclaimtypes.BridgeCursor,
	time.Time,
) error {
	return nil
}

func (*fakeStorage) GetLERCursor(context.Context, uint32) (*autoclaimtypes.LERCursor, bool, error) {
	return nil, false, nil
}

func (*fakeStorage) SaveLERCursor(context.Context, uint32, autoclaimtypes.LERCursor, time.Time) error {
	return nil
}

func (*fakeStorage) ApproveManualRequest(
	context.Context,
	autoclaimtypes.RequestKey,
	autoclaimtypes.PolicyDecision,
	time.Time,
) (*autoclaimtypes.AutoClaimRequest, error) {
	return nil, nil
}

func (*fakeStorage) RejectManualRequest(
	context.Context,
	autoclaimtypes.RequestKey,
	autoclaimtypes.PolicyDecision,
	time.Time,
) (*autoclaimtypes.AutoClaimRequest, error) {
	return nil, nil
}

type fakePolicy struct{}

func (fakePolicy) Evaluate(
	context.Context,
	autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.PolicyDecision, error) {
	return &autoclaimtypes.PolicyDecision{Result: autoclaimtypes.PolicyResultApproved}, nil
}

type fakeTargetClaimReader struct{}

func (fakeTargetClaimReader) IsClaimed(context.Context, *big.Int) (bool, error) {
	return false, nil
}

type fakeTargetSimulator struct{}

func (fakeTargetSimulator) SimulateClaim(
	context.Context,
	autoclaimtypes.AutoClaimRequest,
) (*policy.SimulationResult, error) {
	return &policy.SimulationResult{
		GasUsed:          1,
		NestedBridgeCall: policy.NestedBridgeCallNotDetected,
	}, nil
}

type fakeSender struct{}

func (fakeSender) SubmitClaim(
	context.Context,
	autoclaimtypes.AutoClaimRequest,
	autoclaimtypes.ClaimProof,
	autoclaimtypes.ClaimerTarget,
) (*autoclaimtypes.TransactionAttempt, error) {
	return nil, nil
}

func (fakeSender) EthTxManager() aggoracletypes.EthTxManager {
	return &fakeEthTxManager{}
}

type fakeClaimer struct {
	target autoclaimtypes.ClaimerTarget
}

func (c fakeClaimer) Target() autoclaimtypes.ClaimerTarget {
	return c.target
}

func (fakeClaimer) IsClaimed(context.Context, autoclaimtypes.BridgeExit) (bool, error) {
	return false, nil
}

func (fakeClaimer) Enqueue(context.Context, autoclaimtypes.BridgeExit) error {
	return nil
}

func (fakeClaimer) Advance(context.Context, autoclaimtypes.RequestKey) error {
	return nil
}

type fakeEthTxManager struct{}

func (*fakeEthTxManager) Start() {}

func (*fakeEthTxManager) Stop() {}

func (*fakeEthTxManager) Remove(context.Context, common.Hash) error {
	return nil
}

func (*fakeEthTxManager) ResultsByStatus(
	context.Context,
	[]ethtxtypes.MonitoredTxStatus,
) ([]ethtxtypes.MonitoredTxResult, error) {
	return nil, nil
}

func (*fakeEthTxManager) Result(context.Context, common.Hash) (ethtxtypes.MonitoredTxResult, error) {
	return ethtxtypes.MonitoredTxResult{}, nil
}

func (*fakeEthTxManager) Add(
	context.Context,
	*common.Address,
	*big.Int,
	[]byte,
	uint64,
	*coretypes.BlobTxSidecar,
) (common.Hash, error) {
	return common.Hash{}, nil
}

func (*fakeEthTxManager) From() common.Address {
	return common.HexToAddress("0x2000000000000000000000000000000000000002")
}
