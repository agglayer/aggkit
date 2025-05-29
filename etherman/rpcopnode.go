package etherman

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/opnode"
	aggkittypes "github.com/agglayer/aggkit/types"
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

// RPCOpNodeDecorator is a decorator for the ethclient.Client that intercepts calls to the HeaderByNumber method
// and if the block number is the FinalizedBlockNumber, it will ask the OpNodeClient for the finalized block
// instead of asking the ethclient.Client
type RPCOpNodeDecorator struct {
	aggkittypes.EthClienter
	OpNodeClient OpNodeClienter
}

// NewRPCClientModeOp creates a new RPC client that uses the OPNode client to get the finalized block
func NewRPCClientModeOp(cfg ethermanconfig.RPCClientConfig) (aggkittypes.EthClienter, error) {
	opNodeURL, err := cfg.GetString(ExtraParamFieldName)
	if err != nil {
		opNodeURL, err = cfg.GetString(strings.ToLower(ExtraParamFieldName))
	}
	if err != nil {
		return nil, fmt.Errorf("field %s not found in extra params (%+v). Err: %w", ExtraParamFieldName, cfg, err)
	}
	log.Debugf("Creating OPNode RPC client with URL %s %s:%s", cfg.URL, ExtraParamFieldName, opNodeURL)
	basicClient, err := ethclient.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("fails to create RPC client. Err: %w", err)
	}
	opNodeClient := opnode.NewOpNodeClient(opNodeURL)
	ethClient := aggkittypes.NewDefaultEthClient(basicClient, basicClient.Client())
	return NewRPCOpNodeDecorator(ethClient, opNodeClient), nil
}

func NewRPCOpNodeDecorator(client aggkittypes.EthClienter, opNodeClient OpNodeClienter) *RPCOpNodeDecorator {
	return &RPCOpNodeDecorator{
		EthClienter:  client,
		OpNodeClient: opNodeClient,
	}
}

func (f *RPCOpNodeDecorator) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	if number != nil && rpc.BlockNumber(number.Int64()) == rpc.FinalizedBlockNumber {
		// Is asking for finalized block, so we intercept the call and ask to op-node
		blockInfo, err := f.OpNodeClient.FinalizedL2Block()
		if err != nil {
			return nil, err
		}
		return f.EthClienter.HeaderByNumber(ctx, new(big.Int).SetUint64(blockInfo.Number))
	}
	return f.EthClienter.HeaderByNumber(ctx, number)
}
