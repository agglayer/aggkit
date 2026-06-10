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
	"github.com/agglayer/aggkit/autoclaim/claimer"
	autoclaimcfg "github.com/agglayer/aggkit/autoclaim/config"
	"github.com/agglayer/aggkit/autoclaim/policy"
	"github.com/agglayer/aggkit/autoclaim/simulator"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/autoclaim/watchdog"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	coretypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func TestStartDisabledAutoClaimNoop(t *testing.T) {
	ctx := context.Background()
	called := false

	runtime, err := Start(ctx, Dependencies{Config: autoclaimcfg.Config{Enabled: false}}, Factories{
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

	_, err = Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
	}, Factories{})
	require.ErrorContains(t, err, "AutoClaim L1-to-L2 watchdog requires l2gersync")
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
	startedWatchdog := 0
	apiCreated := 0

	runtime, err := Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
		L2GERSync:      fakeL2GERSync{},
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
		startWatchdog: func(context.Context, *watchdog.L1ToL2) {
			mu.Lock()
			defer mu.Unlock()
			startedWatchdog++
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
		return startedManagers == 2 && startedClaimers == 2 && startedWatchdog == 1
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
	watchdogStopped := make(chan struct{})

	_, err := Start(ctx, Dependencies{
		Config:         validConfig(),
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
		L2GERSync:      fakeL2GERSync{},
	}, testFactories(&factoryHooks{
		startEthTxManager: func(ctx context.Context, _ EthTxManager) {
			<-ctx.Done()
			close(txStopped)
		},
		startClaimer: func(ctx context.Context, _ autoclaimtypes.Claimer) {
			<-ctx.Done()
			close(claimerStopped)
		},
		startWatchdog: func(ctx context.Context, _ *watchdog.L1ToL2) {
			<-ctx.Done()
			close(watchdogStopped)
		},
	}))
	require.NoError(t, err)

	cancel()
	requireClosed(t, txStopped)
	requireClosed(t, claimerStopped)
	requireClosed(t, watchdogStopped)
}

func TestStartDoesNotCreateAPIWhenDisabled(t *testing.T) {
	cfg := validConfig()
	cfg.API.Enabled = false
	apiCreated := false
	apiStarted := false

	_, err := Start(context.Background(), Dependencies{
		Config:         cfg,
		L1BridgeSync:   fakeL1BridgeSync{},
		L1InfoTreeSync: fakeL1InfoTreeSync{},
		L2GERSync:      fakeL2GERSync{},
	}, testFactories(&factoryHooks{
		newAPI: func() {
			apiCreated = true
		},
		startAPI: func(context.Context) {
			apiStarted = true
		},
	}))

	require.NoError(t, err)
	require.False(t, apiCreated)
	require.False(t, apiStarted)
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
		L2GERSync:      fakeL2GERSync{},
	}, testFactories(&factoryHooks{
		newTargetSimulator: func(
			_ simulator.Client,
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
		L2GERSync:      fakeL2GERSync{},
	}, testFactories(&factoryHooks{
		targetSimulatorErr: errors.New("simulator unavailable"),
	}))

	require.ErrorContains(t, err, "create basic-filter target simulator")
	require.ErrorContains(t, err, "simulator unavailable")
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
		Enabled:     true,
		StoragePath: "/tmp/autoclaim.sqlite",
		API: autoclaimcfg.APIConfig{
			Enabled: false,
			Host:    "127.0.0.1",
			Port:    5579,
		},
		L1ToL2Watchdog: autoclaimcfg.L1ToL2Watchdog{
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
	newEthTxManager    func(context.Context, autoclaimcfg.ClaimerConfig) (EthTxManager, error)
	startEthTxManager  func(context.Context, EthTxManager)
	startClaimer       func(context.Context, autoclaimtypes.Claimer)
	startWatchdog      func(context.Context, *watchdog.L1ToL2)
	newClaimer         func(autoclaimtypes.ClaimerTarget)
	newPolicy          func(autoclaimcfg.PolicyName, autoclaimcfg.PolicyConfig, ...policy.RegistryOption)
	newTargetSimulator func(
		simulator.Client,
		autoclaimtypes.ProofPreparer,
		autoclaimtypes.ClaimerTarget,
		common.Address,
	)
	targetSimulatorErr error
	newAPI             func()
	startAPI           func(context.Context)
}

func testFactories(hooks *factoryHooks) Factories {
	if hooks == nil {
		hooks = &factoryHooks{}
	}
	factories := Factories{
		OpenStorage: func(aggkitcommon.Logger, string, time.Duration) (autoclaimtypes.Storage, error) {
			return &fakeStorage{}, nil
		},
		NewRPCClient: func(ctx context.Context, logger aggkitcommon.Logger, cfg ethermanconfig.RPCClientConfig) (
			aggkittypes.EthClienter, error,
		) {
			if hooks.newRPCClient != nil {
				return hooks.newRPCClient(ctx, logger, cfg)
			}
			return nil, nil
		},
		NewEthTxManager: func(ctx context.Context, cfg autoclaimcfg.ClaimerConfig) (EthTxManager, error) {
			if hooks.newEthTxManager != nil {
				return hooks.newEthTxManager(ctx, cfg)
			}
			return &fakeEthTxManager{}, nil
		},
		StartEthTxManager: func(ctx context.Context, txManager EthTxManager) {
			if hooks.startEthTxManager != nil {
				hooks.startEthTxManager(ctx, txManager)
			}
		},
		NewPolicy: func(
			name autoclaimcfg.PolicyName,
			cfg autoclaimcfg.PolicyConfig,
			options ...policy.RegistryOption,
		) (autoclaimtypes.Policy, error) {
			if hooks.newPolicy != nil {
				hooks.newPolicy(name, cfg, options...)
			}
			return fakePolicy{}, nil
		},
		NewTargetClaimReader: func(common.Address, aggkittypes.BaseEthereumClienter) (
			autoclaimtypes.TargetClaimReader, error,
		) {
			return fakeTargetClaimReader{}, nil
		},
		NewTargetSimulator: func(
			client simulator.Client,
			proofPreparer autoclaimtypes.ProofPreparer,
			target autoclaimtypes.ClaimerTarget,
			from common.Address,
		) (policy.TargetSimulator, error) {
			if hooks.newTargetSimulator != nil {
				hooks.newTargetSimulator(client, proofPreparer, target, from)
			}
			if hooks.targetSimulatorErr != nil {
				return nil, hooks.targetSimulatorErr
			}
			return fakeTargetSimulator{}, nil
		},
		NewSender: func(
			autoclaimtypes.Storage,
			EthTxManager,
			autoclaimtypes.TargetClaimReader,
		) (autoclaimtypes.ClaimSender, error) {
			return fakeSender{}, nil
		},
		NewClaimer: func(
			target autoclaimtypes.ClaimerTarget,
			_ autoclaimtypes.Storage,
			_ autoclaimtypes.Policy,
			_ autoclaimtypes.ProofPreparer,
			_ autoclaimtypes.ClaimSender,
			_ ...claimer.Option,
		) (autoclaimtypes.Claimer, error) {
			if hooks.newClaimer != nil {
				hooks.newClaimer(target)
			}
			return fakeClaimer{target: target}, nil
		},
		StartClaimer: func(ctx context.Context, runtimeClaimer autoclaimtypes.Claimer) {
			if hooks.startClaimer != nil {
				hooks.startClaimer(ctx, runtimeClaimer)
			}
		},
		StartWatchdog: func(ctx context.Context, runner *watchdog.L1ToL2) {
			if hooks.startWatchdog != nil {
				hooks.startWatchdog(ctx, runner)
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
		StartAPI: func(ctx context.Context, _ *api.API) {
			if hooks.startAPI != nil {
				hooks.startAPI(ctx)
			}
		},
		Go: func(fn func()) {
			go fn()
		},
	}
	return factories
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

type fakeL2GERSync struct{}

func (fakeL2GERSync) GetFirstGERAfterL1InfoTreeIndex(
	_ context.Context,
	atOrAfterL1InfoTreeIndex uint32,
) (l2gersync.GlobalExitRootInfo, error) {
	return l2gersync.GlobalExitRootInfo{L1InfoTreeIndex: atOrAfterL1InfoTreeIndex}, nil
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
