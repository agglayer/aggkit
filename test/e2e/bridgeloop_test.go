package e2e

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	gosigner "github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The bridge_loop_tester e2e test drives the tool's own library API around one closed ring
// 0 -> 1 -> 2 -> 0 on the anvil-2chains env. That single ring covers all three bridge directions
// (L1->L2, L2->L2, L2->L1) in one cycle, and it is walked twice concurrently: once for ETH and
// once for an ERC20 the tool deploys and mints itself.
const (
	// bridgeLoopProxyURL is the aggkit-proxy REST base URL (bridge + tracker components) of the
	// anvil-2chains env, the same host port proxy_tracker_test.go uses.
	bridgeLoopProxyURL = proxyTrackerBaseURL

	// bridgeLoopL1RPC / bridgeLoopL2ARPC / bridgeLoopL2BRPC are the host-side JSON-RPC endpoints
	// of the env's three networks. The tool dials them itself (that is the code path under test),
	// so they are named here rather than taken from the already-dialed env clients.
	bridgeLoopL1RPC   = "http://127.0.0.1:13545"
	bridgeLoopL2ARPC  = "http://127.0.0.1:14545"
	bridgeLoopL2BRPC  = "http://127.0.0.1:15545"
	bridgeLoopL1Name  = "l1-anvil"
	bridgeLoopL2AName = "l2-anvil-001"
	bridgeLoopL2BName = "l2-anvil-002"

	// bridgeLoopKeystorePass encrypts the keystores this test writes for the tool's go_signer
	// "local" signer configs. The keys are the env's own public test keys, so the password is
	// only there because the keystore format requires one.
	bridgeLoopKeystorePass = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"

	// bridgeLoopETHAmount is what the ETH loop moves on every hop: 1 ETH. It is deliberately three
	// orders of magnitude above the hop engine's native gas slack (1e15 wei) so a native balance
	// delta that is short by a claim's gas is still unambiguously "the value arrived". Both L2
	// bridges hold ~3.4e38 wei of native liquidity in this snapshot, and the L1 bridge only ever
	// has to release what hop 0->1 of the same cycle deposited into it.
	bridgeLoopETHAmount = 1_000_000_000_000_000_000 // 1e18
	// bridgeLoopERC20Amount is what the ERC20 loop moves on every hop: 1 token (18 decimals).
	bridgeLoopERC20Amount = 1_000_000_000_000_000_000 // 1e18

	// bridgeLoopGracePeriod is Global.ManualGracePeriod: for the two manual hops it is the window
	// during which nothing may claim the deposit (the tool's core negative assertion), and for the
	// auto hop it is how long the network-2 Auto Claim service has to claim it once the claim
	// proof is available.
	bridgeLoopGracePeriod = 3 * time.Minute
	// bridgeLoopHopTimeout is the total budget of a single hop, which must comfortably exceed
	// bridgeLoopGracePeriod plus the cross-network settlement waits (an L2 local exit root has to
	// settle to L1 through agglayer before an L2-sourced hop's claim proof exists).
	bridgeLoopHopTimeout = 22 * time.Minute
	// bridgeLoopPollInterval is how often the tool re-polls a readiness gate.
	bridgeLoopPollInterval = 3 * time.Second

	// bridgeLoopTestTimeout bounds the whole test, leaving headroom inside the 60m `go test`
	// timeout the CI job and the step's acceptance criteria use.
	bridgeLoopTestTimeout = 50 * time.Minute

	// bridgeLoopL1GasAllowance is how much L1 native balance the ring-closure assertion lets the
	// loops burn on gas across one full cycle: the ERC20 deploy and mint, plus a bridge and a claim
	// per loop. That is well under 4M gas at the ~1 gwei this env's L1 charges (under 4e15 wei), so
	// 1e17 is a generous cap that is still an order of magnitude below bridgeLoopETHAmount - "the
	// ETH came back" and "the ETH did not come back" can never be confused.
	bridgeLoopL1GasAllowance = 100_000_000_000_000_000 // 1e17

	// bridgeLoopPrimeAmount is the ETH each priming hop moves. Priming exists only to produce
	// claim activity on an L2 (see primeBridgeLoopSettlement), so the amount is irrelevant except
	// that it must exceed what the claim costs in gas: the priming hop claims to its own account,
	// so a credit smaller than the claim's fee shows up as a native balance that went *down* and
	// the hop engine (correctly) reports it as an unreconciled destination balance. A claim on
	// these anvil L2s costs on the order of 2e14 wei, so 0.1 ETH leaves three orders of magnitude
	// of headroom.
	bridgeLoopPrimeAmount = 100_000_000_000_000_000 // 1e17
	// bridgeLoopPrimeGrace is the priming hops' grace period. Priming asserts nothing; it just
	// needs to bridge and claim quickly, so its negative-assertion window is minimal.
	bridgeLoopPrimeGrace = 2 * time.Second
	// bridgeLoopPrimeHopTimeout bounds one priming hop, and bridgeLoopPrimeInterval is the pause
	// between successive priming hops on one network.
	bridgeLoopPrimeHopTimeout = 8 * time.Minute
	bridgeLoopPrimeInterval   = 10 * time.Second

	// bridgeLoopGasLimitOffset is added to every eth_estimateGas result the tool gets, on every
	// network. It is not optional here: bridgeAsset is called with forceUpdateGlobalExitRoot =
	// true, so it also writes the global exit root, and when two bridges land in the same block
	// the second one pays materially more than its own estimate predicted (the first already
	// warmed the storage the estimate was priced against) and reverts OutOfGas *inside*
	// updateExitRoot. This test bridges concurrently by design - two loops plus background
	// priming - so it hits that race reliably. test/e2e/bridge_utils.go works around the same
	// thing by pinning l1BridgeGasLimit to 500_000.
	bridgeLoopGasLimitOffset = 300_000
)

// bridgeLoopKeys holds every key the test checks out of the env's pools: one signing key per
// network for the tool's own loops, and one bridge/claim pair per L2 for the priming activity.
type bridgeLoopKeys struct {
	loopL1  *ecdsa.PrivateKey
	loopL2A *ecdsa.PrivateKey
	loopL2B *ecdsa.PrivateKey

	primeL1A *ecdsa.PrivateKey
	primeL1B *ecdsa.PrivateKey
	primeL2A *ecdsa.PrivateKey
	primeL2B *ecdsa.PrivateKey
}

// TestBridgeLoopFullCycle drives tools/bridge_loop_tester's library API through one full circular
// cycle of the ring 0 -> 1 -> 2 -> 0 on the anvil-2chains env, for an ETH loop and an ERC20 loop
// at the same time, and asserts on the Report the tool returns rather than on its log output.
//
// What the ring proves, in one pass:
//
//   - hop 0->1 is L1->L2 with Claim = "manual": nothing may claim it for the whole grace period,
//     then the tool claims it itself;
//   - hop 1->2 is L2->L2 with Claim = "auto": the network-2 aggkit node runs Auto Claim (enabled
//     for this test only, via the same harness machinery autoclaim_test.go uses) and must claim
//     it within the grace period;
//   - hop 2->0 is L2->L1 with Claim = "manual": again nobody else may claim it.
//
// Both loops share one NetworkClient per (network, signing key) pair - the orchestrator's own pool
// - so the run also exercises the per-instance nonce serialization two concurrent loops on one
// account depend on.
func TestBridgeLoopFullCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := loadBridgeLoopTestEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), bridgeLoopTestTimeout)
	defer cancel()

	// Auto Claim runs only on the network-2 (aggkit-002) node, and only through the L2ToLx
	// detector, so the single L2->L2 hop into network 2 is the only hop any service claims. Every
	// other hop's destination (network 1 and network 0) has no claimer at all, which is what makes
	// the two manual hops' negative assertion meaningful rather than incidental.
	enableAutoClaimL2ToL2(t, ctx, env, "allow-all")
	waitForBridgeServiceSynced(ctx, t)

	keys := checkoutBridgeLoopKeys(t, env)
	cfg := newBridgeLoopConfig(t, env, keys)

	logger := log.WithFields("test", "bridge-loop")

	// The 2-chain env has no batcher/proposer, so neither L2's finalized head advances on its own
	// and an aggsender only certifies up to min(lastBridgeBlock, lastClaimBlock). A hop whose
	// source is an L2 therefore needs an unrelated claim to land on that L2 *after* its bridge,
	// or the bridge's local exit root never settles to L1 and the hop's claim proof never exists.
	// Drive that activity in the background on both L2s, exactly as autoclaim_test.go's
	// primeL2ClaimSyncer / primeL2ToL2SourceSettlement do. This is an environment precondition
	// only: it bridges and claims its own deposits with its own keys and never touches the
	// deposits under test.
	primeEngineL2B, stopPriming := startBridgeLoopPriming(t, ctx, env, keys, logger)
	defer stopPriming()

	orchestrator, err := bridgelooptester.NewOrchestrator(ctx, cfg, bridgelooptester.OrchestratorDeps{
		Logger: logger,
		// One attempt per hop: a retry would hide a real stall behind a second 22-minute budget,
		// and this test wants the first failure, not the third.
		HopAttempts: 1,
	})
	require.NoError(t, err, "build the bridge_loop_tester orchestrator")
	t.Cleanup(orchestrator.Close)

	l1BalanceBefore := bridgeLoopNativeBalance(ctx, t, env.Clients.L1, crypto.PubkeyToAddress(keys.loopL1.PublicKey))

	report, runErr := orchestrator.Run(ctx)
	require.NotNil(t, report, "Run must return a report even when it fails")
	logBridgeLoopReport(t, report)
	require.NoError(t, runErr, "bridge_loop_tester run: %s", report.Err)

	assertBridgeLoopPreflight(t, report)
	assertBridgeLoopTotals(t, report)
	assertBridgeLoopHops(t, report)
	assertBridgeLoopRingClosure(ctx, t, env, keys, report, l1BalanceBefore)
	assertBridgeLoopState(t, cfg, orchestrator, report)
	assertBridgeLoopResume(ctx, t, primeEngineL2B, report)
}

// assertBridgeLoopResume feeds a finished hop's own checkpoint back into a hop engine as
// HopRequest.Resume, so the checkpoint the run produced is proved to round-trip through the resume
// path and not just through JSON.
//
// It uses the ETH loop's last hop (2 -> 0), whose checkpoint is terminal, so the engine replays it
// and returns without touching either chain - which is exactly the property being asserted, and
// which is why this is safe to run after the ring has already closed. The deeper resume states
// (bridging, awaiting-claim, submitting-claim) each re-drive real on-chain work and are covered by
// the hop engine's unit tests, not here: re-driving them against a closed ring would move value the
// ring no longer has.
func assertBridgeLoopResume(
	ctx context.Context,
	t *testing.T,
	engine *bridgelooptester.HopEngine,
	report *bridgelooptester.Report,
) {
	t.Helper()

	eth := report.Loop("eth-ring")
	require.NotNil(t, eth)
	hops := eth.AllHops()
	require.Len(t, hops, 3)
	last := hops[2]
	require.Equal(t, uint32(0), last.Destination, "the ETH loop's last hop must land on L1")

	checkpoint := last.Checkpoint
	result, err := engine.RunHop(ctx, bridgelooptester.HopRequest{
		LoopName:           "eth-ring-resume",
		Iteration:          last.Iteration,
		HopIndex:           last.HopIndex,
		Hop:                bridgelooptester.Hop{Source: last.Source, Destination: last.Destination, Claim: last.ClaimMode},
		Asset:              last.Asset,
		Amount:             last.Amount,
		DestinationAddress: last.DestinationAddress,
		Resume:             &checkpoint,
	})
	require.NoError(t, err, "resuming a verified checkpoint must succeed without re-driving the hop")
	require.NotNil(t, result)

	require.True(t, result.Resumed, "the engine must report the hop as resumed")
	require.Equal(t, bridgelooptester.HopStateVerified, result.ResumedFrom)
	require.Equal(t, bridgelooptester.HopStateVerified, result.FinalState)
	require.Equal(t, bridgelooptester.HopOutcomeSuccess, result.Outcome)
	require.Equal(t, checkpoint.BridgeTxHash, result.BridgeTxHash,
		"the resumed hop must recover the bridge transaction from the checkpoint")
	require.Equal(t, checkpoint.BridgeTxNonce, result.BridgeTxNonce)
	require.Equal(t, checkpoint.ClaimTxHash, result.ClaimTxHash)
	require.Equal(t, checkpoint.StartedAt, result.StartedAt,
		"a resumed hop keeps the start time its checkpoint recorded")
	require.Equal(t, bridgelooptester.ClaimActorNone, result.ClaimedBy,
		"replaying a terminal checkpoint must not re-attribute the claim")
}

// loadBridgeLoopTestEnv returns the loaded env, requiring the two-L2 topology the ring needs.
func loadBridgeLoopTestEnv(t *testing.T) *envs.Env {
	t.Helper()
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")
	if testEnv.L2B == nil {
		t.Skip("the bridge_loop_tester full-cycle test needs a multi-chain env (L2B must be non-nil)")
	}

	return testEnv
}

// checkoutBridgeLoopKeys takes every key the test needs from the env's pre-funded pools, and
// returns them to the pools on cleanup. Loop keys and priming keys are deliberately distinct on
// every network: NetworkClient serializes nonces per instance, and the priming clients are
// separate instances, so sharing an account between them would produce nonce collisions.
func checkoutBridgeLoopKeys(t *testing.T, env *envs.Env) *bridgeLoopKeys {
	t.Helper()

	checkoutL1 := func() *ecdsa.PrivateKey {
		_, key, err := env.Keys.L1Keys.Checkout()
		require.NoError(t, err, "check out an L1 key")
		t.Cleanup(func() { env.Keys.L1Keys.Return(key) })

		return key
	}
	checkoutL2 := func(pool *envs.KeyPool, label string) *ecdsa.PrivateKey {
		_, key, err := pool.Checkout()
		require.NoError(t, err, "check out a %s key", label)
		t.Cleanup(func() { pool.Return(key) })

		return key
	}

	return &bridgeLoopKeys{
		loopL1:   checkoutL1(),
		loopL2A:  checkoutL2(env.L2.Keys, "L2A"),
		loopL2B:  checkoutL2(env.L2B.Keys, "L2B"),
		primeL1A: checkoutL1(),
		primeL1B: checkoutL1(),
		primeL2A: checkoutL2(env.L2.Keys, "L2A priming"),
		primeL2B: checkoutL2(env.L2B.Keys, "L2B priming"),
	}
}

// newBridgeLoopConfig builds the tool's Config programmatically from the loaded env: three
// networks (L1/L2A/L2B, addresses and IDs taken from the env, never hardcoded) and two loops that
// walk the same closed ring 0 -> 1 -> 2 -> 0, one moving ETH and one moving an ERC20 the tool
// deploys on L1 itself.
//
// Each network's Signer is a real go_signer "local" keystore config written to the test's temp
// directory from a pre-funded pool key, so the run exercises NewNetworkClient's dial-plus-go_signer
// path rather than an injected backend.
func newBridgeLoopConfig(t *testing.T, env *envs.Env, keys *bridgeLoopKeys) *bridgelooptester.Config {
	t.Helper()

	keystoreDir := t.TempDir()
	l1BridgeAddr := bridgeLoopL1BridgeAddress(t, env)
	tokenOrigin := uint32(0)

	ring := []bridgelooptester.Hop{
		{Source: 0, Destination: env.L2.NetworkID, Claim: bridgelooptester.ClaimManual},
		{Source: env.L2.NetworkID, Destination: env.L2B.NetworkID, Claim: bridgelooptester.ClaimAuto},
		{Source: env.L2B.NetworkID, Destination: 0, Claim: bridgelooptester.ClaimManual},
	}

	return &bridgelooptester.Config{
		Global: bridgelooptester.Global{
			ProxyURL:          bridgeLoopProxyURL,
			LogLevel:          "info",
			Iterations:        1,
			LoopDelay:         cfgtypes.Duration{Duration: 5 * time.Second},
			HopTimeout:        cfgtypes.Duration{Duration: bridgeLoopHopTimeout},
			PollInterval:      cfgtypes.Duration{Duration: bridgeLoopPollInterval},
			ManualGracePeriod: cfgtypes.Duration{Duration: bridgeLoopGracePeriod},
			HopAttempts:       1,
			StatePath:         filepath.Join(t.TempDir(), "bridge-loop-state.json"),
		},
		Networks: []bridgelooptester.Network{
			{
				NetworkID:      0,
				Name:           bridgeLoopL1Name,
				RPCURL:         bridgeLoopL1RPC,
				BridgeAddr:     l1BridgeAddr,
				ChainID:        env.L1.ChainID.Uint64(),
				GasLimitOffset: bridgeLoopGasLimitOffset,
				Signer:         bridgeLoopSignerConfig(t, keystoreDir, "l1", keys.loopL1),
			},
			{
				NetworkID:      env.L2.NetworkID,
				Name:           bridgeLoopL2AName,
				RPCURL:         bridgeLoopL2ARPC,
				BridgeAddr:     env.L2.Contracts.L2BridgeAddress,
				ChainID:        env.L2.ChainID.Uint64(),
				GasLimitOffset: bridgeLoopGasLimitOffset,
				Signer:         bridgeLoopSignerConfig(t, keystoreDir, "l2a", keys.loopL2A),
			},
			{
				NetworkID:      env.L2B.NetworkID,
				Name:           bridgeLoopL2BName,
				RPCURL:         bridgeLoopL2BRPC,
				BridgeAddr:     env.L2B.Contracts.L2BridgeAddress,
				ChainID:        env.L2B.ChainID.Uint64(),
				GasLimitOffset: bridgeLoopGasLimitOffset,
				Signer:         bridgeLoopSignerConfig(t, keystoreDir, "l2b", keys.loopL2B),
			},
		},
		Loops: []bridgelooptester.Loop{
			{
				Name:    "eth-ring",
				Asset:   bridgelooptester.AssetETH,
				Amount:  bridgelooptester.NewWeiAmount(bridgeLoopETHAmount),
				Enabled: true,
				Hops:    ring,
			},
			{
				Name:               "erc20-ring",
				Asset:              bridgelooptester.AssetERC20,
				Amount:             bridgelooptester.NewWeiAmount(bridgeLoopERC20Amount),
				Enabled:            true,
				TokenOriginNetwork: &tokenOrigin,
				Hops:               ring,
			},
		},
	}
}

// bridgeLoopL1BridgeAddress reads the L1 bridge address out of the loaded env's own summary.json,
// so the test never hardcodes a snapshot contract address. (envs.Env exposes the L1 bridge only as
// a bound contract, whose address it does not re-publish, hence reading the file the loader itself
// read.)
func bridgeLoopL1BridgeAddress(t *testing.T, env *envs.Env) common.Address {
	t.Helper()

	var summary struct {
		Networks struct {
			L1 struct {
				Contracts struct {
					Bridge string `json:"bridge"`
				} `json:"contracts"`
			} `json:"l1"`
		} `json:"networks"`
	}
	raw, readErr := os.ReadFile(filepath.Join(env.EnvDir, "summary.json"))
	require.NoError(t, readErr, "read the env's summary.json for the L1 bridge address")
	require.NoError(t, json.Unmarshal(raw, &summary), "parse the env's summary.json")
	l1Bridge := common.HexToAddress(summary.Networks.L1.Contracts.Bridge)
	require.NotEqual(t, common.Address{}, l1Bridge, "summary.json must name the L1 bridge address")

	return l1Bridge
}

// bridgeLoopSignerConfig writes key into a keystore file under dir and returns the go_signer
// "local" SignerConfig pointing at it, which is what NewNetworkClient consumes.
func bridgeLoopSignerConfig(
	t *testing.T, dir, label string, key *ecdsa.PrivateKey,
) signertypes.SignerConfig {
	t.Helper()

	encrypted, err := keystore.EncryptKey(&keystore.Key{
		Id:         uuid.New(),
		Address:    crypto.PubkeyToAddress(key.PublicKey),
		PrivateKey: key,
	}, bridgeLoopKeystorePass, keystore.LightScryptN, keystore.LightScryptP)
	require.NoError(t, err, "encrypt the %s signing key", label)

	path := filepath.Join(dir, label+".keystore")
	require.NoError(t, os.WriteFile(path, encrypted, 0o600), "write the %s keystore", label)

	return gosigner.NewLocalSignerConfig(path, bridgeLoopKeystorePass)
}

// assertBridgeLoopPreflight asserts what the run's own live, read-only preflight found. Two of its
// findings were assumptions until this test ran: that both L2s' gas token really is ether (so the
// ETH loop's native bridging path is the right one), and that the proxy can route network 0 at all.
func assertBridgeLoopPreflight(t *testing.T, report *bridgelooptester.Report) {
	t.Helper()
	require.NotNil(t, report.Preflight, "Run must record its preflight in the report")
	preflight := report.Preflight

	require.True(t, preflight.ProxyHealthy, "the proxy's /tracker/v1/health must answer")
	require.True(t, preflight.L1BridgeServiceAvailable,
		"the proxy must expose a network_id=0 bridge service, or no hop through L1 can be observed")
	require.True(t, preflight.NetworkZeroParticipates, "the ring routes through network 0")
	require.Len(t, preflight.Networks, 3, "the preflight must cover all three configured networks")

	for _, network := range preflight.Networks {
		require.True(t, network.ProxyReachable,
			"the proxy must publish a bridge config for network %d", network.NetworkID)
		require.True(t, network.GasTokenIsEther,
			"network %d (%s) reported gasTokenAddress=%s: the ETH loop's native bridging path is only "+
				"valid where the gas token is ether", network.NetworkID, network.Name, network.GasTokenAddress)
		require.Equal(t, network.NetworkID, network.BridgeNetworkID,
			"the bridge on network %d reports networkID %d", network.NetworkID, network.BridgeNetworkID)
		require.Equal(t, network.BridgeAddrConfigured, network.BridgeAddrReported,
			"the proxy and the config must agree on network %d's bridge address", network.NetworkID)
	}
}

// assertBridgeLoopTotals asserts the run-level counters: two loops, one closed ring each, three
// hops each, no failure of any class, and the claim work split between the tool and the autoclaim
// service exactly as the ring's claim modes declare.
func assertBridgeLoopTotals(t *testing.T, report *bridgelooptester.Report) {
	t.Helper()
	totals := report.Totals

	require.True(t, report.Succeeded(), "the run must succeed: %s", report.Err)
	require.False(t, report.Cancelled, "the run must finish its iterations, not be cancelled")
	require.Equal(t, 2, totals.LoopsRun)
	require.Equal(t, 0, totals.LoopsHalted)
	require.Equal(t, uint64(2), totals.CyclesAttempted)
	require.Equal(t, uint64(2), totals.CyclesCompleted)
	require.Equal(t, 6, totals.HopsAttempted, "3 hops x 2 loops, with no retries")
	require.Equal(t, 6, totals.HopsSucceeded)
	require.Equal(t, 0, totals.HopsFailed)
	require.Equal(t, 0, totals.HopsRetried)
	require.Equal(t, 0, totals.HopsResumed)
	require.Equal(t, 2, totals.AutoClaimHops, "one auto hop (1->2) per loop")
	require.Equal(t, 4, totals.ManualClaimHops, "two manual hops (0->1, 2->0) per loop")
	require.Equal(t, 4, totals.ClaimedByTool, "the tool claims exactly the manual hops")
	require.Equal(t, 2, totals.ClaimedExternally,
		"the auto hops must be attributed to the Auto Claim service, not left unattributed")
	require.Equal(t, 0, totals.ClaimModeViolations)
	require.Equal(t, 0, totals.AmbiguousResumes)
	require.Equal(t, 0, totals.StrandedLoops)
}

// assertBridgeLoopHops asserts every hop of every loop in detail: the gates it passed, who claimed
// it, the balances it reconciled, and the checkpoint it ended on.
func assertBridgeLoopHops(t *testing.T, report *bridgelooptester.Report) {
	t.Helper()

	for i := range report.Loops {
		loop := &report.Loops[i]
		t.Run(loop.Name, func(t *testing.T) {
			require.False(t, loop.Halted, "loop %q halted: %s", loop.Name, loop.Err)
			require.Empty(t, loop.Err)
			require.Equal(t, []string{"0->1", "1->2", "2->0"}, loop.Route)
			require.Len(t, loop.Cycles, 1, "Iterations = 1 yields exactly one cycle")
			require.False(t, loop.ValueLocation.Stranded, "loop %q left value behind: %s",
				loop.Name, loop.ValueLocation.Detail)
			require.Equal(t, uint32(0), loop.ValueLocation.NetworkID,
				"a closed ring leaves the value back on its origin network")
			require.False(t, loop.ValueLocation.InFlight)

			if loop.Asset == bridgelooptester.AssetERC20 {
				require.True(t, loop.TokenDeployed, "the tool must deploy the ERC20 loop's own token")
				require.NotEqual(t, common.Address{}, loop.TokenAddress)
				require.NotNil(t, loop.TokenOriginNetwork)
				require.Equal(t, uint32(0), *loop.TokenOriginNetwork)
			} else {
				require.Nil(t, loop.TokenOriginNetwork, "an ETH loop must carry no token origin network")
			}

			cycle := loop.Cycles[0]
			require.Equal(t, uint64(1), cycle.Iteration)
			require.Equal(t, 0, cycle.StartHopIndex, "a fresh loop starts at hop 0")
			require.True(t, cycle.RingClosed, "the cycle must close the ring: %s", cycle.Err)
			require.Empty(t, cycle.Err)
			require.Len(t, cycle.Hops, 3, "one attempt per hop, no retries")

			for hopIndex, hop := range cycle.Hops {
				assertBridgeLoopHop(t, loop, hopIndex, hop)
			}
		})
	}
}

// assertBridgeLoopHop asserts one hop's HopResult.
func assertBridgeLoopHop(
	t *testing.T, loop *bridgelooptester.LoopReport, hopIndex int, hop *bridgelooptester.HopResult,
) {
	t.Helper()
	label := fmt.Sprintf("%s hop %d (%s)", loop.Name, hopIndex, hop.Route())

	require.Equal(t, hopIndex, hop.HopIndex, label)
	require.Equal(t, uint64(1), hop.Iteration, label)
	require.Equal(t, bridgelooptester.HopOutcomeSuccess, hop.Outcome, "%s: %s", label, hop.ErrMessage)
	require.Equal(t, bridgelooptester.HopStateVerified, hop.FinalState, label)
	require.Empty(t, hop.ErrMessage, label)
	require.Empty(t, hop.StalledGate, label)
	require.False(t, hop.ClaimModeViolated, label)
	require.False(t, hop.Resumed, label)
	require.Equal(t, loop.Asset, hop.Asset, label)

	// The bridge leg: a mined transaction, an asset leaf, and a deposit count the engine took
	// straight from the receipt's BridgeEvent rather than from a list endpoint.
	require.NotEqual(t, common.Hash{}, hop.BridgeTxHash, "%s: bridge tx hash", label)
	require.Positive(t, hop.BridgeBlockNumber, "%s: bridge block number", label)
	require.Positive(t, hop.BridgeGasUsed, "%s: bridge gas used", label)
	require.Equal(t, uint8(0), hop.LeafType, "%s: an asset bridge must emit leaf type 0", label)
	require.NotNil(t, hop.GlobalIndex, "%s: global index", label)
	require.Zero(t, hop.GlobalIndex.Cmp(bridgesync.GenerateGlobalIndexForNetworkID(hop.Source, hop.DepositCount)),
		"%s: the global index must be the canonical encoding of (source %d, deposit count %d)",
		label, hop.Source, hop.DepositCount)
	require.Equal(t, uint32(0), hop.EventOriginNetwork,
		"%s: every asset in this ring originates on network 0 (ether, and an L1-deployed ERC20)", label)

	// The readiness gates: the origin's own L1-info-tree index always, the destination's injected
	// leaf only when the destination is an L2 (an L1 destination needs no GER injection), and a
	// claim proof keyed on the leaf index that was actually injected.
	require.Positive(t, hop.L1InfoTreeIndex, "%s: L1 info tree index", label)
	if hop.Destination == 0 {
		require.True(t, hop.InjectedLeafSkipped,
			"%s: the GER-injection gate must be skipped for an L1 destination", label)
		require.Equal(t, hop.L1InfoTreeIndex, hop.InjectedLeafIndex,
			"%s: with the injection gate skipped, I' is I", label)
	} else {
		require.False(t, hop.InjectedLeafSkipped,
			"%s: the GER-injection gate must run for an L2 destination", label)
		require.GreaterOrEqual(t, hop.InjectedLeafIndex, hop.L1InfoTreeIndex,
			"%s: the actually-injected leaf index must be at least the polled one", label)
		require.Equal(t, hop.InjectedLeafIndex > hop.L1InfoTreeIndex, hop.InjectedLeafAdvanced,
			"%s: InjectedLeafAdvanced must report whether I' exceeded I", label)
	}
	require.NotEqual(t, common.Hash{}, hop.GlobalExitRoot, "%s: global exit root", label)

	// The claim leg: who claimed it must match the hop's configured expectation.
	require.False(t, hop.ClaimObservedAt.IsZero(), "%s: the claim must have been observed", label)
	switch hop.ClaimMode {
	case bridgelooptester.ClaimManual:
		require.Equal(t, bridgeLoopGracePeriod, hop.GracePeriod,
			"%s: a manual hop must record the configured grace period it sat out", label)
		require.Equal(t, bridgelooptester.ClaimActorTool, hop.ClaimedBy,
			"%s: a manual hop must be claimed by the tool itself", label)
		require.NotEqual(t, common.Hash{}, hop.ClaimTxHash, "%s: the tool's claim tx hash", label)
		require.Positive(t, hop.ClaimBlockNumber, "%s: claim block number", label)
		require.Positive(t, hop.ClaimGasUsed, "%s: claim gas used", label)
		require.NotNil(t, hop.ClaimGasCost, "%s: claim gas cost", label)
	case bridgelooptester.ClaimAuto:
		require.Equal(t, bridgelooptester.ClaimActorExternal, hop.ClaimedBy,
			"%s: an auto hop must be attributed to the external claimer that actually claimed it "+
				"(ClaimActorUnknown here means the tool could not identify the claimant at all)", label)
		require.NotEqual(t, common.Address{}, hop.ExternalClaimFromAddress,
			"%s: the external claimant's address must be resolvable", label)
		require.NotEqual(t, common.Hash{}, hop.ExternalClaimTxHash,
			"%s: the external claim's tx hash must be known", label)
		require.Zero(t, hop.ClaimGasUsed, "%s: the tool submitted no claim for an auto hop", label)
	default:
		t.Fatalf("%s: unexpected claim mode %q", label, hop.ClaimMode)
	}

	// The destination balance: an ERC20 credit is exact (fees never touch a token balance), a
	// native credit is checked against the gas allowance.
	require.True(t, hop.BalanceVerified, "%s: destination balance must be verified", label)
	require.NotNil(t, hop.DestinationBalanceAfter, "%s: destination balance after", label)
	if hop.Asset == bridgelooptester.AssetERC20 {
		require.NotEqual(t, common.Address{}, hop.SourceTokenAddress, "%s: source token", label)
		require.NotEqual(t, common.Address{}, hop.DestinationTokenAddress, "%s: destination token", label)
		if hop.BalanceExact {
			require.NotNil(t, hop.DestinationDelta, label)
			require.Zero(t, hop.DestinationDelta.Cmp(hop.Amount),
				"%s: an ERC20 credit must be exactly the hop amount", label)
		} else {
			// Only legitimate when the wrapped representation did not exist before the claim, so
			// there was no baseline to subtract.
			require.NotEmpty(t, hop.BalanceNote,
				"%s: a non-exact ERC20 balance check must say why", label)
			require.GreaterOrEqual(t, hop.DestinationBalanceAfter.Cmp(hop.Amount), 0, label)
		}
	} else {
		require.Equal(t, common.Address{}, hop.SourceTokenAddress,
			"%s: an ETH hop must resolve no token address", label)
		require.NotNil(t, hop.DestinationDelta, "%s: native destination delta", label)
		require.Positive(t, hop.DestinationDelta.Sign(),
			"%s: the native credit must have arrived", label)
	}

	assertBridgeLoopPhases(t, label, hop)
	assertBridgeLoopCheckpoint(t, label, hop)
}

// assertBridgeLoopPhases asserts the hop recorded a timing for exactly the phases its asset and
// claim mode imply, in execution order.
func assertBridgeLoopPhases(t *testing.T, label string, hop *bridgelooptester.HopResult) {
	t.Helper()

	seen := make([]bridgelooptester.HopPhase, 0, len(hop.Phases))
	for _, phase := range hop.Phases {
		require.False(t, phase.StartedAt.IsZero(), "%s: phase %q has no start time", label, phase.Phase)
		require.GreaterOrEqual(t, phase.Duration, time.Duration(0),
			"%s: phase %q has a negative duration", label, phase.Phase)
		seen = append(seen, phase.Phase)
	}

	// Every hop records the asset-resolution and balance-check phases, and an ERC20 hop always
	// records the approve phase (the allowance read happens inside it, whether or not it ends up
	// sending an approval).
	prefix := []bridgelooptester.HopPhase{
		bridgelooptester.PhaseResolveAsset,
		bridgelooptester.PhaseBalanceCheck,
	}
	if hop.Asset == bridgelooptester.AssetERC20 {
		prefix = append(prefix, bridgelooptester.PhaseApprove)
	}
	prefix = append(prefix, bridgelooptester.PhaseBridge, bridgelooptester.PhaseOriginIndex)
	if hop.Destination != 0 {
		prefix = append(prefix, bridgelooptester.PhaseGERInjection)
	}
	prefix = append(prefix, bridgelooptester.PhaseClaimProof)

	if hop.ClaimMode == bridgelooptester.ClaimManual {
		expected := concatPhases(prefix,
			bridgelooptester.PhaseManualGracePeriod,
			bridgelooptester.PhaseClaimSubmit,
			bridgelooptester.PhaseVerifyBalance)
		require.Equal(t, expected, seen, "%s: recorded phases", label)

		// The grace period is the assertion a manual hop exists to make, so it must really have
		// been sat out rather than short-circuited.
		for _, phase := range hop.Phases {
			if phase.Phase == bridgelooptester.PhaseManualGracePeriod {
				require.GreaterOrEqual(t, phase.Duration, bridgeLoopGracePeriod,
					"%s: the manual grace period must have been waited out in full - the whole point of "+
						"a manual hop is that nothing claimed the deposit for that entire window", label)
			}
		}

		return
	}

	// An auto hop has two legitimate shapes, and which one happens is a race the test must not
	// pretend to control: either the tool reached its claim decision first and then waited out the
	// auto-claim window (an auto-claim-wait phase), or the Auto Claim service had already claimed
	// the deposit by the time the tool got there (no wait phase at all, and no grace period
	// recorded, because none was needed). Both are successes; anything else is not.
	waited := concatPhases(prefix,
		bridgelooptester.PhaseAutoClaimWait, bridgelooptester.PhaseVerifyBalance)
	alreadyClaimed := concatPhases(prefix, bridgelooptester.PhaseVerifyBalance)
	require.Contains(t,
		[][]bridgelooptester.HopPhase{waited, alreadyClaimed}, seen,
		"%s: an auto hop must either wait out the auto-claim window or find the deposit already "+
			"claimed, and nothing else", label)
	if len(seen) == len(waited) {
		require.Equal(t, bridgeLoopGracePeriod, hop.GracePeriod,
			"%s: an auto hop that waited must record the window it was given", label)
	}
}

// concatPhases returns prefix followed by tail as a fresh slice, so two expected phase lists can
// share a prefix without one aliasing the other's backing array.
func concatPhases(
	prefix []bridgelooptester.HopPhase, tail ...bridgelooptester.HopPhase,
) []bridgelooptester.HopPhase {
	out := make([]bridgelooptester.HopPhase, 0, len(prefix)+len(tail))
	out = append(out, prefix...)

	return append(out, tail...)
}

// assertBridgeLoopCheckpoint asserts the hop's final checkpoint carries the identity a resume
// would need, and that it round-trips through the JSON encoding the state file uses.
func assertBridgeLoopCheckpoint(t *testing.T, label string, hop *bridgelooptester.HopResult) {
	t.Helper()
	checkpoint := hop.Checkpoint

	require.Equal(t, bridgelooptester.HopStateVerified, checkpoint.State, "%s: checkpoint state", label)
	require.Equal(t, hop.BridgeTxHash, checkpoint.BridgeTxHash, "%s: checkpoint bridge tx", label)
	require.Equal(t, hop.DepositCount, checkpoint.DepositCount, "%s: checkpoint deposit count", label)
	require.Equal(t, hop.L1InfoTreeIndex, checkpoint.L1InfoTreeIndex, "%s: checkpoint leaf index", label)
	require.Equal(t, hop.InjectedLeafIndex, checkpoint.InjectedLeafIndex,
		"%s: checkpoint injected leaf index", label)
	require.False(t, checkpoint.StartedAt.IsZero(), "%s: checkpoint start time", label)

	// Round-trip through the encoding the state file uses. The comparison is on the re-encoded
	// bytes rather than on the structs: a checkpoint's StartedAt comes from time.Now() and so
	// carries a monotonic reading and a Local location that JSON cannot represent, which makes the
	// decoded value non-identical under reflect.DeepEqual while losing nothing that matters. The
	// instant itself is compared separately, with time.Time.Equal.
	encoded, err := json.Marshal(checkpoint)
	require.NoError(t, err, "%s: marshal the checkpoint", label)
	var decoded bridgelooptester.HopCheckpoint
	require.NoError(t, json.Unmarshal(encoded, &decoded), "%s: unmarshal the checkpoint", label)
	reEncoded, err := json.Marshal(decoded)
	require.NoError(t, err, "%s: re-marshal the decoded checkpoint", label)
	require.JSONEq(t, string(encoded), string(reEncoded),
		"%s: the checkpoint must round-trip through the JSON encoding the state file uses", label)
	require.True(t, decoded.StartedAt.Equal(checkpoint.StartedAt),
		"%s: the checkpoint's start time must survive the round-trip (%s != %s)",
		label, decoded.StartedAt, checkpoint.StartedAt)
	require.Equal(t, checkpoint.State, decoded.State, label)
	require.Equal(t, checkpoint.BridgeTxHash, decoded.BridgeTxHash, label)
	require.Equal(t, checkpoint.BridgeTxNonce, decoded.BridgeTxNonce, label)
	require.Equal(t, checkpoint.ClaimTxHash, decoded.ClaimTxHash, label)
	require.Equal(t, checkpoint.DepositCount, decoded.DepositCount, label)
	require.Equal(t, checkpoint.L1InfoTreeIndex, decoded.L1InfoTreeIndex, label)
	require.Equal(t, checkpoint.InjectedLeafIndex, decoded.InjectedLeafIndex, label)
	require.Equal(t, checkpoint.SourceTokenAddress, decoded.SourceTokenAddress, label)
	require.Equal(t, checkpoint.DestinationTokenAddress, decoded.DestinationTokenAddress, label)
	require.Equal(t, checkpoint.DestinationBalanceBefore, decoded.DestinationBalanceBefore, label)
}

// assertBridgeLoopRingClosure asserts the value really came back to where it started: the ERC20
// balance on its origin network is exactly what it was before the cycle (a token balance is
// untouched by fees, so this is an exact assertion), and the L1 native balance is back to its
// starting value minus at most the gas allowance.
func assertBridgeLoopRingClosure(
	ctx context.Context,
	t *testing.T,
	env *envs.Env,
	keys *bridgeLoopKeys,
	report *bridgelooptester.Report,
	l1BalanceBefore *big.Int,
) {
	t.Helper()
	account := crypto.PubkeyToAddress(keys.loopL1.PublicKey)

	l1BalanceAfter := bridgeLoopNativeBalance(ctx, t, env.Clients.L1, account)
	spent := new(big.Int).Sub(l1BalanceBefore, l1BalanceAfter)
	t.Logf("ring closure: L1 native balance of %s went %s -> %s (net %s wei)",
		account, l1BalanceBefore, l1BalanceAfter, new(big.Int).Neg(spent))
	require.LessOrEqual(t, spent.Cmp(big.NewInt(bridgeLoopL1GasAllowance)), 0,
		"the ETH ring must return its %d wei to L1, leaving only gas behind, but the L1 balance of %s "+
			"dropped by %s wei", int64(bridgeLoopETHAmount), account, spent)

	erc20 := report.Loop("erc20-ring")
	require.NotNil(t, erc20, "the ERC20 loop must be reported")
	hops := erc20.AllHops()
	require.Len(t, hops, 3)

	// The last hop's destination is the ring's origin (network 0) and its destination token is the
	// original ERC20, so its post-claim balance is the loop's L1 token balance at rest. The first
	// hop's pre-bridge source balance is the same quantity before the cycle started.
	before := hops[0].SourceBalanceBefore
	after := hops[2].DestinationBalanceAfter
	require.NotNil(t, before, "the ERC20 loop's first hop must record its source balance")
	require.NotNil(t, after, "the ERC20 loop's last hop must record its destination balance")
	require.Equal(t, erc20.TokenAddress, hops[2].DestinationTokenAddress,
		"the last hop must deliver the original token back to its origin network")
	t.Logf("ring closure: L1 ERC20 %s balance of %s went %s -> %s",
		erc20.TokenAddress, account, before, after)
	require.Zero(t, before.Cmp(after),
		"the ERC20 ring must return exactly what it bridged: the L1 token balance was %s before the "+
			"cycle and %s after", before, after)
}

// assertBridgeLoopState asserts the run's persisted state file says what a restart would need: no
// loop stranded mid-ring, one completed cycle each, and the final hop's checkpoint on disk.
func assertBridgeLoopState(
	t *testing.T,
	cfg *bridgelooptester.Config,
	orchestrator *bridgelooptester.Orchestrator,
	report *bridgelooptester.Report,
) {
	t.Helper()

	raw, err := os.ReadFile(cfg.Global.StatePath)
	require.NoError(t, err, "the run must have persisted its state file at %s", cfg.Global.StatePath)
	var onDisk bridgelooptester.State
	require.NoError(t, json.Unmarshal(raw, &onDisk), "parse the persisted state file")
	require.Equal(t, bridgelooptester.StateVersion, onDisk.Version)

	for i := range report.Loops {
		loop := &report.Loops[i]
		record := onDisk.Loops[loop.Name]
		require.NotNil(t, record, "the state file must hold a record for loop %q", loop.Name)
		require.Equal(t, uint64(1), record.CyclesCompleted, "loop %q completed cycles", loop.Name)
		require.Equal(t, uint64(1), record.CyclesAttempted, "loop %q attempted cycles", loop.Name)
		require.Equal(t, 0, record.HopIndex,
			"loop %q closed its ring, so its resume cursor must be back at hop 0", loop.Name)
		require.Equal(t, uint32(0), record.ValueNetwork, "loop %q value network", loop.Name)
		require.False(t, record.Halted, "loop %q halted: %s", loop.Name, record.LastError)

		// The engine checkpoints on entry to every state, but a hop that finished is no longer in
		// flight: the orchestrator clears the record's checkpoint when it advances the cursor, and
		// again when the ring closes. So a completed cycle must leave nothing in flight - that is
		// what tells a restart there is no stranded hop to resume.
		require.Nil(t, record.InFlight,
			"loop %q closed its ring, so the state file must record no in-flight hop", loop.Name)

		if loop.Asset == bridgelooptester.AssetERC20 {
			token := onDisk.Tokens[loop.Name]
			require.NotNil(t, token, "the state file must record loop %q's deployed token", loop.Name)
			require.Equal(t, loop.TokenAddress, token.Address)
			require.Equal(t, uint32(0), token.OriginNetwork)
			require.NotEqual(t, common.Hash{}, token.DeployTxHash,
				"the token's deployment tx hash must be recorded")
			require.NotEmpty(t, token.Minted, "the tool must record what it minted")
		}
	}

	live := orchestrator.State()
	require.NotNil(t, live)
	require.Equal(t, len(onDisk.Loops), len(live.Loops),
		"the in-memory state and the persisted one must describe the same loops")
}

// bridgeLoopNativeBalance reads account's native balance at the latest block.
func bridgeLoopNativeBalance(
	ctx context.Context, t *testing.T, client *ethclient.Client, account common.Address,
) *big.Int {
	t.Helper()
	balance, err := client.BalanceAt(ctx, account, nil)
	require.NoError(t, err, "read the native balance of %s", account)

	return balance
}

// logBridgeLoopReport dumps the run's report as indented JSON, so a failure is diagnosable from
// the test output alone without re-reading the container logs.
func logBridgeLoopReport(t *testing.T, report *bridgelooptester.Report) {
	t.Helper()
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Logf("bridge_loop_tester report could not be encoded: %v", err)

		return
	}
	t.Logf("bridge_loop_tester report:\n%s", encoded)
}

// startBridgeLoopPriming drives background L1->L2 bridge-and-claim activity on both L2 networks
// and returns a function that stops it and waits for it to finish.
//
// Why this is needed: an aggsender certifies its L2 only up to
// min(lastBridgeSyncBlock, lastClaimSyncBlock), and in this batcher-less env the claim syncer only
// advances when it sees a claim land on that L2. A hop whose source is an L2 (1->2 and 2->0 here)
// therefore needs an unrelated claim on that L2 *after* its bridge, or the bridge's local exit root
// never settles to L1 and its claim proof never becomes available. autoclaim_test.go's
// primeL2ClaimSyncer and primeL2ToL2SourceSettlement solve the same problem the same way; this
// version drives the tool's own hop engine, once per L2, with its own keys.
// It returns the priming hop engine whose networks are L1 and L2B (the last hop's own pair, reused
// by assertBridgeLoopResume) alongside the stop function.
func startBridgeLoopPriming(
	t *testing.T,
	ctx context.Context,
	env *envs.Env,
	keys *bridgeLoopKeys,
	logger aggkitcommon.Logger,
) (*bridgelooptester.HopEngine, func()) {
	t.Helper()

	proxy, err := bridgelooptester.NewProxyClient(bridgelooptester.Global{ProxyURL: bridgeLoopProxyURL})
	require.NoError(t, err, "build the priming proxy client")

	l1BridgeAddr := bridgeLoopL1BridgeAddress(t, env)
	engineA := newBridgeLoopPrimeEngine(ctx, t, proxy, logger,
		primeNetwork{id: 0, name: bridgeLoopL1Name + "-prime-a", bridge: l1BridgeAddr,
			client: env.Clients.L1, key: keys.primeL1A},
		primeNetwork{id: env.L2.NetworkID, name: bridgeLoopL2AName + "-prime", bridge: env.L2.Contracts.L2BridgeAddress,
			client: env.L2.Client, key: keys.primeL2A},
	)
	engineB := newBridgeLoopPrimeEngine(ctx, t, proxy, logger,
		primeNetwork{id: 0, name: bridgeLoopL1Name + "-prime-b", bridge: l1BridgeAddr,
			client: env.Clients.L1, key: keys.primeL1B},
		primeNetwork{id: env.L2B.NetworkID, name: bridgeLoopL2BName + "-prime", bridge: env.L2B.Contracts.L2BridgeAddress,
			client: env.L2B.Client, key: keys.primeL2B},
	)

	stop := make(chan struct{})
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		primeBridgeLoopSettlement(ctx, engineA, env.L2.NetworkID, stop, logger)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		primeBridgeLoopSettlement(ctx, engineB, env.L2B.NetworkID, stop, logger)
	}()

	stopped := false

	return engineB, func() {
		if stopped {
			return
		}
		stopped = true
		close(stop)
		<-done
		<-done
	}
}

// primeNetwork is one network of a priming hop engine: which network it is, which bridge it uses,
// which already-dialed client talks to it, and which key signs for it.
type primeNetwork struct {
	id     uint32
	name   string
	bridge common.Address
	client *ethclient.Client
	key    *ecdsa.PrivateKey
}

// newBridgeLoopPrimeEngine builds a HopEngine over the env's already-dialed clients and injected
// signers. Unlike the loops' own clients (which the orchestrator dials from the config), these go
// through NewNetworkClientWithBackend, so the run exercises both network-client constructors.
func newBridgeLoopPrimeEngine(
	ctx context.Context,
	t *testing.T,
	proxy bridgelooptester.Proxy,
	logger aggkitcommon.Logger,
	networks ...primeNetwork,
) *bridgelooptester.HopEngine {
	t.Helper()

	hopNetworks := make(map[uint32]bridgelooptester.HopNetwork, len(networks))
	for _, network := range networks {
		chainID, err := network.client.ChainID(ctx)
		require.NoError(t, err, "read the chain id of %s", network.name)

		signer := gosigner.NewLocalSignFromPrivateKey(network.name, logger, network.key, chainID.Uint64())
		require.NoError(t, signer.Initialize(ctx), "initialize the %s priming signer", network.name)

		cfg := bridgelooptester.Network{
			NetworkID:      network.id,
			Name:           network.name,
			BridgeAddr:     network.bridge,
			ChainID:        chainID.Uint64(),
			GasLimitOffset: bridgeLoopGasLimitOffset,
		}
		client, err := bridgelooptester.NewNetworkClientWithBackend(ctx, cfg, network.client, signer, logger)
		require.NoError(t, err, "build the %s priming network client", network.name)
		t.Cleanup(client.Close)

		bridge, err := bridgelooptester.NewBridge(client, network.bridge)
		require.NoError(t, err, "bind the %s priming bridge", network.name)

		hopNetworks[network.id] = bridgelooptester.HopNetwork{Config: cfg, Client: client, Bridge: bridge}
	}

	engine, err := bridgelooptester.NewHopEngine(bridgelooptester.HopDeps{
		Networks: hopNetworks,
		Proxy:    proxy,
		Logger:   logger,
		Timings: bridgelooptester.HopTimings{
			PollInterval:      bridgeLoopPollInterval,
			HopTimeout:        bridgeLoopPrimeHopTimeout,
			ManualGracePeriod: bridgeLoopPrimeGrace,
		},
	})
	require.NoError(t, err, "build a priming hop engine")

	return engine
}

// primeBridgeLoopSettlement repeatedly bridges a small amount of ETH from L1 to destination and
// claims it, until stop is closed or ctx is done. A failure is logged and retried: this is an
// environment precondition, not an assertion, and aborting the test because a priming hop timed
// out would replace a real diagnosis with an unrelated one.
func primeBridgeLoopSettlement(
	ctx context.Context,
	engine *bridgelooptester.HopEngine,
	destination uint32,
	stop <-chan struct{},
	logger aggkitcommon.Logger,
) {
	for iteration := uint64(1); ; iteration++ {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		default:
		}

		result, err := engine.RunHop(ctx, bridgelooptester.HopRequest{
			LoopName:  fmt.Sprintf("prime-network-%d", destination),
			Iteration: iteration,
			Hop: bridgelooptester.Hop{
				Source: 0, Destination: destination, Claim: bridgelooptester.ClaimManual,
			},
			Asset:  bridgelooptester.AssetETH,
			Amount: big.NewInt(bridgeLoopPrimeAmount),
		})
		switch {
		case err != nil:
			logger.Warnf("bridge-loop priming: hop 0->%d failed (environment precondition only, "+
				"retrying): %v", destination, err)
		case result != nil:
			logger.Infof("bridge-loop priming: claimed 0->%d deposit %d in %s, advancing network %d's "+
				"claim syncer", destination, result.DepositCount, result.Duration, destination)
		}

		if !sleepOrStop(ctx, stop, bridgeLoopPrimeInterval) {
			return
		}
	}
}
