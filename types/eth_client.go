package types

import (
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
)

var _ EthClienter = (*DefaultEthClient)(nil)

// DefaultEthClient is the default implementation of EthClient.
type DefaultEthClient struct {
	BaseEthereumClienter
	RPCClienter
}

// EthClienter defines the methods for an Ethereum RPC client.
type EthClienter interface {
	BaseEthereumClienter
	RPCClienter
}

// BaseEthereumClienter defines the methods required to interact with an Ethereum client.
type BaseEthereumClienter interface {
	ethereum.LogFilterer
	ethereum.BlockNumberReader
	ethereum.ChainReader
	ethereum.ChainIDReader
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
