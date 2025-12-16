package etherman

import (
	"context"
	"fmt"
	"math/big"

	aggkitcommon "github.com/agglayer/aggkit/common"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

var _ aggkittypes.EthClienter = (*DefaultEthClient)(nil)

type DefaultEthClient struct {
	aggkittypes.EthereumClienter
	aggkittypes.RPCClienter

	// If true, the block Hash is getted from JSON RPC
	// if false, the block Hash is getted from go-ethereum RLP hashing of header
	HashFromJSON bool
	logger       aggkitcommon.Logger
}

// DialWithRetry attempts to connect to an Ethereum client with retries and exponential backoff.
// It returns an EthClienter on success or an error if all attempts fail.
func DialWithRetry(ctx context.Context,
	logger aggkitcommon.Logger,
	cfg *ethermanconfig.RPCClientConfig) (aggkittypes.EthClienter, error) {
	retryHandler, err := cfg.NewRetryHandler()
	if err != nil {
		return nil, fmt.Errorf("failed to create retry handler: %w", err)
	}
	if logger == nil {
		logger = log.NewLoggerNil()
	}
	return aggkitcommon.Execute(retryHandler, ctx, log.Infof, fmt.Sprintf("dial %s rpc", cfg.URL),
		func() (aggkittypes.EthClienter, error) {
			client, err := ethclient.Dial(cfg.URL)
			if err != nil {
				return nil, err
			}
			return NewDefaultEthClientWithLogger(logger, client, client.Client(), cfg), nil
		})
}

// This function is for legacy code that doesn't use logger
func NewDefaultEthClient(client aggkittypes.EthereumClienter,
	rpcClient aggkittypes.RPCClienter,
	cfg *ethermanconfig.RPCClientConfig,
) *DefaultEthClient {
	return NewDefaultEthClientWithLogger(log.NewLoggerNil(), client, rpcClient, cfg)
}

func NewDefaultEthClientWithLogger(
	logger aggkitcommon.Logger,
	client aggkittypes.EthereumClienter,
	rpcClient aggkittypes.RPCClienter,
	cfg *ethermanconfig.RPCClientConfig,
) *DefaultEthClient {
	if cfg == nil {
		cfg = ethermanconfig.NewDefaultRPCClientConfig()
	}
	hashFromJSON := cfg.HashFromJSON
	// HashFromJSON requires rpcClient
	if rpcClient == nil && cfg.HashFromJSON {
		logger.Warnf("rpcClient is nil, cannot use HashFromJSON=true, setting to false")
		hashFromJSON = false
	}

	return &DefaultEthClient{
		EthereumClienter: client,
		RPCClienter:      rpcClient,
		HashFromJSON:     hashFromJSON,
		logger:           logger,
	}
}

func (c *DefaultEthClient) CustomBlockNumber(ctx context.Context, number aggkittypes.BlockName) (uint64, error) {
	ethHeader, err := c.HeaderByNumber(ctx, number.ToBigInt())
	if err != nil {
		return 0, err
	}
	return ethHeader.Number.Uint64(), nil
}

func (c *DefaultEthClient) CustomHeaderByNumber(ctx context.Context,
	number *aggkittypes.BlockNumberFinality) (*aggkittypes.BlockHeader, error) {
	if number == nil {
		number = &aggkittypes.LatestBlock
	}
	// The number can have an offset, so maybe we need to resolve the blockName, apply offset to require the header
	numberBigInt, err := c.resolveBlockNumber(ctx, number)
	if err != nil {
		return nil, err
	}
	var result *aggkittypes.BlockHeader
	if c.HashFromJSON {
		result, err = c.rpcGetBlockByNumber(ctx, numberBigInt)
		if err != nil {
			return nil, err
		}
	} else {
		ethHeader, err := c.HeaderByNumber(ctx, numberBigInt)
		if err != nil {
			return nil, err
		}
		result = aggkittypes.NewBlockHeaderFromEthHeader(ethHeader)
	}

	result.RequestedBlock = number
	return result, nil
}

func (c *DefaultEthClient) resolveBlockNumber(ctx context.Context,
	number *aggkittypes.BlockNumberFinality) (*big.Int, error) {
	// If is a number or don't have offset with 1 query it's enough
	if number.IsConstant() || !number.HasOffset() {
		return number.ToBigInt(), nil
	}
	// Resolve the base block number
	hdr, err := c.rpcGetBlockByNumber(ctx, number.ToBigInt())
	if err != nil {
		return nil, err
	}
	num := number.CalculateBlockNumber(hdr.Number)
	return big.NewInt(0).SetUint64(num), nil
}

func (c *DefaultEthClient) rpcGetBlockByNumber(ctx context.Context, number *big.Int) (*aggkittypes.BlockHeader, error) {
	var blockArg string
	if number == nil {
		blockArg = rpc.BlockNumber(rpc.LatestBlockNumber).String()
	} else {
		blockArg = rpc.BlockNumber(number.Int64()).String()
	}
	var rawEthHeader *blockRawEth
	err := c.CallContext(ctx, &rawEthHeader, "eth_getBlockByNumber", blockArg, false)
	if err != nil {
		return nil, fmt.Errorf("rpcGetBlockByNumber: %w", err)
	}
	return rawEthHeader.ToBlockHeader()
}

func (c *DefaultEthClient) Call(result any, method string, args ...any) error {
	if c.RPCClienter == nil {
		return ErrNotImplemented
	}
	return c.RPCClienter.Call(result, method, args...)
}

func (c *DefaultEthClient) BatchCallContext(ctx context.Context, b []rpc.BatchElem) error {
	if c.RPCClienter == nil {
		return ErrNotImplemented
	}
	return c.RPCClienter.BatchCallContext(ctx, b)
}

func (c *DefaultEthClient) CallContext(ctx context.Context,
	result interface{}, method string, args ...interface{}) error {
	if c.RPCClienter == nil {
		return ErrNotImplemented
	}
	return c.RPCClienter.CallContext(ctx, result, method, args...)
}
