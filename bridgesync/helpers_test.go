package bridgesync

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

// startGeth is a test helper function that starts the Geth Docker image and creates a single account that is pre-funded
func startGeth(t *testing.T, ctx context.Context, cancelFn context.CancelFunc) (*ethclient.Client, *bind.TransactOpts) {
	t.Helper()

	log.Debug("starting docker")
	msg, err := exec.Command("bash", "-l", "-c", "docker compose up -d").CombinedOutput()
	require.NoError(t, err, string(msg))

	time.Sleep(time.Second * 1)
	t.Cleanup(func() {
		cancelFn()
		msg, err = exec.Command("bash", "-l", "-c", "docker compose down").CombinedOutput()
		require.NoError(t, err, string(msg))
		log.Debug("docker is shutted down")
	})
	log.Debug("docker started")

	client, err := dialGeth("http://127.0.0.1:8545", 10, time.Second)
	require.NoError(t, err)

	auth := createAuth(t, ctx, "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", client)

	return client, auth
}

// createAuth generates a TransactOpts instance for signing Ethereum transactions.
// It derives an ECDSA key from the given private key and binds it to the client's chain ID.
func createAuth(t *testing.T, ctx context.Context, rawPrivateKey string, client *ethclient.Client) *bind.TransactOpts {
	t.Helper()

	chainID, err := client.ChainID(ctx)
	require.NoError(t, err)

	privateKey, err := crypto.HexToECDSA(rawPrivateKey)
	require.NoError(t, err)

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainID)
	require.NoError(t, err)

	return auth
}

// dialGeth pings Geth for predefined number of times and returns an ethclient.Client instance upon successful dial.
func dialGeth(url string, retries int, delay time.Duration) (*ethclient.Client, error) {
	for i := range retries {
		client, err := ethclient.Dial(url)
		if err == nil {
			return client, nil
		}
		log.Debugf("Waiting for Geth to start... attempt %d/%d", i+1, retries)
		time.Sleep(delay)
	}
	return nil, fmt.Errorf("failed to connect after %d retries", retries)
}

// waitForReceipt waits for the given transaction to get included in a block (namely there should be a receipt for it).
// In case it fails for predefined number of times an error is propagated.
func waitForReceipt(ctx context.Context, client *ethclient.Client, txHash common.Hash, maxAttempts int) (*types.Receipt, error) {
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
			time.Sleep(time.Second)
			attempts++
			continue
		} else if err != nil {
			return nil, err
		}
		return receipt, nil
	}

	return receipt, nil
}
