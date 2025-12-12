package types

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

// EthClienter defines the methods for an Ethereum RPC client.
type EthClienter interface {
	BaseEthereumClienter
	RPCClienter
	CustomEthereumClienter
}

// EthChainReader defines methods to read blocks and headers from the Ethereum chain.
// it's based on ethereum.ChainReader
type EthChainReader interface {
	HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
}

type EthereumClienter interface {
	ethereum.BlockNumberReader
	ethereum.ChainIDReader
	EthChainReader
	ethereum.ChainStateReader
	ethereum.LogFilterer
	ethereum.TransactionReader
	bind.ContractBackend
}

// BaseEthereumClienter defines the methods required to interact with an Ethereum client.
type BaseEthereumClienter interface {
	ethereum.BlockNumberReader
	ethereum.ChainIDReader
	EthChainReader
	ethereum.ChainStateReader
	ethereum.LogFilterer
	ethereum.TransactionReader
	bind.ContractBackend
	CustomEthereumClienter
}

type CustomEthereumClienter interface {
	// Like HeaderByNumber but returns a custom BlockHeader type.
	CustomHeaderByNumber(ctx context.Context, number *BlockNumberFinality) (*BlockHeader, error)
}

// RPCClienter defines an interface for making generic RPC calls.
type RPCClienter interface {
	Call(result any, method string, args ...any) error
	CallContext(ctx context.Context, result interface{}, method string, args ...interface{}) error
	BatchCallContext(ctx context.Context, b []rpc.BatchElem) error
}
