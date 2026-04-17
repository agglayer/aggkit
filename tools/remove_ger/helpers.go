package remove_ger

import (
	"context"
	"fmt"
	"math/big"
	"time"

	bridgeservice "github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/go_signer/signer"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// buildSovereignAdminTransactor creates a transact options instance for the sovereign admin key
// using the signertypes.SignerConfig from config. Supports local keystore, AWS KMS, and GCP KMS.
func buildSovereignAdminTransactor(ctx context.Context, cfg *Config, l2ChainID *big.Int) (*bind.TransactOpts, error) {
	s, err := signer.NewSigner(ctx, l2ChainID.Uint64(), cfg.RemoveGER.SovereignAdminKey,
		"remove-ger", log.GetDefaultLogger())
	if err != nil {
		return nil, fmt.Errorf("load sovereign admin signer: %w", err)
	}
	if err := s.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize sovereign admin signer: %w", err)
	}
	opts := &bind.TransactOpts{
		From: s.PublicAddress(),
		Signer: func(_ common.Address, tx *gethTypes.Transaction) (*gethTypes.Transaction, error) {
			return s.SignTx(ctx, tx)
		},
	}
	return opts, nil
}

// waitForReceipt waits for the transaction to be mined and returns its receipt.
func waitForReceipt(
	ctx context.Context, client *ethclient.Client, tx *gethTypes.Transaction,
) (*gethTypes.Receipt, error) {
	return bind.WaitMined(ctx, client, tx)
}

func (e *Env) waitReceipt(ctx context.Context, tx *gethTypes.Transaction) (*gethTypes.Receipt, error) {
	if e != nil && e.waitReceiptFn != nil {
		return e.waitReceiptFn(ctx, tx)
	}
	if e == nil || e.L2 == nil {
		return nil, fmt.Errorf("L2 client is nil")
	}
	return waitForReceipt(ctx, e.L2, tx)
}

const pollTickInterval = 2 * time.Second

// pollBridgeService runs check periodically until it returns (true, nil) or the context/timeout expires.
// Returns an error on timeout or if check returns an error.
func pollBridgeService(
	ctx context.Context, bridgeClient *bridgeservice.Client, check func() (bool, error), timeout time.Duration,
) error {
	if bridgeClient == nil {
		return fmt.Errorf("bridge service client is nil")
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(pollTickInterval)
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
