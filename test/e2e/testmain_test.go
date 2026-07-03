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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)

	// Select which env to load via AGGKIT_E2E_ENV (used by CI to run the 2-chain matrix);
	// defaults to the single-chain op-pp env.
	envName := envs.ENVName(os.Getenv("AGGKIT_E2E_ENV"))
	if envName == "" {
		envName = envs.EnvOpPP
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

	// Post-test bridge health-check
	if code == 0 {
		log.Info("Running a L1 -> L2 and L2 -> L1 bridge flow to check network health post-test...")
		bridgeCheckCtx, bridgeCancel := context.WithTimeout(context.Background(), 8*time.Minute)

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

		// Run L1->L2 and L2->L1 bridges in parallel.
		// Each goroutine gets its own copy of the transactors so that mutations to
		// fields like Value (done by BridgeL1ToL2 for the ETH bridge tx) don't race
		// with the other goroutine's transactions.
		l1l2ErrCh := make(chan error, 1)
		l2l1ErrCh := make(chan error, 1)

		l1OptsL1L2, l2OptsL1L2 := *env.L1.Transactor, *env.L2.Transactor
		l1OptsL2L1, l2OptsL2L1 := *env.L1.Transactor, *env.L2.Transactor

		go func() {
			l1l2ErrCh <- BridgeL1ToL2(bridgeCheckCtx, env, &l1OptsL1L2, &l2OptsL1L2)
		}()
		go func() {
			l2l1ErrCh <- BridgeL2ToL1(bridgeCheckCtx, env, &l1OptsL2L1, &l2OptsL2L1, env.L2.Contracts.MintableERC20Address)
		}()

		bridgeL1L2Err := <-l1l2ErrCh
		bridgeL2L1Err := <-l2l1ErrCh

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
