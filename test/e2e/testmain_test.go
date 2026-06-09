package e2e

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

	env, err := envs.LoadEnv(ctx, envs.EnvOpPP)
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
		if !l1HeadAdvances(env) {
			log.Infof("[POSTTEST] Skipping bridge flows post-test check: L1 head is not advancing")
			code = 1
		} else {
			log.Info("Running a L1 -> L2 and L2 -> L1 bridge flow to check network health post-test...")
			runPostTestBridgeCheck(env)
		}
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := env.Stop(stopCtx); err != nil {
		log.Infof("failed to stop env: %v", err)
	}
	stopCancel()

	os.Exit(code)
}

func l1HeadAdvances(env *envs.Env) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start, err := env.Clients.L1.HeaderByNumber(ctx, nil)
	if err != nil {
		log.Infof("[POSTTEST] Failed to read L1 head before bridge check: %v", err)
		return false
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			current, err := env.Clients.L1.HeaderByNumber(ctx, nil)
			if err != nil {
				log.Infof("[POSTTEST] Failed to read L1 head while checking progress: %v", err)
				return false
			}
			if current.Number.Cmp(start.Number) > 0 {
				return true
			}
		}
	}
}

func runPostTestBridgeCheck(env *envs.Env) {
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

	l1l2UsesAutoClaim := autoClaimEnabled(env)
	go func() {
		if l1l2UsesAutoClaim {
			l1l2ErrCh <- BridgeL1ToL2WithAutoClaim(bridgeCheckCtx, env, &l1OptsL1L2, &l2OptsL1L2)
			return
		}
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

func BridgeL1ToL2WithAutoClaim(ctx context.Context, env *envs.Env, l1Opts, l2Opts *bind.TransactOpts) error {
	log.Info("Starting L1->L2 bridge flow using Auto Claim (helper)")
	bridgeAmount := big.NewInt(1e14)
	result, err := BridgeL1NoClaim(ctx, env, l1Opts, l2Opts, bridgeAmount, "posttest-autoclaim")
	if err != nil {
		return err
	}
	requestKey := autoclaimtypes.DeriveRequestKey(
		result.Bridge.OriginNetwork,
		result.Bridge.DestinationNetwork,
		result.DepositCount,
	)
	confirmed, err := waitForAutoClaimStatusResult(ctx, requestKey, autoclaimtypes.RequestStatusConfirmed)
	if err != nil {
		return err
	}
	if confirmed.ClaimTxHash == nil {
		return fmt.Errorf("Auto Claim request %s confirmed without claim tx hash", requestKey)
	}
	if err := requireClaimedOnL2(ctx, env, result.GlobalIndex); err != nil {
		return err
	}
	log.Infof("L1->L2 flow completed successfully through Auto Claim (helper)")
	return nil
}

func requireClaimedOnL2(ctx context.Context, env *envs.Env, globalIndex *big.Int) error {
	depositCount, originNetwork, err := globalIndexToDepositCountAndOrigin(globalIndex)
	if err != nil {
		return fmt.Errorf("decode global index %s: %w", globalIndex.String(), err)
	}
	claimed, err := env.L2.Contracts.L2Bridge.IsClaimed(&bind.CallOpts{Context: ctx}, depositCount, originNetwork)
	if err != nil {
		return fmt.Errorf("check L2 claim state for global index %s: %w", globalIndex.String(), err)
	}
	if !claimed {
		return fmt.Errorf("claim is not marked claimed on L2 for global index %s", globalIndex.String())
	}
	return nil
}

func autoClaimEnabled(env *envs.Env) bool {
	config, err := os.ReadFile(env.GetAggkitConfigPath())
	if err != nil {
		log.Infof("[POSTTEST] Auto Claim config check failed, using manual L1->L2 claim path: %v", err)
		return false
	}
	enabled, found := parseAutoClaimEnabled(string(config))
	if !found {
		return false
	}
	if enabled {
		log.Infof("[POSTTEST] Auto Claim is enabled; L1->L2 bridge health check will wait for Auto Claim confirmation")
	}
	return enabled
}

func parseAutoClaimEnabled(config string) (bool, bool) {
	inAutoClaimSection := false
	for _, rawLine := range strings.Split(config, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inAutoClaimSection = line == "[AutoClaim]"
			continue
		}
		if !inAutoClaimSection || !strings.HasPrefix(line, "Enabled") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return false, false
		}
		return strings.EqualFold(strings.TrimSpace(parts[1]), "true"), true
	}
	return false, false
}
