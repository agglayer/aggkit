package types

import (
	"context"
	"fmt"

	aggkitcommon "github.com/agglayer/aggkit/common"
	commontypes "github.com/agglayer/aggkit/common/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
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
	BatchCallContext(ctx context.Context, b []rpc.BatchElem) error
}

var _ RPCClienter = (*NoopRPCClient)(nil)

// NoopRPCClient is no operation implementation for the RPCClienter interface
type NoopRPCClient struct{}

func (c *NoopRPCClient) Call(result any, method string, args ...any) error {
	return nil
}

func (c *NoopRPCClient) BatchCallContext(ctx context.Context, b []rpc.BatchElem) error {
	return nil
}

// DialWithRetry attempts to connect to an Ethereum client with retries and exponential backoff.
// It returns an EthClienter on success or an error if all attempts fail.
func DialWithRetry(ctx context.Context, url string, retryHandler commontypes.RetryHandler) (EthClienter, error) {
	return aggkitcommon.Execute(retryHandler, ctx, log.Infof, fmt.Sprintf("dial %s rpc", url),
		func() (EthClienter, error) {
			client, err := ethclient.Dial(url)
			if err != nil {
				return nil, err
			}
			return NewDefaultEthClient(client, client.Client()), nil
		})
}
