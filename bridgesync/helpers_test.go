package bridgesync

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// startGeth is a test helper function that starts the Geth Docker image and creates a single account that is pre-funded
func startGeth(t *testing.T, ctx context.Context, cancelFn context.CancelFunc) (aggkittypes.EthClienter, *bind.TransactOpts) {
	t.Helper()

	log.Debug("starting Geth docker container")
	msg, err := exec.Command("bash", "-l", "-c", "docker compose up -d").CombinedOutput()
	require.NoError(t, err, string(msg))

	time.Sleep(time.Second * 1)
	t.Cleanup(func() {
		cancelFn()
		msg, err = exec.Command("bash", "-l", "-c", "docker compose down").CombinedOutput()
		require.NoError(t, err, string(msg))
		log.Debug("Geth docker container is shutted down")
	})
	log.Debug("Geth docker container is started")

	cfg, err := aggkitcommon.NewRetryBackoffConfig(
		&aggkitcommon.RetryPolicyGenericConfig{
			MaxRetries:        10,
			InitialBackoff:    cfgtypes.NewDuration(time.Second),
			MaxBackoff:        cfgtypes.NewDuration(2 * time.Second),
			BackoffMultiplier: 2.0,
		})
	require.NoError(t, err, "failed to create retry backoff config")

	retryHandler, err := cfg.NewRetryHandler()
	require.NoError(t, err, "failed to create retry handler")

	client, err := etherman.DialWithRetry(t.Context(), "http://127.0.0.1:8545", retryHandler)
	require.NoError(t, err)

	auth := createAuth(t, ctx, "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", client)

	return client, auth
}

// createAuth generates a TransactOpts instance for signing Ethereum transactions.
// It derives an ECDSA key from the given private key and binds it to the client's chain ID.
func createAuth(t *testing.T, ctx context.Context, rawPrivateKey string, client aggkittypes.EthClienter) *bind.TransactOpts {
	t.Helper()

	chainID, err := client.ChainID(ctx)
	require.NoError(t, err)

	privateKey, err := crypto.HexToECDSA(rawPrivateKey)
	require.NoError(t, err)

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainID)
	require.NoError(t, err)

	return auth
}

// waitForReceipt waits for the given transaction to get included in a block (namely there should be a receipt for it).
// In case it fails for predefined number of times an error is propagated.
func waitForReceipt(ctx context.Context, client aggkittypes.EthClienter, txHash common.Hash, maxAttempts int) (*types.Receipt, error) {
	var (
		receipt  *types.Receipt
		err      error
		attempts int
	)

	for receipt == nil {
		if attempts == maxAttempts {
			return nil, fmt.Errorf("receipt not found for %s tx after %d attempts", txHash, maxAttempts)
		}
		receipt, err = client.TransactionReceipt(ctx, txHash)
		if errors.Is(err, ethereum.NotFound) {
			time.Sleep(500 * time.Millisecond)
			attempts++
			continue
		} else if err != nil {
			return nil, err
		}
		return receipt, nil
	}

	return receipt, nil
}
