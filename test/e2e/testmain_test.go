package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/common"
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)

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
		log.Info("Running a L1 -> L2 and L2 -> L1 bridge flow to check network health post-test...")
		bridgeCheckCtx, bridgeCancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer bridgeCancel()
		l1Opts := env.L1.Transactor
		l2Opts := env.L2.Transactor
		bridgeL1L2Err := BridgeL1ToL2(bridgeCheckCtx, env, l1Opts, l2Opts)
		if bridgeL1L2Err != nil {
			log.Fatalf(`[POSTTEST] Bridge flows post-test check failed: L1->L2: %v.
			Note that test env will not be cleaned for further debugging`, bridgeL1L2Err)
		}
		bridgeL2L1Err := BridgeL2ToL1(bridgeCheckCtx, env, l1Opts, l2Opts, common.Address{})
		if bridgeL2L1Err != nil {
			log.Fatalf(`[POSTTEST] Bridge flows post-test check failed: L2->L1: %v.
			Note that test env will not be cleaned for further debugging`, bridgeL2L1Err)
		}
		log.Infof("[POSTTEST] Bridge flows post-test check succeeded.")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()
	if err := env.Stop(stopCtx); err != nil {
		log.Infof("failed to stop env: %v", err)
	}

	os.Exit(code)
}
