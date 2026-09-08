package e2e

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	gosigner "github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
)

var testEnv *envs.Env

// dumpContainerLogs writes "docker compose logs" output for every service in the loaded env's
// docker-compose.yml (discovered via Env.ComposeServices, i.e. "docker compose config --services")
// to test/e2e/<service>.log, relative to the test binary's working directory (test/e2e when run
// via `go test ./test/e2e/...`, matching the CI artifact glob). This covers every service in any
// env, present or future, with zero per-env code -- summary.json's schema has no key for services
// like beacon/validator/op-node, so a hardcoded list (or a summary.json-derived one) would always
// under-cover. Failures here are logged, not fatal, since this only runs to aid debugging an
// already-failed run.
func dumpContainerLogs(env *envs.Env) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	services, err := env.ComposeServices(ctx)
	if err != nil {
		log.Infof("[TEARDOWN] failed to list compose services: %v", err)
		return
	}

	for _, service := range services {
		out, err := env.DockerComposeLogs(ctx, "--no-log-prefix", service)
		if err != nil {
			log.Infof("[TEARDOWN] failed to fetch logs for service %q: %v", service, err)
			continue
		}
		logPath := service + ".log"
		if err := os.WriteFile(logPath, out, 0o644); err != nil {
			log.Infof("[TEARDOWN] failed to write %s: %v", logPath, err)
			continue
		}
		log.Infof("[TEARDOWN] wrote container logs: %s", logPath)
	}
}

func TestMain(m *testing.M) {
	short := false
	for _, arg := range os.Args {
		if strings.Contains(arg, "short") {
			short = true
			break
		}
	}
	if short {
		os.Exit(m.Run())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)

	// Select which env to load via AGGKIT_E2E_ENV; default to the two-chain Anvil env.
	envName := envs.ENVName(os.Getenv("AGGKIT_E2E_ENV"))
	if envName == "" {
		envName = envs.EnvAnvil2Chains
	}

	env, err := envs.LoadEnv(ctx, envName)
	if err != nil {
		cancel()
		log.Fatalf("failed to load env: %v", err)
	}
	cancel()
	testEnv = env

	checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	if err := env.CheckEnv(checkCtx); err != nil {
		checkCancel()
		log.Fatalf("pre-test environment check failed: %v", err)
	}
	checkCancel()

	code := m.Run()

	// Post-test bridge health-check.
	//
	// Some e2e tests legitimately manipulate the GER (e.g. removeger_test.go's remove-GER scenarios,
	// forcegerupdate_test.go's TestForceGERUpdateE2E) in ways that can leave the L2->L1 settlement
	// flow unable to complete, which would make this global check fail even though the test's own
	// assertions passed. Those tests are skipped by default in the shared `make test-e2e` suite for
	// exactly that reason, but the dedicated isolated CI job that opts into running
	// TestForceGERUpdateE2E (RUN_FORCE_GER_UPDATE_E2E=true) also sets
	// E2E_SKIP_POSTTEST_BRIDGE_CHECK=true to disable this check for that run, since it owns its own
	// env and doesn't need (or want) this cross-test health signal. Unset (the default), behavior is
	// unchanged.
	if os.Getenv("E2E_SKIP_POSTTEST_BRIDGE_CHECK") == "true" {
		log.Info("[POSTTEST] E2E_SKIP_POSTTEST_BRIDGE_CHECK=true: skipping post-test bridge health-check.")
	} else if code == 0 {
		postTestBridgeCheck(env)
	}

	if code != 0 {
		log.Infof("[TEARDOWN] test run failed (code=%d): dumping container logs for diagnostics...", code)
		dumpContainerLogs(env)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := env.Stop(stopCtx); err != nil {
		log.Infof("failed to stop env: %v", err)
	}
	stopCancel()

	os.Exit(code)
}

// -----------------------------------------------------------------------------------------------
// Post-test bridge health-check
// -----------------------------------------------------------------------------------------------

const (
	// postTestBridgeCheckTimeout bounds the whole post-test check, unchanged from the hand-rolled
	// check that used to live inline in TestMain.
	postTestBridgeCheckTimeout = 8 * time.Minute

	// postTestLoopName names the single loop the check configures. It shows up in every log line
	// bridge_loop_tester emits for the check, so it is deliberately self-describing.
	postTestLoopName = "posttest-health-ring"

	// postTestRingAmount is how much native ETH the ring moves on every hop: 0.1 ETH. It has to be
	// far above what a claim costs in gas on the destination network, because a hop credits the
	// same account that pays for its own claim: a credit smaller than that fee shows up as a
	// destination balance that went *down*, which the hop engine (correctly) reports as an
	// unreconciled balance. A claim on these networks costs on the order of 2e14 wei, so 1e17
	// leaves three orders of magnitude of headroom while still being small enough that the ring
	// never strains the L1 bridge's liquidity (which only ever releases what the same cycle
	// deposited into it).
	postTestRingAmount = 100_000_000_000_000_000 // 1e17

	// postTestHopTimeout is the total budget of one hop, chosen so that all hops of the largest
	// ring (three, on a two-L2 env) fit inside postTestBridgeCheckTimeout with headroom. A hop
	// that exhausts it fails with the tool's own diagnosis (which readiness gate was not
	// satisfied) rather than with a bare context deadline, which is the whole point of bounding
	// the hop rather than only the check.
	postTestHopTimeout = 2 * time.Minute

	// postTestPollInterval is how often the tool re-polls a readiness gate. The envs this check
	// runs on settle in seconds, so polling fast keeps the check short.
	postTestPollInterval = 2 * time.Second

	// postTestGracePeriod is the window a manual hop asserts nothing else claims its deposit in.
	// It is deliberately short: this is a network-health probe run after every suite, not an
	// autoclaim-policy test, and the value it adds (catching a claimer that should not be running
	// on this destination) does not grow with the length of the wait, while the cost does -- it is
	// paid once per hop, on every suite run.
	postTestGracePeriod = 5 * time.Second

	// postTestLoopDelay is the pause between cycles. The check runs exactly one cycle, so this is
	// only here because the config requires a positive value.
	postTestLoopDelay = 1 * time.Second

	// postTestGasLimitOffset is added to every eth_estimateGas result the tool gets. It is not
	// optional: bridgeAsset is called with forceUpdateGlobalExitRoot = true, so it also writes the
	// global exit root, and a bridge that lands in the same block as another one pays materially
	// more than its own estimate predicted (the first already warmed the storage the estimate was
	// priced against) and reverts OutOfGas inside updateExitRoot. bridge_utils.go works around the
	// same thing by pinning l1BridgeGasLimit to 500_000.
	postTestGasLimitOffset = 300_000

	// postTestKeystorePassword encrypts the keystores this check writes for the tool's go_signer
	// "local" signer configs. The keys are the env's own public test keys, so the password is only
	// there because the keystore format requires one.
	postTestKeystorePassword = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"

	// postTestL1NetworkID is L1's network ID in the bridge's own numbering: mainnet is always 0.
	postTestL1NetworkID uint32 = 0
)

// postTestBridgeCheck is the suite's post-test network-health probe: it moves value once around a
// closed bridge ring derived from the loaded env and fails the whole run if the value does not come
// home. It runs only when the suite itself passed (a failing suite has already reported a problem,
// and its failure may well be why the network is unhappy).
//
// On envs that run an aggkit-proxy it drives tools/bridge_loop_tester, so the tool the repo ships
// is the probe the repo uses. On envs without one (EnvOpPP -- the tool observes everything through
// the proxy's /bridge/v1 + /tracker/v1 surface, and the /tracker/v1 half exists only in the
// aggkit-proxy binary) it falls back to the hand-rolled L1<->L2 check this replaced, so no env
// loses its health signal.
//
// A failure calls log.Fatalf, which exits before TestMain's teardown runs: the env is deliberately
// left standing so the failure can be debugged against the live network.
func postTestBridgeCheck(env *envs.Env) {
	if err := runPostTestBridgeCheck(env); err != nil {
		log.Fatalf(`[POSTTEST] Bridge flows post-test check failed: %v.
		Note that test env will not be cleaned for further debugging`, err)
	}
	log.Info("[POSTTEST] Bridge flows post-test check succeeded.")
}

// runPostTestBridgeCheck picks the check appropriate to the loaded env and runs it under the
// check's timeout, returning the first failure. It is separate from postTestBridgeCheck only so
// that the log.Fatalf lives in a function with no deferred cleanup to skip.
func runPostTestBridgeCheck(env *envs.Env) error {
	ctx, cancel := context.WithTimeout(context.Background(), postTestBridgeCheckTimeout)
	defer cancel()

	if env.ProxyRESTURL == "" {
		log.Info("[POSTTEST] this env runs no aggkit-proxy, so bridge_loop_tester has nothing to " +
			"observe through: running the hand-rolled L1->L2 and L2->L1 bridge flows instead...")

		return postTestBridgeCheckLegacy(ctx, env)
	}

	log.Info("Running one bridge_loop_tester cycle around a closed ring to check network health post-test...")

	return postTestBridgeCheckViaTool(ctx, env)
}

// postTestBridgeCheckViaTool drives one full cycle of the post-test ring through
// tools/bridge_loop_tester's library API and turns the Report it returns into a verdict.
func postTestBridgeCheckViaTool(ctx context.Context, env *envs.Env) error {
	keystoreDir, err := os.MkdirTemp("", "posttest-bridge-loop")
	if err != nil {
		return fmt.Errorf("create a keystore directory for the post-test ring: %w", err)
	}
	defer func() { _ = os.RemoveAll(keystoreDir) }()

	cfg, err := postTestBridgeLoopConfig(env, keystoreDir)
	if err != nil {
		return err
	}

	orchestrator, err := bridgelooptester.NewOrchestrator(ctx, cfg, bridgelooptester.OrchestratorDeps{
		Logger: log.WithFields("posttest", "bridge-ring"),
		// One attempt per hop. A retry would hide a genuinely broken network behind a second
		// hop budget and double the worst-case duration of a check that runs after every suite;
		// this probe wants the first failure, reported immediately.
		HopAttempts: 1,
	})
	if err != nil {
		return fmt.Errorf("build the bridge_loop_tester orchestrator: %w", err)
	}
	defer orchestrator.Close()

	report, runErr := orchestrator.Run(ctx)
	postTestLogBridgeLoopReport(report)
	if runErr != nil {
		return fmt.Errorf("bridge_loop_tester run: %w", runErr)
	}

	return postTestBridgeLoopVerdict(report, len(cfg.Loops[0].Hops))
}

// postTestBridgeLoopConfig builds the tool's Config for the post-test ring entirely from the loaded
// env: one Network per network the env has (IDs, chain IDs, RPC URLs and bridge addresses all read
// off the env, never hardcoded) and one ETH loop walking the closed ring those networks form.
//
// The ring is derived from the env's topology, so the check covers every bridge direction the env
// can express and still works on a single-L2 env:
//
//	env.L2B == nil:  0 -> 1 -> 0       (L1->L2, L2->L1)          -- the two directions the
//	                                                                hand-rolled check covered
//	env.L2B != nil:  0 -> 1 -> 2 -> 0  (L1->L2, L2->L2, L2->L1)
//
// Because the ring is closed, the ETH it moves returns to the L1 account it started from and the
// check leaves the env's balances where it found them, bar gas.
//
// It is deliberately ETH-only, with no ERC20 loop: an ERC20 loop needs a token deploy and a mint on
// every run, and this check runs after every e2e suite. tools/bridge_loop_tester's own e2e test
// (TestBridgeLoopFullCycle) is where the ERC20 ring, the "auto" claim mode and the full assertion
// surface are exercised.
//
// Every hop is Claim = "manual", i.e. the tool submits every claim itself. That is not an
// arbitrary default: whether an autoclaim service is running on any given aggkit node here depends
// on which tests just ran (autoclaim_test.go enables autoclaim and restores the node's config on
// cleanup), so a hop configured Claim = "auto" would fail whenever the suite happened to leave
// autoclaim off -- a property of the preceding test, not of the network's health. Manual claims
// make the check self-sufficient: it needs no service beyond the bridge itself and the proxy.
func postTestBridgeLoopConfig(env *envs.Env, keystoreDir string) (*bridgelooptester.Config, error) {
	l2s := []*envs.L2Config{&env.L2}
	if env.L2B != nil {
		l2s = append(l2s, env.L2B)
	}

	networks := make([]bridgelooptester.Network, 0, len(l2s)+1)
	hops := make([]bridgelooptester.Hop, 0, len(l2s)+1)

	_, l1Key, err := env.Keys.L1Keys.Checkout()
	if err != nil {
		return nil, fmt.Errorf("check out an L1 key for the post-test ring: %w", err)
	}
	l1Signer, err := postTestSignerConfig(keystoreDir, "l1", l1Key)
	if err != nil {
		return nil, err
	}
	networks = append(networks, bridgelooptester.Network{
		NetworkID:      postTestL1NetworkID,
		Name:           "l1",
		RPCURL:         env.L1.RPCURL,
		BridgeAddr:     env.L1.Contracts.BridgeAddress,
		ChainID:        env.L1.ChainID.Uint64(),
		GasLimitOffset: postTestGasLimitOffset,
		Signer:         l1Signer,
	})

	source := postTestL1NetworkID
	for _, l2 := range l2s {
		name := fmt.Sprintf("l2-%d", l2.NetworkID)
		_, l2Key, err := l2.Keys.Checkout()
		if err != nil {
			return nil, fmt.Errorf("check out a %s key for the post-test ring: %w", name, err)
		}
		l2Signer, err := postTestSignerConfig(keystoreDir, name, l2Key)
		if err != nil {
			return nil, err
		}
		networks = append(networks, bridgelooptester.Network{
			NetworkID:      l2.NetworkID,
			Name:           name,
			RPCURL:         l2.RPCURL,
			BridgeAddr:     l2.Contracts.L2BridgeAddress,
			ChainID:        l2.ChainID.Uint64(),
			GasLimitOffset: postTestGasLimitOffset,
			Signer:         l2Signer,
		})
		hops = append(hops, bridgelooptester.Hop{
			Source: source, Destination: l2.NetworkID, Claim: bridgelooptester.ClaimManual,
		})
		source = l2.NetworkID
	}
	// Close the ring back onto L1.
	hops = append(hops, bridgelooptester.Hop{
		Source: source, Destination: postTestL1NetworkID, Claim: bridgelooptester.ClaimManual,
	})

	return &bridgelooptester.Config{
		Global: bridgelooptester.Global{
			ProxyURL:          env.ProxyRESTURL,
			LogLevel:          "info",
			Iterations:        1,
			LoopDelay:         cfgtypes.Duration{Duration: postTestLoopDelay},
			HopTimeout:        cfgtypes.Duration{Duration: postTestHopTimeout},
			PollInterval:      cfgtypes.Duration{Duration: postTestPollInterval},
			ManualGracePeriod: cfgtypes.Duration{Duration: postTestGracePeriod},
			HopAttempts:       1,
			// No StatePath and no MetricsAddr: a one-shot probe has nothing to resume from and
			// binding a port after every suite would be a needless risk.
		},
		Networks: networks,
		Loops: []bridgelooptester.Loop{
			{
				Name:    postTestLoopName,
				Asset:   bridgelooptester.AssetETH,
				Amount:  bridgelooptester.NewWeiAmount(postTestRingAmount),
				Enabled: true,
				Hops:    hops,
			},
		},
	}, nil
}

// postTestSignerConfig writes key into a keystore file under dir and returns the go_signer "local"
// SignerConfig pointing at it, which is what the tool's own NewNetworkClient consumes -- so the
// check drives the tool exactly as an operator running the CLI against a TOML config would.
func postTestSignerConfig(dir, label string, key *ecdsa.PrivateKey) (signertypes.SignerConfig, error) {
	encrypted, err := keystore.EncryptKey(&keystore.Key{
		Id:         uuid.New(),
		Address:    crypto.PubkeyToAddress(key.PublicKey),
		PrivateKey: key,
	}, postTestKeystorePassword, keystore.LightScryptN, keystore.LightScryptP)
	if err != nil {
		return signertypes.SignerConfig{}, fmt.Errorf("encrypt the %s signing key: %w", label, err)
	}

	path := filepath.Join(dir, label+".keystore")
	if err := os.WriteFile(path, encrypted, 0o600); err != nil {
		return signertypes.SignerConfig{}, fmt.Errorf("write the %s keystore: %w", label, err)
	}

	return gosigner.NewLocalSignerConfig(path, postTestKeystorePassword), nil
}

// postTestLogBridgeLoopReport prints the run's one-line summary and one line per hop, so a passing
// check leaves a readable trace and a failing one leaves the hop detail next to the verdict.
func postTestLogBridgeLoopReport(report *bridgelooptester.Report) {
	if report == nil {
		return
	}

	log.Infof("[POSTTEST] bridge_loop_tester: %s", report.Summary())
	for _, hop := range report.AllHops() {
		log.Infof("[POSTTEST] hop %d %s asset=%s claim=%s outcome=%s state=%s claimed_by=%s "+
			"deposit_count=%d duration=%s",
			hop.HopIndex, hop.Route(), hop.Asset, hop.ClaimMode, hop.Outcome, hop.FinalState,
			hop.ClaimedBy, hop.DepositCount, hop.Duration)
	}
}

// postTestBridgeLoopVerdict turns the tool's Report into the check's verdict. It reads the report
// rather than trusting Run's error alone, because a hop that exhausts its attempts on a transient
// failure ends its cycle without halting the loop -- Run reports no error for that, but a ring that
// did not close is exactly what this check exists to catch. expectedHops is the ring's length, so a
// cycle that quietly ran fewer hops than the ring has is a failure too.
func postTestBridgeLoopVerdict(report *bridgelooptester.Report, expectedHops int) error {
	if report == nil {
		return errors.New("the bridge_loop_tester run returned no report")
	}

	var problems []string
	note := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if report.Cancelled {
		note("the run was cancelled before the ring closed")
	}
	if !report.Succeeded() {
		note("the run did not succeed (%s)", report.Summary())
	}
	if report.Totals.ClaimModeViolations > 0 {
		note("%d claim-mode violation(s): a hop the tool was to claim itself was claimed by "+
			"something else (an autoclaim service left running by a preceding test would do this)",
			report.Totals.ClaimModeViolations)
	}
	if report.Totals.StrandedLoops > 0 {
		note("%d loop(s) left value stranded mid-ring", report.Totals.StrandedLoops)
	}
	if report.Totals.HopsSucceeded != expectedHops {
		note("%d of the ring's %d hops succeeded", report.Totals.HopsSucceeded, expectedHops)
	}

	for i := range report.Loops {
		loop := &report.Loops[i]
		if loop.Halted {
			note("loop %q halted (%s): %s", loop.Name, loop.HaltClass, loop.Err)
		}
		if loop.CyclesCompleted != loop.CyclesAttempted {
			note("loop %q completed %d of %d attempted cycles", loop.Name,
				loop.CyclesCompleted, loop.CyclesAttempted)
		}
		if loop.ValueLocation.Stranded {
			note("loop %q left its value stranded on network %d (%s): %s", loop.Name,
				loop.ValueLocation.NetworkID, loop.ValueLocation.NetworkName, loop.ValueLocation.Detail)
		}
		for j := range loop.Cycles {
			if cycle := &loop.Cycles[j]; !cycle.RingClosed {
				note("loop %q cycle %d did not close the ring (%s): %s", loop.Name, cycle.Iteration,
					cycle.FailureClass, cycle.Err)
			}
		}
	}

	for _, hop := range report.AllHops() {
		if hop.Succeeded() {
			continue
		}
		detail := hop.ErrMessage
		if hop.StalledGate != "" {
			detail = fmt.Sprintf("stalled on the %q readiness gate: %s", hop.StalledGate, detail)
		}
		note("hop %d %s failed in state %q: %s", hop.HopIndex, hop.Route(), hop.FinalState, detail)
	}

	if len(problems) == 0 {
		return nil
	}

	return errors.New(strings.Join(problems, "; "))
}

// postTestBridgeCheckLegacy is the hand-rolled health check bridge_loop_tester replaced, kept for
// envs that run no aggkit-proxy (see postTestBridgeCheck). It mints and approves an L2-native
// ERC20 (L2-native tokens bypass the Local Balance Tree underflow check in the L2 bridge contract)
// and then runs an L1->L2 and an L2->L1 flow in parallel.
//
// The parallelism is load-bearing, not just a speed-up: the L1->L2 flow's claim lands on L2 after
// the L2->L1 flow's bridge, which is what advances that L2's claim syncer past it so its local exit
// root can settle to L1.
func postTestBridgeCheckLegacy(ctx context.Context, env *envs.Env) error {
	l2Opts := env.L2.Transactor

	mintAmount := big.NewInt(1e18)
	mintTx, err := env.L2.Contracts.MintableERC20.Mint(l2Opts, l2Opts.From, mintAmount)
	if err != nil {
		return fmt.Errorf("failed to mint ERC20 tokens: %w", err)
	}
	if _, err := bind.WaitMined(ctx, env.Clients.L2, mintTx); err != nil {
		return fmt.Errorf("failed to wait for ERC20 mint tx: %w", err)
	}

	approveTx, err := env.L2.Contracts.MintableERC20.Approve(l2Opts, env.L2.Contracts.L2BridgeAddress, mintAmount)
	if err != nil {
		return fmt.Errorf("failed to approve ERC20 tokens for L2 bridge: %w", err)
	}
	if _, err := bind.WaitMined(ctx, env.Clients.L2, approveTx); err != nil {
		return fmt.Errorf("failed to wait for ERC20 approve tx: %w", err)
	}

	// Each goroutine gets its own copy of the transactors so that mutations to fields like Value
	// (done by BridgeL1ToL2 for the ETH bridge tx) don't race with the other goroutine's
	// transactions.
	l1l2ErrCh := make(chan error, 1)
	l2l1ErrCh := make(chan error, 1)

	l1OptsL1L2, l2OptsL1L2 := *env.L1.Transactor, *env.L2.Transactor
	l1OptsL2L1, l2OptsL2L1 := *env.L1.Transactor, *env.L2.Transactor

	go func() {
		l1l2ErrCh <- BridgeL1ToL2(ctx, env, &l1OptsL1L2, &l2OptsL1L2)
	}()
	go func() {
		l2l1ErrCh <- BridgeL2ToL1(ctx, env, &l1OptsL2L1, &l2OptsL2L1, env.L2.Contracts.MintableERC20Address)
	}()

	bridgeL1L2Err := <-l1l2ErrCh
	bridgeL2L1Err := <-l2l1ErrCh

	if bridgeL1L2Err != nil || bridgeL2L1Err != nil {
		return fmt.Errorf("L1->L2: %w, L2->L1: %w", bridgeL1L2Err, bridgeL2L1Err)
	}

	return nil
}
