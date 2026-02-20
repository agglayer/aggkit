package e2e

import (
	"context"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agglayer/aggkit/test/e2e/envs"
)

var testEnv *envs.Env

func TestMain(m *testing.M) {
	// Skip loading env when -short is set (testing.Short() cannot be used before flag parse).
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
	defer cancel()

	env, err := envs.LoadEnv(ctx, envs.EnvOpPP)
	if err != nil {
		log.Fatalf("failed to load env: %v", err)
	}
	testEnv = env

	code := m.Run()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()
	if err := env.Stop(stopCtx); err != nil {
		log.Printf("failed to stop env: %v", err)
	}

	os.Exit(code)
}
