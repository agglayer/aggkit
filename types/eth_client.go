package types

import (
	"math"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	// Base for exponential backoff
	backoffBase = 2
)

var _ EthClienter = (*DefaultEthClient)(nil)

// DefaultEthClient is the default implementation of EthClienter.
type DefaultEthClient struct {
	BaseEthereumClienter
	RPCClienter
}

// NewDefaultEthClient creates a new DefaultEthClient.
func NewDefaultEthClient(baseClient BaseEthereumClienter, rpcClient RPCClienter) *DefaultEthClient {
	return &DefaultEthClient{
		BaseEthereumClienter: baseClient,
		RPCClienter:          rpcClient,
	}
}

// EthClienter defines the methods for an Ethereum RPC client.
type EthClienter interface {
	BaseEthereumClienter
	RPCClienter
}

// BaseEthereumClienter defines the methods required to interact with an Ethereum client.
type BaseEthereumClienter interface {
	ethereum.BlockNumberReader
	ethereum.ChainIDReader
	ethereum.ChainReader
	ethereum.ChainStateReader
	ethereum.LogFilterer
	ethereum.TransactionReader
	bind.ContractBackend
}

// RPCClienter defines an interface for making generic RPC calls.
type RPCClienter interface {
	Call(result any, method string, args ...any) error
}

var _ RPCClienter = (*NoopRPCClient)(nil)

// NoopRPCClient is no operation implementation for the RPCClienter interface
type NoopRPCClient struct{}

func (c *NoopRPCClient) Call(result any, method string, args ...any) error {
	return nil
}

// DialWithRetry attempts to connect to an Ethereum client with retries and exponential backoff.
// It returns an EthClienter on success or an error if all attempts fail.
func DialWithRetry(url string, maxRetries int, initialBackoff, maxBackoff time.Duration) (EthClienter, error) {
	var (
		client *ethclient.Client
		err    error
	)

	// If maxRetries is 0, we try to connect once without retries.
	if maxRetries == 0 {
		maxRetries = 1
	}

	for attempt := range maxRetries {
		client, err = ethclient.Dial(url)
		if err == nil {
			return NewDefaultEthClient(client, client.Client()), nil
		}

		backoff := float64(initialBackoff) * math.Pow(backoffBase, float64(attempt))
		wait := time.Duration(math.Min(backoff, float64(maxBackoff)))
		log.Warnf("Dialing %s failed (attempt %d/%d): %v. Retrying in %s...", url, attempt+1, maxRetries+1, err, backoff)
		time.Sleep(wait)
	}

	return nil, err
}
