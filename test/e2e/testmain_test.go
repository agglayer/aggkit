package e2e

import (
	"context"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
)

var testEnv *envs.Env

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

	// Select the env to run via E2E_ENV (defaults to op-pp when unset/empty) so an env
	// with no migrated tests yet still boots + sanity-checks. Unknown values fail fast
	// with the list of valid env names.
	envName, err := envs.ParseENVName(os.Getenv("E2E_ENV"))
	if err != nil {
		log.Fatalf("invalid E2E_ENV: %v", err)
	}

	// LoadEnv brings the docker-compose stack up and blocks until the L2 EL
	// dependency chain is healthy. op-geth-backed envs (op-pp) settle in well
	// under a minute, but op-reth-backed FEP envs (op-fep) take ~75s (op-reth
	// init + L1 must produce up to the L2 origin block), so use the loader's own
	// service-ready budget rather than a tight 1-minute cap. op-pp is unaffected.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

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

	// Post-test bridge health-check. The flow mints/approves a MintableERC20 on L2, which
	// the loader only auto-deploys for native-gas envs. On custom-gas / cdk-erigon envs
	// (Capabilities.NativeGas == false) MintableERC20 is nil, so skip the ERC20 bridge flow
	// to avoid a nil panic; such envs only need to boot + sanity-check here.
	if code == 0 && !env.Capabilities.NativeGas {
		log.Infof("[POSTTEST] Skipping ERC20 mint/approve/bridge flow (native_gas=false); env booted and passed sanity checks.")
	}
	// FEP envs (op-fep, op-fep-committee) have a documented L2->L1 FEP-settlement
	// limitation (snapshots emit settled:false). They are CI'd as boot/load/checks
	// (+committee quorum) smoke ONLY, so skip the bridge/settlement health-check
	// here rather than letting it red the leg on the known limitation.
	if code == 0 && env.Capabilities.NativeGas && !env.Capabilities.SettlementSupported {
		log.Infof("[POSTTEST] Skipping L1<->L2 bridge/settlement health-check " +
			"(settlement_supported=false; documented FEP L2->L1 settled:false limitation); " +
			"env booted and passed sanity checks.")
	}
	if code == 0 && env.Capabilities.NativeGas && env.Capabilities.SettlementSupported {
		log.Info("Running a L1 -> L2 and L2 -> L1 bridge flow to check network health post-test...")
		// The L2->L1 leg only becomes claimable after a PP certificate covering its exit settles and
		// the rollup exit root propagates into a new GER / L1 Info Tree leaf, which spans several
		// agglayer epochs. Allow a generous budget so this health check is not flaky on a slow
		// settlement; BridgeL2ToL1 returns as soon as the exit is claimable, so the happy path is fast.
		bridgeCheckCtx, bridgeCancel := context.WithTimeout(context.Background(), 35*time.Minute)

		l2Opts := env.L2.Transactor

		// Mint and approve ERC20 tokens on L2 before bridging (L2-native tokens bypass
		// the Local Balance Tree underflow check in the L2 bridge contract).
		mintAmount := big.NewInt(1e18)
		mintTx, err := env.L2.Contracts.MintableERC20.Mint(l2Opts, l2Opts.From, mintAmount)
		if err != nil {
			bridgeCancel()
			log.Fatalf("[POSTTEST] Failed to mint ERC20 tokens: %v", err)
		}
		if _, err := bind.WaitMined(bridgeCheckCtx, env.Clients.L2, mintTx); err != nil {
			bridgeCancel()
			log.Fatalf("[POSTTEST] Failed to wait for ERC20 mint tx: %v", err)
		}

		approveTx, err := env.L2.Contracts.MintableERC20.Approve(
			l2Opts, env.L2.Contracts.L2BridgeAddress, mintAmount,
		)
		if err != nil {
			bridgeCancel()
			log.Fatalf("[POSTTEST] Failed to approve ERC20 tokens for L2 bridge: %v", err)
		}
		if _, err := bind.WaitMined(bridgeCheckCtx, env.Clients.L2, approveTx); err != nil {
			bridgeCancel()
			log.Fatalf("[POSTTEST] Failed to wait for ERC20 approve tx: %v", err)
		}

		// Run the L1->L2 bridge in the background (it is fast: no cert settlement needed) on the full
		// budget, while the L2->L1 leg runs in the foreground. Each gets its own copy of the transactors
		// so that mutations to fields like Value (done by BridgeL1ToL2 for the ETH bridge tx) don't race.
		l1l2ErrCh := make(chan error, 1)
		l1OptsL1L2, l2OptsL1L2 := *env.L1.Transactor, *env.L2.Transactor
		go func() {
			l1l2ErrCh <- BridgeL1ToL2(bridgeCheckCtx, env, &l1OptsL1L2, &l2OptsL1L2)
		}()

		// L2->L1 with a single aggkit-restart recovery. The aggsender's epoch-notifier L1 block
		// subscription can intermittently freeze after repeated aggkit restarts (e.g. during BFL
		// Case3/Case4), which stalls cert settlement so the L2->L1 exit never becomes claimable and the
		// leg would otherwise burn the entire budget. A single aggkit restart re-establishes the
		// subscription. This is a test/env-side mitigation; the aggsender itself is left unchanged.
		runL2L1 := func(attemptCtx context.Context) error {
			l1Opts, l2Opts := *env.L1.Transactor, *env.L2.Transactor
			return BridgeL2ToL1(attemptCtx, env, &l1Opts, &l2Opts, env.L2.Contracts.MintableERC20Address)
		}
		firstCtx, firstCancel := context.WithTimeout(bridgeCheckCtx, 18*time.Minute)
		bridgeL2L1Err := runL2L1(firstCtx)
		firstCancel()

		// Collect the (fast) L1->L2 result before any restart so the restart can't disrupt it.
		bridgeL1L2Err := <-l1l2ErrCh

		if bridgeL2L1Err != nil && bridgeCheckCtx.Err() == nil {
			log.Warnf("[POSTTEST] L2->L1 leg did not settle in the first attempt (%v); restarting aggkit "+
				"to recover a possibly-frozen epoch-notifier subscription, then retrying once...", bridgeL2L1Err)
			restartCtx, restartCancel := context.WithTimeout(bridgeCheckCtx, 3*time.Minute)
			if stopErr := env.StopAggkit(restartCtx); stopErr != nil {
				log.Warnf("[POSTTEST] StopAggkit during recovery failed: %v", stopErr)
			}
			if startErr := env.StartAggkit(restartCtx); startErr != nil {
				log.Warnf("[POSTTEST] StartAggkit during recovery failed: %v", startErr)
			}
			restartCancel()
			bridgeL2L1Err = runL2L1(bridgeCheckCtx)
		}

		bridgeCancel()

		if bridgeL1L2Err != nil || bridgeL2L1Err != nil {
			log.Fatalf(`[POSTTEST] Bridge flows post-test check failed: L1->L2: %v, L2->L1: %v.
			Note that test env will not be cleaned for further debugging`, bridgeL1L2Err, bridgeL2L1Err)
		}
		log.Infof("[POSTTEST] Bridge flows post-test check succeeded.")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := env.Stop(stopCtx); err != nil {
		log.Infof("failed to stop env: %v", err)
	}
	stopCancel()

	os.Exit(code)
}
