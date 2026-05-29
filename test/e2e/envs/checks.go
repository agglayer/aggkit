package envs

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
)

// CheckEnv validates that the environment is correctly configured and all services are accessible.
// It is intended to be called as a pre-test sanity check in TestMain after loading the environment.
// The checks are topology-agnostic: they iterate over every L2 network declared by the env
// (Env.L2s) and use the env capabilities to gate checks that are only meaningful for a specific
// gas model or sequencer, so FEP, custom-gas and cdk-erigon envs are tolerated.
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

// checkConfiguration verifies the env struct is populated with the values every env must carry.
// L1 checks remain env-agnostic (non-nil ChainID, contracts, transactor); the L2 checks iterate
// over every network in e.L2s so multi-network envs are validated in full. The L1 chain id is
// asserted to be non-nil/positive rather than a hardcoded literal so the check holds across envs.
func (e *Env) checkConfiguration() error {
	if e.L1.ChainID == nil {
		return fmt.Errorf("L1 ChainID is nil")
	}
	if e.L1.ChainID.Sign() <= 0 {
		return fmt.Errorf("L1 ChainID is not positive: %s", e.L1.ChainID)
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

	if len(e.L2s) == 0 {
		return fmt.Errorf("no L2 networks configured")
	}
	for _, l2 := range e.L2s {
		if err := checkL2Configuration(l2); err != nil {
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

// checkL2Configuration validates a single L2 network's static configuration. The network's
// SummaryKey and NetworkID are included in every error so a failing network is identifiable.
// The L2 chain id is only required to be non-nil/positive here; it is cross-checked against the
// live client in checkL2Connectivity, which is topology-agnostic (no hardcoded chain id).
func checkL2Configuration(l2 L2Config) error {
	if l2.ChainID == nil {
		return fmt.Errorf("L2 ChainID is nil (network %s/%d)", l2.SummaryKey, l2.NetworkID)
	}
	if l2.ChainID.Sign() <= 0 {
		return fmt.Errorf("L2 ChainID is not positive: %s (network %s/%d)", l2.ChainID, l2.SummaryKey, l2.NetworkID)
	}
	if l2.Contracts.L2Bridge == nil {
		return fmt.Errorf("L2Bridge contract is nil (network %s/%d)", l2.SummaryKey, l2.NetworkID)
	}
	if l2.Contracts.GlobalExitRoot == nil {
		return fmt.Errorf("GlobalExitRoot contract is nil (network %s/%d)", l2.SummaryKey, l2.NetworkID)
	}
	if l2.Transactor == nil {
		return fmt.Errorf("L2 Transactor is nil (network %s/%d)", l2.SummaryKey, l2.NetworkID)
	}
	return nil
}

// checkL1Connectivity verifies the L1 RPC is reachable, reports the configured chain id, has
// produced blocks, and that the L1 transactor account is funded.
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

// checkL2Connectivity verifies connectivity for every L2 network in the env. The primary network
// reuses the shared e.Clients.L2 (so single-network op-pp behaves exactly as before); additional
// networks are dialed read-only via their op-geth external URL (AggsenderRPCURL is the aggsender
// RPC, so the dedicated op-geth URL is preferred when available — see clientForNetwork). For each
// network the live chain id is asserted to equal the configured ChainID, which catches
// misconfiguration without hardcoding any env-specific chain id.
func (e *Env) checkL2Connectivity(ctx context.Context) error {
	for i := range e.L2s {
		l2 := e.L2s[i]
		client, cleanup, err := e.clientForNetwork(ctx, l2, i == 0)
		if err != nil {
			return fmt.Errorf("dial L2 client (network %s/%d): %w", l2.SummaryKey, l2.NetworkID, err)
		}
		err = checkSingleL2Connectivity(ctx, client, l2)
		cleanup()
		if err != nil {
			return err
		}
	}
	return nil
}

// clientForNetwork returns an *ethclient.Client for the given L2 network and a cleanup function.
// The primary network reuses the shared e.Clients.L2 client (cleanup is a no-op) so single-network
// envs behave identically to before. Non-primary networks are dialed on demand and the returned
// cleanup closes that connection. Non-primary networks are dialed via their op-geth (L2 EL) URL so
// standard eth_* calls work; AggsenderRPCURL (the aggkit node RPC) is only a last-resort fallback.
func (e *Env) clientForNetwork(ctx context.Context, l2 L2Config, primary bool) (*ethclient.Client, func(), error) {
	if primary {
		return e.Clients.L2, func() {}, nil
	}
	url := l2.OpGethRPCURL
	if url == "" {
		url = l2.AggsenderRPCURL
	}
	if url == "" {
		return nil, func() {}, fmt.Errorf("no RPC URL available for network %s/%d", l2.SummaryKey, l2.NetworkID)
	}
	client, err := ethclient.DialContext(ctx, url)
	if err != nil {
		return nil, func() {}, err
	}
	return client, client.Close, nil
}

// checkSingleL2Connectivity validates chain id, block production and account funding for one L2.
func checkSingleL2Connectivity(ctx context.Context, client *ethclient.Client, l2 L2Config) error {
	l2ChainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("fetch chain ID (network %s/%d): %w", l2.SummaryKey, l2.NetworkID, err)
	}
	if l2ChainID.Cmp(l2.ChainID) != 0 {
		return fmt.Errorf("chain ID mismatch (network %s/%d): got %s, want %s",
			l2.SummaryKey, l2.NetworkID, l2ChainID, l2.ChainID)
	}

	const allowedRetries = 5
	success := false
	for range allowedRetries {
		l2BlockNumber, err := client.BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("fetch block number (network %s/%d): %w", l2.SummaryKey, l2.NetworkID, err)
		}
		if l2BlockNumber > 0 {
			success = true
			break
		}
		time.Sleep(time.Second)
	}
	if !success {
		return fmt.Errorf("L2 block number did not exceed 0 after %d retries (network %s/%d)",
			allowedRetries, l2.SummaryKey, l2.NetworkID)
	}

	balance, err := client.BalanceAt(ctx, l2.Transactor.From, nil)
	if err != nil {
		return fmt.Errorf("fetch balance (network %s/%d): %w", l2.SummaryKey, l2.NetworkID, err)
	}
	if balance.Cmp(big.NewInt(0)) <= 0 {
		return fmt.Errorf("L2 account balance is zero (network %s/%d)", l2.SummaryKey, l2.NetworkID)
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

// checkL2Contracts exercises the per-network L2 contract bindings for every network in the env.
// These bindings exist per-network in e.L2s regardless of sequencer/gas model, so the check loops
// over all of them and tags errors with the network identity.
func (e *Env) checkL2Contracts(ctx context.Context) error {
	callOpts := &bind.CallOpts{Context: ctx}
	for _, l2 := range e.L2s {
		if _, err := l2.Contracts.L2Bridge.NetworkID(callOpts); err != nil {
			return fmt.Errorf("call L2Bridge.NetworkID (network %s/%d): %w", l2.SummaryKey, l2.NetworkID, err)
		}
		if _, err := l2.Contracts.GlobalExitRoot.LastRollupExitRoot(callOpts); err != nil {
			return fmt.Errorf("call GlobalExitRoot.LastRollupExitRoot (network %s/%d): %w",
				l2.SummaryKey, l2.NetworkID, err)
		}
	}
	return nil
}
