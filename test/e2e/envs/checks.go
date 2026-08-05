package envs

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
)

// CheckEnv validates that the environment is correctly configured and all services are accessible.
// It is intended to be called as a pre-test sanity check in TestMain after loading the environment.
func (e *Env) CheckEnv(ctx context.Context) error {
	if err := e.checkConfiguration(); err != nil {
		return fmt.Errorf("VerifyConfiguration: %w", err)
	}
	if err := e.checkL1Connectivity(ctx); err != nil {
		return fmt.Errorf("VerifyL1Connectivity: %w", err)
	}
	if err := e.checkL2Connectivity(ctx); err != nil {
		return fmt.Errorf("VerifyL2Connectivity: %w", err)
	}
	if err := e.checkBridgeServiceConnectivity(ctx); err != nil {
		return fmt.Errorf("VerifyBridgeServiceConnectivity: %w", err)
	}
	if err := e.checkL1Contracts(ctx); err != nil {
		return fmt.Errorf("VerifyL1Contracts: %w", err)
	}
	if err := e.checkL2Contracts(ctx); err != nil {
		return fmt.Errorf("VerifyL2Contracts: %w", err)
	}

	// For multi-chain envs, also validate the secondary L2 network (L2B).
	if e.L2B != nil {
		if err := checkL2ConnectivityFor(ctx, e.L2B, e.L2B.Client); err != nil {
			return fmt.Errorf("VerifyL2BConnectivity: %w", err)
		}
		if err := checkBridgeServiceConnectivityFor(ctx, e.L2B.BridgeService); err != nil {
			return fmt.Errorf("VerifyL2BBridgeServiceConnectivity: %w", err)
		}
		if err := checkL2ContractsFor(ctx, e.L2B); err != nil {
			return fmt.Errorf("VerifyL2BContracts: %w", err)
		}
	}
	return nil
}

// checkConfiguration verifies the env struct is populated with expected values for EnvOpPP.
func (e *Env) checkConfiguration() error {
	if e.L1.ChainID == nil {
		return fmt.Errorf("L1 ChainID is nil")
	}
	if e.L1.ChainID.String() != "271828" {
		return fmt.Errorf("L1 ChainID: got %s, want 271828", e.L1.ChainID)
	}
	if e.L1.Contracts.RollupManager == nil {
		return fmt.Errorf("RollupManager contract is nil")
	}
	if e.L1.Contracts.Bridge == nil {
		return fmt.Errorf("bridge contract is nil")
	}
	if e.L1.Transactor == nil {
		return fmt.Errorf("L1 Transactor is nil")
	}

	// Expected L2A chain ID depends on the env: op-pp uses 2151908, op-pp-2chains uses 20201.
	wantL2AChainID := "2151908"
	if e.envName == EnvOpPP2Chains {
		wantL2AChainID = "20201"
	}
	if err := checkL2Configured(e.L2, wantL2AChainID, "L2"); err != nil {
		return err
	}

	// For multi-chain envs, validate the secondary L2 (L2B) configuration.
	if e.L2B != nil {
		if err := checkL2Configured(*e.L2B, "20202", "L2B"); err != nil {
			return err
		}
	}

	if e.Clients.L1 == nil {
		return fmt.Errorf("L1 client is nil")
	}
	if e.Clients.L2 == nil {
		return fmt.Errorf("L2 client is nil")
	}
	if e.Clients.BridgeService == nil {
		return fmt.Errorf("BridgeService client is nil")
	}
	return nil
}

// checkL2Configured verifies a single L2Config is populated with the expected chain ID and
// non-nil contracts/transactor. label is used in error messages (e.g. "L2" or "L2B").
func checkL2Configured(l2 L2Config, wantChainID, label string) error {
	if l2.ChainID == nil {
		return fmt.Errorf("%s ChainID is nil", label)
	}
	if l2.ChainID.String() != wantChainID {
		return fmt.Errorf("%s ChainID: got %s, want %s", label, l2.ChainID, wantChainID)
	}
	if l2.Contracts.L2Bridge == nil {
		return fmt.Errorf("%s L2Bridge contract is nil", label)
	}
	if l2.Contracts.GlobalExitRoot == nil {
		return fmt.Errorf("%s GlobalExitRoot contract is nil", label)
	}
	if l2.Transactor == nil {
		return fmt.Errorf("%s Transactor is nil", label)
	}
	return nil
}

func (e *Env) checkL1Connectivity(ctx context.Context) error {
	l1ChainID, err := e.Clients.L1.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("fetch chain ID: %w", err)
	}
	if l1ChainID.Cmp(e.L1.ChainID) != 0 {
		return fmt.Errorf("chain ID mismatch: got %s, want %s", l1ChainID, e.L1.ChainID)
	}

	l1BlockNumber, err := e.Clients.L1.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("fetch block number: %w", err)
	}
	if l1BlockNumber == 0 {
		return fmt.Errorf("L1 has no blocks")
	}

	balance, err := e.Clients.L1.BalanceAt(ctx, e.L1.Transactor.From, nil)
	if err != nil {
		return fmt.Errorf("fetch balance: %w", err)
	}
	if balance.Cmp(big.NewInt(0)) <= 0 {
		return fmt.Errorf("L1 account balance is zero")
	}
	return nil
}

func (e *Env) checkL2Connectivity(ctx context.Context) error {
	return checkL2ConnectivityFor(ctx, &e.L2, e.Clients.L2)
}

// checkL2ConnectivityFor validates RPC connectivity, chain ID, block production and account
// balance for a single L2 network using the provided client.
func checkL2ConnectivityFor(ctx context.Context, l2 *L2Config, l2Client *ethclient.Client) error {
	l2ChainID, err := l2Client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("fetch chain ID: %w", err)
	}
	if l2ChainID.Cmp(l2.ChainID) != 0 {
		return fmt.Errorf("chain ID mismatch: got %s, want %s", l2ChainID, l2.ChainID)
	}

	const allowedRetries = 5
	success := false
	for range allowedRetries {
		l2BlockNumber, err := l2Client.BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("fetch block number: %w", err)
		}
		if l2BlockNumber > 0 {
			success = true
			break
		}
		time.Sleep(time.Second)
	}
	if !success {
		return fmt.Errorf("L2 block number did not exceed 0 after %d retries", allowedRetries)
	}

	balance, err := l2Client.BalanceAt(ctx, l2.Transactor.From, nil)
	if err != nil {
		return fmt.Errorf("fetch balance: %w", err)
	}
	if balance.Cmp(big.NewInt(0)) <= 0 {
		return fmt.Errorf("L2 account balance is zero")
	}
	return nil
}

func (e *Env) checkBridgeServiceConnectivity(ctx context.Context) error {
	return checkBridgeServiceConnectivityFor(ctx, e.Clients.BridgeService)
}

// checkBridgeServiceConnectivityFor validates a bridge-service client responds to health and
// sync-status calls.
func checkBridgeServiceConnectivityFor(ctx context.Context, bridgeService *client.Client) error {
	healthResp, err := bridgeService.HealthCheck(ctx)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	if healthResp == nil {
		return fmt.Errorf("health check response is nil")
	}

	syncStatus, err := bridgeService.GetSyncStatus(ctx)
	if err != nil {
		return fmt.Errorf("get sync status: %w", err)
	}
	if syncStatus == nil {
		return fmt.Errorf("sync status response is nil")
	}
	return nil
}

func (e *Env) checkL1Contracts(ctx context.Context) error {
	callOpts := &bind.CallOpts{Context: ctx}
	if _, err := e.L1.Contracts.RollupManager.GetRollupExitRoot(callOpts); err != nil {
		return fmt.Errorf("call RollupManager.GetRollupExitRoot: %w", err)
	}
	return nil
}

func (e *Env) checkL2Contracts(ctx context.Context) error {
	return checkL2ContractsFor(ctx, &e.L2)
}

// checkL2ContractsFor exercises read calls against a single L2 network's bridge and
// global-exit-root contracts.
func checkL2ContractsFor(ctx context.Context, l2 *L2Config) error {
	callOpts := &bind.CallOpts{Context: ctx}
	if _, err := l2.Contracts.L2Bridge.NetworkID(callOpts); err != nil {
		return fmt.Errorf("call L2Bridge.NetworkID: %w", err)
	}
	if _, err := l2.Contracts.GlobalExitRoot.LastRollupExitRoot(callOpts); err != nil {
		return fmt.Errorf("call GlobalExitRoot.LastRollupExitRoot: %w", err)
	}
	return nil
}
