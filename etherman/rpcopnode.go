package etherman

import (
	"context"
	"fmt"
	"math/big"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/opnode"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

const (
	ExtraParamFieldName = "OpNodeURL"
)

type OpNodeClienter interface {
	FinalizedL2Block() (*opnode.BlockInfo, error)
}

// EthDecorator is a decorator for the ethclient.Client that intercepts calls to the HeaderByNumber method
// and if the block number is the FinalizedBlockNumber, it will ask the OpNodeClient for the finalized block
// instead of asking the ethclient.Client
type ethRealClient = EthClienter
type RPCOpNodeDecorator struct {
	ethRealClient
	OpNodeClient OpNodeClienter
}

func NewRPCClientModeOp(cfg ethermanconfig.RPCClientConfig) (EthClienter, error) {
	opNodeURL, err := cfg.GetString(ExtraParamFieldName)
	if err != nil {
		return nil, fmt.Errorf("field %s not found in extra params. Err: %w", ExtraParamFieldName, err)
	}
	log.Debugf("Creating OPNode RPC client with URL %s %s:%s", cfg.URL, ExtraParamFieldName, opNodeURL)
	basicClient, err := ethclient.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("fails to create RPC client. Err: %w", err)
	}
	opNodeClient := opnode.NewOpNodeClient(opNodeURL)
	return NewRPCOpNodeDecorator(basicClient, opNodeClient), nil
}

func NewRPCOpNodeDecorator(client EthClienter, opNodeClient OpNodeClienter) *RPCOpNodeDecorator {
	return &RPCOpNodeDecorator{
		ethRealClient: client,
		OpNodeClient:  opNodeClient,
	}
}

func (f *RPCOpNodeDecorator) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	if number != nil && rpc.BlockNumber(number.Int64()) == rpc.FinalizedBlockNumber {
		// Is asking for finalized block, so we intercept the call and ask to op-node
		blockInfo, err := f.OpNodeClient.FinalizedL2Block()
		if err != nil {
			return nil, err
		}
		return f.ethRealClient.HeaderByNumber(ctx, new(big.Int).SetUint64(blockInfo.Number))
	}
	return f.ethRealClient.HeaderByNumber(ctx, number)
}

func (f *RPCOpNodeDecorator) Client() *rpc.Client {
	return f.Client()
}
