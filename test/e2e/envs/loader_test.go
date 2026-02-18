package envs

import (
	"context"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/stretchr/testify/require"
)

func TestLoadEnv(t *testing.T) {
	// Skip in short mode as this test starts docker-compose
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Load environment (starts docker-compose)
	env, err := LoadEnv(ctx, EnvOpPP)
	require.NoError(t, err, "LoadEnv should not return an error")
	require.NotNil(t, env, "Env should not be nil")

	// Ensure cleanup
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		require.NoError(t, env.Stop(stopCtx), "Stop should not return an error")
	}()

	t.Run("VerifyConfiguration", func(t *testing.T) {
		// Verify L1 configuration
		require.NotNil(t, env.L1.ChainID, "L1 ChainID should not be nil")
		require.Equal(t, "271828", env.L1.ChainID.String(), "L1 ChainID should match")
		require.NotNil(t, env.L1.Contracts.RollupManager, "RollupManager contract should be initialized")
		require.NotNil(t, env.L1.Contracts.Bridge, "Bridge contract should be initialized")
		require.NotNil(t, env.L1.Transactor, "L1 Transactor should be initialized")
		require.NotNil(t, env.L1.Transactor.From, "L1 Transactor From address should be set")

		// Verify L2 configuration
		require.NotNil(t, env.L2.ChainID, "L2 ChainID should not be nil")
		require.Equal(t, "2151908", env.L2.ChainID.String(), "L2 ChainID should match")
		require.NotNil(t, env.L2.Contracts.L2Bridge, "L2Bridge contract should be initialized")
		require.NotNil(t, env.L2.Contracts.GlobalExitRoot, "GlobalExitRoot contract should be initialized")
		require.NotNil(t, env.L2.Transactor, "L2 Transactor should be initialized")
		require.NotNil(t, env.L2.Transactor.From, "L2 Transactor From address should be set")

		// Verify clients
		require.NotNil(t, env.Clients.L1, "L1 client should be initialized")
		require.NotNil(t, env.Clients.L2, "L2 client should be initialized")
		require.NotNil(t, env.Clients.BridgeService, "BridgeService client should be initialized")
	})

	t.Run("VerifyL1Connectivity", func(t *testing.T) {
		// Verify L1 client can fetch chain ID
		l1ChainID, err := env.Clients.L1.ChainID(ctx)
		require.NoError(t, err, "L1 client should be able to fetch chain ID")
		require.Equal(t, env.L1.ChainID, l1ChainID, "L1 client chain ID should match")

		// Verify L1 client can fetch block number
		l1BlockNumber, err := env.Clients.L1.BlockNumber(ctx)
		require.NoError(t, err, "L1 client should be able to fetch block number")
		require.Greater(t, l1BlockNumber, uint64(0), "L1 should have blocks")

		// Verify L1 client can fetch balance
		balance, err := env.Clients.L1.BalanceAt(ctx, env.L1.Transactor.From, nil)
		require.NoError(t, err, "L1 client should be able to fetch balance")
		require.True(t, balance.Cmp(big.NewInt(0)) > 0, "L1 account should have balance")
	})

	t.Run("VerifyL2Connectivity", func(t *testing.T) {
		// Verify L2 client can fetch chain ID
		l2ChainID, err := env.Clients.L2.ChainID(ctx)
		require.NoError(t, err, "L2 client should be able to fetch chain ID")
		require.Equal(t, env.L2.ChainID, l2ChainID, "L2 client chain ID should match")

		// Verify L2 client can fetch block number
		const allowedRetries = 5
		success := false
		for i := 0; i < allowedRetries; i++ {
			l2BlockNumber, err := env.Clients.L2.BlockNumber(ctx)
			require.NoError(t, err, "L2 client should be able to fetch block number")
			if l2BlockNumber > 0 {
				success = true
				break
			}
			time.Sleep(time.Second)
		}
		require.True(t, success, "L2 client should eventually be able to fetch block number")

		// Verify L2 client can fetch balance
		balance, err := env.Clients.L2.BalanceAt(ctx, env.L2.Transactor.From, nil)
		require.NoError(t, err, "L2 client should be able to fetch balance")
		require.True(t, balance.Cmp(big.NewInt(0)) > 0, "L2 account should have balance")
	})

	t.Run("VerifyBridgeServiceConnectivity", func(t *testing.T) {
		// Verify bridge service health check
		healthResp, err := env.Clients.BridgeService.HealthCheck(ctx)
		require.NoError(t, err, "BridgeService client should be able to perform health check")
		require.NotNil(t, healthResp, "Health check response should not be nil")

		// Verify bridge service sync status
		syncStatus, err := env.Clients.BridgeService.GetSyncStatus(ctx)
		require.NoError(t, err, "BridgeService should return sync status")
		require.NotNil(t, syncStatus, "Sync status should not be nil")
	})

	t.Run("VerifyL1Contracts", func(t *testing.T) {
		callOpts := &bind.CallOpts{Context: ctx}

		// Verify rollup manager contract is accessible
		// Try to call a view function to ensure contract is properly initialized
		_, err := env.L1.Contracts.RollupManager.GetRollupExitRoot(callOpts)
		require.NoError(t, err, "Should be able to call RollupManager contract")
	})

	t.Run("VerifyL2Contracts", func(t *testing.T) {
		callOpts := &bind.CallOpts{Context: ctx}

		// Verify L2 bridge contract is accessible
		_, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
		require.NoError(t, err, "Should be able to call L2Bridge contract")

		// Verify global exit root contract is accessible
		_, err = env.L2.Contracts.GlobalExitRoot.LastRollupExitRoot(callOpts)
		require.NoError(t, err, "Should be able to call GlobalExitRoot contract")
	})
}

func TestLoadEnv_InvalidEnvName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env, err := LoadEnv(ctx, ENVName("non-existent-env"))
	require.Error(t, err, "LoadEnv should return an error for non-existent environment")
	require.Nil(t, env, "Env should be nil when error occurs")
}

func TestFindEnvsDir(t *testing.T) {
	// Test that findEnvsDir works from current directory
	envsDir, err := findEnvsDir()
	require.NoError(t, err, "findEnvsDir should not return an error")
	require.NotEmpty(t, envsDir, "envs directory path should not be empty")

	// Verify the directory exists
	info, err := os.Stat(envsDir)
	require.NoError(t, err, "envs directory should exist")
	require.True(t, info.IsDir(), "envs path should be a directory")

	// Verify op-pp subdirectory exists
	opPPDir := filepath.Join(envsDir, string(EnvOpPP))
	info, err = os.Stat(opPPDir)
	require.NoError(t, err, "op-pp directory should exist")
	require.True(t, info.IsDir(), "op-pp path should be a directory")

	// Verify summary.json exists
	summaryPath := filepath.Join(opPPDir, "summary.json")
	_, err = os.Stat(summaryPath)
	require.NoError(t, err, "summary.json should exist")
}
