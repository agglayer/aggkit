package opnode

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// EthDecorator is a decorator for the ethclient.Client that intercepts calls to the HeaderByNumber method
// and if the block number is the FinalizedBlockNumber, it will ask the OpNodeClient for the finalized block
// instead of asking the ethclient.Client
type ethRealClient = ethclient.Client
type EthDecorator struct {
	*ethRealClient
	OpNodeClient *OpNodeClient
}

func NewEthDecorator(client *ethclient.Client, opNodeClient *OpNodeClient) *EthDecorator {
	return &EthDecorator{
		ethRealClient: client,
		OpNodeClient:  opNodeClient,
	}
}

func (f *EthDecorator) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	if number != nil && rpc.BlockNumber(number.Uint64()) == rpc.FinalizedBlockNumber {
		// Is asking for finalized block, so we intercept the call and ask to op-node
		blockInfo, err := f.OpNodeClient.FinalizedL2Block()
		if err != nil {
			return nil, err
		}
		return f.ethRealClient.HeaderByNumber(ctx, new(big.Int).SetUint64(blockInfo.Number))
	}
	return f.ethRealClient.HeaderByNumber(ctx, number)
}

func (f *EthDecorator) Client() *rpc.Client {
	return f.Client()
}
