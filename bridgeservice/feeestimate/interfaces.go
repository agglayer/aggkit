package feeestimate

import (
	"context"
	"math/big"

	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// ChainReader is the narrow slice of an Ethereum JSON-RPC client the estimator needs:
// transaction and receipt reads, a header read, gas price suggestions and contract calls.
// It is deliberately structural: aggkit's EthClienter and go-ethereum's *ethclient.Client
// both satisfy it without an adapter, so the wiring step passes them straight through.
type ChainReader interface {
	// TransactionByHash returns a transaction by hash, and whether it is still pending.
	TransactionByHash(ctx context.Context, hash common.Hash) (*ethtypes.Transaction, bool, error)
	// TransactionReceipt returns the receipt of a mined transaction, or ethereum.NotFound
	// when the transaction is unknown or not mined yet.
	TransactionReceipt(ctx context.Context, hash common.Hash) (*ethtypes.Receipt, error)
	// HeaderByNumber returns a header of the canonical chain; nil number means the latest.
	HeaderByNumber(ctx context.Context, number *big.Int) (*ethtypes.Header, error)
	// SuggestGasPrice returns the chain's eth_gasPrice.
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	// SuggestGasTipCap returns the chain's eth_maxPriorityFeePerGas.
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	// CallContract executes an Ethereum contract call with the specified input data.
	CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
}

// ClaimsReader reads the claims claimsync has indexed for the estimator's own network. It
// carries the GetClaimsPaged shape of bridgeservice.Claimer so the claimsync service
// satisfies it directly; pages are 1-based and newest first.
type ClaimsReader interface {
	GetClaimsPaged(ctx context.Context, page, pageSize uint32,
		networkIDs []uint32, globalIndex *big.Int) ([]*claimsynctypes.Claim, int, error)
}

// BridgesReader reads the bridges bridgesync has indexed for the estimator's own network,
// with the GetBridgesPaged shape of bridgeservice.Bridger. The claim estimator does not use
// it; the bridge-side estimate (per mechanism) does.
type BridgesReader interface {
	GetBridgesPaged(ctx context.Context, pageNumber, pageSize uint32,
		depositCount *uint64, networkIDs []uint32, fromAddress string) ([]*bridgesync.Bridge, int, error)
}
