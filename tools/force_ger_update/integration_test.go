package force_ger_update

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/test/helpers"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// This file is a Tier-1 integration test (PLAN.md step S5): it wires the REAL Monitor and REAL
// Sender against a go-ethereum simulated L1 backend (test/helpers.NewSimulatedL1), with the
// ethtxmanager replaced by test/helpers' mock-that-actually-sends-through-the-simulated-client
// (test/helpers/ethtxmanmock_e2e.go). No real network, no docker/kurtosis.
//
// All assertions verify on-chain effects — UpdateL1InfoTree event logs emitted by the deployed GER
// contract, and the selector of the transaction that produced each log — rather than only
// tool-internal state.

const (
	// integrationDestinationNetwork is the bridgeMessage destinationNetwork used throughout: it
	// must not be 0 (the simulated bridge itself is deployed with networkID 0, i.e. L1/mainnet).
	integrationDestinationNetwork = uint32(1)

	// integrationMaxTimeWithoutGER (X) is kept short so the whole suite runs in a few seconds, but
	// long enough that the negative ("stays quiet") assertions below have comfortable margin
	// against the monitor's own detection latency (at most one EventPollInterval in polling mode).
	//
	// The tool has an inherent, PLAN.md-acknowledged race in polling mode: the in-flight guard is
	// released when the send is *confirmed mined*, and if that happens before the monitor's poll
	// loop has observed the resulting UpdateL1InfoTree event and reset lastGERUpdate, the next
	// CheckInterval tick still sees the stale (pre-reset) timestamp and legitimately fires a second,
	// "redundant (harmless)" send. Real deployments never hit this because a transaction takes far
	// longer to *mine* than the monitor takes to *detect* an event. This test must encode that same
	// invariant, and robustly (CI runners starve goroutines unpredictably), so what matters is the
	// RATIO, not absolute values:
	//   - integrationSenderPoll (how long the send stays "in flight" — the mock mines instantly, so
	//     the send is confirmed only on the sender's first Result poll, i.e. after one senderPoll) is
	//     kept MUCH larger than integrationEventPollInterval (how often the monitor re-scans). With a
	//     ~30x ratio, the monitor observes the event and resets the timer many poll cycles before the
	//     guard releases, even under heavy, proportional scheduler starvation — so no second send.
	//   - integrationEventPollInterval is also < integrationCheckInterval so detection generally beats
	//     the next elapsed-time evaluation regardless.
	// (The two subtests also run sequentially rather than in parallel — see TestForceGERUpdate — so a
	// constrained CI runner isn't driving two simulated backends + monitor/sender loops at once.)
	integrationMaxTimeWithoutGER = 2 * time.Second
	integrationCheckInterval     = 50 * time.Millisecond
	integrationEventPollInterval = 10 * time.Millisecond
	integrationSenderPoll        = 300 * time.Millisecond

	// integrationQuietWindow is how long the "no second send" assertions watch for, measured from
	// the moment the resetting event is first observed on-chain by the test. It is deliberately
	// well under integrationMaxTimeWithoutGER (leaving margin for the monitor's own detection
	// latency plus scheduling jitter): the tool is *designed* to force another update once a full
	// threshold has elapsed since the reset, so a window as long as (or longer than) the threshold
	// would eventually observe a legitimate second send and turn this negative assertion flaky/wrong.
	integrationQuietWindow = 1200 * time.Millisecond

	integrationEventuallyTimeout = 5 * time.Second
	integrationEventuallyTick    = 20 * time.Millisecond

	// integrationFundAmount funds the tool's dedicated ethtxmanager account and integrationBridgeAssetValue
	// is the native-asset amount used for the externally-sent BridgeAsset call (mirrors
	// test/e2e/bridge_utils.go's BridgeL1ToL2, which also uses a small fixed native-asset amount).
	integrationFundAmount       = "10000000000000000000" // 10 ETH
	integrationBridgeAssetValue = "100000000000000"      // 1e14 wei
	integrationSimulatedChainID = 1337
)

// integrationBridgeAbi is the agglayerbridge ABI, used both by the production Sender (indirectly,
// via NewSender) and directly here to pack the externally-sent bridgeAsset calldata and to compute
// the two function selectors asserted on below.
var integrationBridgeAbi = func() *abi.ABI {
	a, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	if err != nil {
		panic(fmt.Sprintf("parse agglayerbridge ABI: %v", err))
	}
	return a
}()

var (
	// integrationBridgeMessageSelector is the selector of the function the tool's Sender calls
	// (0x240ff378 per PLAN.md).
	integrationBridgeMessageSelector = integrationBridgeAbi.Methods[bridgeMessageFuncName].ID
	// integrationBridgeAssetSelector is the selector of the function used to simulate an
	// externally-sent forced GER update (a regular user's bridge deposit with
	// forceUpdateGlobalExitRoot = true).
	integrationBridgeAssetSelector = integrationBridgeAbi.Methods["bridgeAsset"].ID
)

// integrationEnv bundles a deployed simulated L1 (bridge + GER manager) together with the two
// accounts and the mock ethtxmanager needed to drive the real Monitor/Sender against it.
type integrationEnv struct {
	client      *helpers.SimulatedBackendWithMutex
	l1Client    aggkittypes.BaseEthereumClienter
	gerAddr     common.Address
	gerContract *agglayerger.Agglayerger
	bridgeAddr  common.Address

	// toolAuth is a dedicated, freshly-funded account used exclusively as the tool's ethtxmanager
	// signer, so that every bridgeMessage transaction observed on-chain is unambiguously
	// attributable to the tool.
	toolAuth *bind.TransactOpts
	// externalAuth is NewSimulatedL1's user account, reused to simulate an unrelated third party
	// sending its own forced-GER-update bridge transaction (scenario 3).
	externalAuth *bind.TransactOpts

	ethTxManager *helpers.EthTxManager
}

// newIntegrationEnv deploys a fresh simulated L1 (bridge + GER manager, see
// test/helpers.NewSimulatedL1), funds a dedicated account for the tool's ethtxmanager, and wires a
// mock ethtxmanager that actually submits and mines transactions against the simulated backend
// (test/helpers/ethtxmanmock_e2e.go).
func newIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()

	rawClient, externalAuth, gerAddr, gerContract, bridgeAddr, _, _ := helpers.NewSimulatedL1(t)
	client := &helpers.SimulatedBackendWithMutex{Backend: rawClient}

	l1Client := etherman.NewDefaultEthClient(rawClient.Client(), nil, nil)

	toolAuth, err := helpers.CreateAccount(big.NewInt(integrationSimulatedChainID))
	require.NoError(t, err)

	fundAmount, ok := new(big.Int).SetString(integrationFundAmount, 10)
	require.True(t, ok)
	require.NoError(t, helpers.SendTx(context.Background(), client, externalAuth, &toolAuth.From, nil, fundAmount))

	ethTxManager := helpers.NewEthTxManMock(t, client, toolAuth)

	return &integrationEnv{
		client:       client,
		l1Client:     l1Client,
		gerAddr:      gerAddr,
		gerContract:  gerContract,
		bridgeAddr:   bridgeAddr,
		toolAuth:     toolAuth,
		externalAuth: externalAuth,
		ethTxManager: ethTxManager,
	}
}

// config builds the tool configuration for this env. When useWS is true, L1WSURL is set so the
// caller should also construct the Monitor with a non-nil wsClient (see newMonitor).
func (e *integrationEnv) config(useWS bool) ForceGERUpdateConfig {
	cfg := ForceGERUpdateConfig{
		GlobalExitRootManagerAddr: e.gerAddr,
		BridgeAddr:                e.bridgeAddr,
		MaxTimeWithoutGERUpdate:   configtypes.NewDuration(integrationMaxTimeWithoutGER),
		CheckInterval:             configtypes.NewDuration(integrationCheckInterval),
		EventPollInterval:         configtypes.NewDuration(integrationEventPollInterval),
		InitialLookbackBlocks:     100_000,
		FilterLogsChunkSize:       10_000,
		DestinationNetwork:        integrationDestinationNetwork,
		DryRun:                    false,
	}
	if useWS {
		// No real websocket is dialled: the simulated backend's in-process client supports
		// SubscribeFilterLogs directly, so the same client doubles as the "WS" client (see
		// newMonitor). Only cfg.L1WSURL's non-emptiness matters to select watch mode.
		cfg.L1WSURL = "ws://simulated-in-process"
	}
	return cfg
}

// newMonitor builds the real Monitor for cfg, wiring wsClient (the same in-process simulated
// client) iff cfg.L1WSURL is set, matching NewMonitor's documented contract.
func (e *integrationEnv) newMonitor(t *testing.T, cfg ForceGERUpdateConfig) *Monitor {
	t.Helper()

	var wsClient aggkittypes.BaseEthereumClienter
	if cfg.L1WSURL != "" {
		wsClient = e.l1Client
	}

	m, err := NewMonitor(cfg, e.l1Client, wsClient)
	require.NoError(t, err)
	return m
}

// newSender builds the real Sender for cfg, backed by the mock ethtxmanager (which actually
// submits and mines bridgeMessage transactions against the simulated backend).
func (e *integrationEnv) newSender(t *testing.T, cfg ForceGERUpdateConfig) *Sender {
	t.Helper()

	s, err := NewSender(cfg, e.ethTxManager, WithPollInterval(integrationSenderPoll))
	require.NoError(t, err)
	return s
}

// gerUpdateLogs returns every UpdateL1InfoTree event emitted so far by the deployed GER contract —
// the on-chain ground truth this test asserts against.
func (e *integrationEnv) gerUpdateLogs(t *testing.T) []*agglayerger.AgglayergerUpdateL1InfoTree {
	t.Helper()

	iter, err := e.gerContract.FilterUpdateL1InfoTree(&bind.FilterOpts{Context: context.Background()}, nil, nil)
	require.NoError(t, err)
	defer iter.Close()

	var logs []*agglayerger.AgglayergerUpdateL1InfoTree
	for iter.Next() {
		logs = append(logs, iter.Event)
	}
	require.NoError(t, iter.Error())
	return logs
}

// txSelector returns the 4-byte function selector of the transaction identified by txHash, letting
// the test tell apart a forced update produced by the tool's bridgeMessage call from one produced
// by an external bridgeAsset call.
func (e *integrationEnv) txSelector(t *testing.T, txHash common.Hash) []byte {
	t.Helper()

	tx, isPending, err := e.client.Client().TransactionByHash(context.Background(), txHash)
	require.NoError(t, err)
	require.False(t, isPending)

	data := tx.Data()
	require.GreaterOrEqual(t, len(data), 4)
	return data[:4]
}

// sendExternalForcedBridgeAsset submits (as externalAuth, unrelated to the tool's own account) a
// bridgeAsset deposit with forceUpdateGlobalExitRoot = true, simulating a normal user's bridge
// transaction that happens to force a GER update on its own — see
// test/e2e/bridge_utils.go's BridgeL1ToL2 for the equivalent real-network call shape.
func (e *integrationEnv) sendExternalForcedBridgeAsset(t *testing.T) {
	t.Helper()

	amount, ok := new(big.Int).SetString(integrationBridgeAssetValue, 10)
	require.True(t, ok)

	const forceUpdateGlobalExitRoot = true
	data, err := integrationBridgeAbi.Pack(
		"bridgeAsset",
		integrationDestinationNetwork,
		e.externalAuth.From,
		amount,
		common.Address{}, // native asset
		forceUpdateGlobalExitRoot,
		[]byte{},
	)
	require.NoError(t, err)

	require.NoError(t, helpers.SendTx(context.Background(), e.client, e.externalAuth, &e.bridgeAddr, data, amount))
}

// TestForceGERUpdate drives the real Monitor + Sender (via runLoop) against a simulated L1 and
// asserts the three PLAN.md S5 scenarios, once in watch mode (WS subscription) and once in polling
// mode (FilterLogs). The two subtests run SEQUENTIALLY (not parallel): each spins up its own
// simulated backend plus a monitor/sender loop, and running both at once starves the timing-
// sensitive poll/commit goroutines on constrained CI runners. Sequential keeps the whole test a few
// seconds — still well under 60s. The parent is intentionally NOT t.Parallel(): its subtests run
// sequentially, so it runs in the package's serial phase instead of contending with other tests.
func TestForceGERUpdate(t *testing.T) {
	t.Run("WatchMode", func(t *testing.T) {
		testForceGERUpdateScenarios(t, true)
	})

	t.Run("PollMode", func(t *testing.T) {
		testForceGERUpdateScenarios(t, false)
	})
}

// testForceGERUpdateScenarios exercises, against a single simulated L1 environment:
//
//  1. Boot with no prior UpdateL1InfoTree event: the tool must itself force a GER update (a
//     bridgeMessage transaction) and the GER contract must emit UpdateL1InfoTree.
//  2. Once the monitor observes that event (resetting its timer), no second send must happen
//     within the following (conservatively shortened) threshold window.
//  3. An externally-sent bridgeAsset call with forceUpdateGlobalExitRoot = true also resets the
//     timer — the tool must stay quiet afterwards too, even though a new UpdateL1InfoTree event
//     did land on-chain (just not one produced by the tool).
func testForceGERUpdateScenarios(t *testing.T, useWS bool) {
	t.Helper()

	env := newIntegrationEnv(t)
	cfg := env.config(useWS)

	monitor := env.newMonitor(t, cfg)
	sender := env.newSender(t, cfg)

	lastGERUpdate, err := monitor.LastGERUpdate()
	require.NoError(t, err)
	require.True(t, lastGERUpdate.IsZero(), "expected the GER to be considered stale before any on-chain event")

	ctx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan error, 1)
	go func() {
		loopDone <- runLoop(ctx, monitor, sender, lastGERUpdate,
			cfg.CheckInterval.Duration, cfg.MaxTimeWithoutGERUpdate.Duration)
	}()
	defer func() {
		cancel()
		select {
		case err := <-loopDone:
			require.NoError(t, err)
		case <-time.After(integrationEventuallyTimeout):
			t.Fatal("runLoop did not return promptly after ctx cancellation")
		}
	}()

	// --- Scenario 1: boot with no prior event -> the tool sends a bridgeMessage tx and the GER
	// contract emits UpdateL1InfoTree. ---
	require.Eventually(t, func() bool {
		return len(env.gerUpdateLogs(t)) >= 1
	}, integrationEventuallyTimeout, integrationEventuallyTick,
		"expected the tool to force a GER update because none had ever been observed")

	logsAfterFirstSend := env.gerUpdateLogs(t)
	require.Len(t, logsAfterFirstSend, 1, "exactly one UpdateL1InfoTree event expected after the boot send")
	require.Equal(t, integrationBridgeMessageSelector, env.txSelector(t, logsAfterFirstSend[0].Raw.TxHash),
		"the forced update must have been produced by a bridgeMessage call")

	// --- Scenario 2: the monitor observes that event and resets -> no second send within the next
	// threshold window. ---
	require.Never(t, func() bool {
		return len(env.gerUpdateLogs(t)) > 1
	}, integrationQuietWindow, integrationEventuallyTick,
		"the monitor's reset timer must suppress a second forced update for a full threshold window")

	// --- Scenario 3: an externally-sent BridgeAsset(..., forceUpdateGER=true, ...) resets the
	// timer so the tool stays quiet. ---
	env.sendExternalForcedBridgeAsset(t)

	require.Eventually(t, func() bool {
		return len(env.gerUpdateLogs(t)) >= 2
	}, integrationEventuallyTimeout, integrationEventuallyTick,
		"expected the external bridgeAsset call to itself force a GER update")

	logsAfterExternalSend := env.gerUpdateLogs(t)
	require.Len(t, logsAfterExternalSend, 2, "exactly one additional UpdateL1InfoTree event expected")
	require.Equal(t, integrationBridgeAssetSelector, env.txSelector(t, logsAfterExternalSend[1].Raw.TxHash),
		"the second GER update must have been produced by the external bridgeAsset call, not the tool")

	require.Never(t, func() bool {
		return len(env.gerUpdateLogs(t)) > 2
	}, integrationQuietWindow, integrationEventuallyTick,
		"the externally-triggered event must have reset the tool's timer, keeping it quiet")
}

// TestBootWithRecentEvent_NoUnnecessarySend covers the flip side of TestForceGERUpdate's scenario
// 1: booting when the GER was genuinely updated (by unrelated bridge activity) moments before the
// tool started must NOT force a spurious update. This exercises the real Monitor.LastGERUpdate
// boot scan and the real runLoop end-to-end (nothing here is a hand-injected/synthetic
// lastGERUpdate) against a simulated L1 that already has one on-chain UpdateL1InfoTree event
// before the tool ever runs — i.e. a normal restart while the GER is actually fine, as opposed to
// TestForceGERUpdate's cold-start (never-updated) case.
func TestBootWithRecentEvent_NoUnnecessarySend(t *testing.T) {
	env := newIntegrationEnv(t)
	cfg := env.config(false)

	// Simulate: the GER was genuinely updated (by unrelated bridge activity) moments before this
	// process starts.
	env.sendExternalForcedBridgeAsset(t)
	require.Len(t, env.gerUpdateLogs(t), 1, "expected exactly one pre-existing on-chain event before boot")

	monitor := env.newMonitor(t, cfg)
	sender := env.newSender(t, cfg)

	// Real boot scan: must find the pre-existing event and NOT report the GER as stale.
	lastGERUpdate, err := monitor.LastGERUpdate()
	require.NoError(t, err)
	require.False(t, lastGERUpdate.IsZero(), "boot scan must find the pre-existing event, not treat the GER as stale")

	ctx, cancel := context.WithCancel(context.Background())
	loopDone := make(chan error, 1)
	go func() {
		loopDone <- runLoop(ctx, monitor, sender, lastGERUpdate,
			cfg.CheckInterval.Duration, cfg.MaxTimeWithoutGERUpdate.Duration)
	}()
	defer func() {
		cancel()
		select {
		case err := <-loopDone:
			require.NoError(t, err)
		case <-time.After(integrationEventuallyTimeout):
			t.Fatal("runLoop did not return promptly after ctx cancellation")
		}
	}()

	require.Never(t, func() bool {
		return len(env.gerUpdateLogs(t)) > 1
	}, integrationQuietWindow, integrationEventuallyTick,
		"booting with a genuinely fresh GER must not force an unnecessary update")
}
