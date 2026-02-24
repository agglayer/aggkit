package remove_ger

import (
	"context"
	"fmt"
	"math/big"
	"time"

	bridgeservice "github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/common"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// buildSovereignAdminTransactor loads the sovereign admin key from config and returns
// transact options for the given chain ID (L2). Uses common.NewKeyFromKeystore.
func buildSovereignAdminTransactor(cfg *Config, chainID *big.Int) (*bind.TransactOpts, error) {
	kc := cfg.RemoveGER.SovereignAdminPrivateKey
	if kc.Path == "" || kc.Password == "" {
		return nil, fmt.Errorf("sovereign admin keystore path and password are required in [RemoveGER.SovereignAdminPrivateKey]")
	}
	keyCfg := cfgtypes.KeystoreFileConfig{Path: kc.Path, Password: kc.Password}
	privKey, err := common.NewKeyFromKeystore(keyCfg)
	if err != nil {
		return nil, fmt.Errorf("load sovereign admin key: %w", err)
	}
	if privKey == nil {
		return nil, fmt.Errorf("sovereign admin key is nil (empty path/password?)")
	}
	opts, err := bind.NewKeyedTransactorWithChainID(privKey, chainID)
	if err != nil {
		return nil, fmt.Errorf("create transactor: %w", err)
	}
	return opts, nil
}

// waitForReceipt waits for the transaction to be mined and returns its receipt.
func waitForReceipt(ctx context.Context, client *ethclient.Client, tx *types.Transaction) (*types.Receipt, error) {
	return bind.WaitMined(ctx, client, tx)
}

// pollBridgeService runs check periodically until it returns (true, nil) or the context/timeout expires.
// Returns an error on timeout or if check returns an error.
func pollBridgeService(ctx context.Context, bridgeClient *bridgeservice.Client, check func() (bool, error), timeout time.Duration) error {
	if bridgeClient == nil {
		return fmt.Errorf("bridge service client is nil")
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		done, err := check()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %v waiting for bridge service", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// continue
		}
	}
}
