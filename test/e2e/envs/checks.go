package envs

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
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

	if e.L2.ChainID == nil {
		return fmt.Errorf("L2 ChainID is nil")
	}
	if e.L2.ChainID.String() != "2151908" {
		return fmt.Errorf("L2 ChainID: got %s, want 2151908", e.L2.ChainID)
	}
	if e.L2.Contracts.L2Bridge == nil {
		return fmt.Errorf("L2Bridge contract is nil")
	}
	if e.L2.Contracts.GlobalExitRoot == nil {
		return fmt.Errorf("GlobalExitRoot contract is nil")
	}
	if e.L2.Transactor == nil {
		return fmt.Errorf("L2 Transactor is nil")
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
	l2ChainID, err := e.Clients.L2.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("fetch chain ID: %w", err)
	}
	if l2ChainID.Cmp(e.L2.ChainID) != 0 {
		return fmt.Errorf("chain ID mismatch: got %s, want %s", l2ChainID, e.L2.ChainID)
	}

	const allowedRetries = 5
	success := false
	for range allowedRetries {
		l2BlockNumber, err := e.Clients.L2.BlockNumber(ctx)
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

	balance, err := e.Clients.L2.BalanceAt(ctx, e.L2.Transactor.From, nil)
	if err != nil {
		return fmt.Errorf("fetch balance: %w", err)
	}
	if balance.Cmp(big.NewInt(0)) <= 0 {
		return fmt.Errorf("L2 account balance is zero")
	}
	return nil
}

func (e *Env) checkBridgeServiceConnectivity(ctx context.Context) error {
	healthResp, err := e.Clients.BridgeService.HealthCheck(ctx)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	if healthResp == nil {
		return fmt.Errorf("health check response is nil")
	}

	syncStatus, err := e.Clients.BridgeService.GetSyncStatus(ctx)
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
	callOpts := &bind.CallOpts{Context: ctx}
	if _, err := e.L2.Contracts.L2Bridge.NetworkID(callOpts); err != nil {
		return fmt.Errorf("call L2Bridge.NetworkID: %w", err)
	}
	if _, err := e.L2.Contracts.GlobalExitRoot.LastRollupExitRoot(callOpts); err != nil {
		return fmt.Errorf("call GlobalExitRoot.LastRollupExitRoot: %w", err)
	}
	return nil
}
