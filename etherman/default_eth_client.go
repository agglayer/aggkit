package etherman

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	aggkitcommon "github.com/agglayer/aggkit/common"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

var (
	_                 aggkittypes.EthClienter = (*DefaultEthClient)(nil)
	ErrNotImplemented                         = errors.New("not implemented")
	// errNullBlock is returned when eth_getBlockByNumber answers null for the requested block
	errNullBlock = errors.New("block is null")
)

type DefaultEthClient struct {
	aggkittypes.EthereumClienter
	aggkittypes.RPCClienter

	// If true, the block Hash is getted from JSON RPC
	// if false, the block Hash is getted from go-ethereum RLP hashing of header
	HashFromJSON      bool
	batchBlockHeaders bool
	// supportNonEthereumWithMaxGapsInBlocks is the max number of previous blocks to look for when a block below the head
	// is missing. 0 means disabled
	supportNonEthereumWithMaxGapsInBlocks uint64
	logger                                aggkitcommon.Logger
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
	batchBlockHeaders := cfg.BatchBlockHeaderRetrieval
	if rpcClient == nil && cfg.BatchBlockHeaderRetrieval {
		batchBlockHeaders = false
	}

	return &DefaultEthClient{
		EthereumClienter:                      client,
		RPCClienter:                           rpcClient,
		HashFromJSON:                          hashFromJSON,
		batchBlockHeaders:                     batchBlockHeaders,
		supportNonEthereumWithMaxGapsInBlocks: cfg.SupportNonEthereumWithMaxGapsInBlocks,
		logger:                                logger,
	}
}

// SupportNonEthereumWithMaxGapsInBlocks returns the max number of previous blocks used to resolve
// a missing block (0 = disabled)
func (c *DefaultEthClient) SupportNonEthereumWithMaxGapsInBlocks() uint64 {
	return c.supportNonEthereumWithMaxGapsInBlocks
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
	result, err := c.internalHeaderByNumber(ctx, numberBigInt)
	if err != nil {
		return nil, err
	}

	result.RequestedBlock = number
	return result, nil
}

// internalHeaderByNumber gets the header of the block numberBigInt. If SupportNonEthereumWithMaxGapsInBlocks is
// set and the block is missing but it is below the chain head (a hole in the block numbering), it returns the nearest
// previous existing block (so result.Number can be lower than the requested number)
func (c *DefaultEthClient) internalHeaderByNumber(ctx context.Context,
	numberBigInt *big.Int) (*aggkittypes.BlockHeader, error) {
	result, err := c.fetchHeaderByNumber(ctx, numberBigInt)
	if err == nil || c.supportNonEthereumWithMaxGapsInBlocks == 0 || !isBlockNotFound(err) ||
		numberBigInt == nil || numberBigInt.Sign() < 0 || !numberBigInt.IsUint64() {
		return result, err
	}
	requested := numberBigInt.Uint64()
	head, headErr := c.BlockNumber(ctx)
	if headErr != nil {
		return nil, fmt.Errorf("block %d not found and cannot get chain head: %w (original: %w)", requested, headErr, err)
	}
	if requested >= head {
		// not a hole: the block doesn't exist yet
		return nil, err
	}
	for i := uint64(1); i <= c.supportNonEthereumWithMaxGapsInBlocks && i <= requested; i++ {
		candidate := requested - i
		result, candErr := c.fetchHeaderByNumber(ctx, new(big.Int).SetUint64(candidate))
		if candErr == nil {
			c.logger.Debugf("block %d is missing (head: %d), using previous block %d", requested, head, candidate)
			return result, nil
		}
		if !isBlockNotFound(candErr) {
			return nil, candErr
		}
	}
	return nil, fmt.Errorf("block %d is missing and no block found in the previous %d blocks: %w",
		requested, c.supportNonEthereumWithMaxGapsInBlocks, err)
}

func isBlockNotFound(err error) bool {
	return errors.Is(err, errNullBlock) || aggkittypes.IsErrNotFound(err)
}

func (c *DefaultEthClient) fetchHeaderByNumber(ctx context.Context,
	numberBigInt *big.Int) (*aggkittypes.BlockHeader, error) {
	var result *aggkittypes.BlockHeader
	var err error
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
	return result, nil
}

func (c *DefaultEthClient) resolveBlockNumber(ctx context.Context,
	number *aggkittypes.BlockNumberFinality) (*big.Int, error) {
	// If is a number or don't have offset with 1 query it's enough
	if number.IsConstant() || !number.HasOffset() {
		return number.ToBigInt(), nil
	}
	// Resolve the base block number
	hdr, err := c.internalHeaderByNumber(ctx, number.ToBigInt())
	if err != nil {
		return nil, err
	}
	num := number.CalculateBlockNumber(hdr.Number)
	return big.NewInt(0).SetUint64(num), nil
}

func (c *DefaultEthClient) rpcGetBlockByNumber(ctx context.Context, number *big.Int) (*aggkittypes.BlockHeader, error) {
	var blockArg string
	if number == nil {
		blockArg = rpc.LatestBlockNumber.String()
	} else {
		blockArg = rpc.BlockNumber(number.Int64()).String()
	}
	var rawEthHeader *blockRawEth
	err := c.CallContext(ctx, &rawEthHeader, "eth_getBlockByNumber", blockArg, false)
	if err != nil {
		return nil, fmt.Errorf("rpcGetBlockByNumber: CallContext error: %w", err)
	}
	if rawEthHeader == nil {
		return nil, fmt.Errorf("rpcGetBlockByNumber:not found:  %s: %w", blockArg, errNullBlock)
	}
	return rawEthHeader.ToBlockHeader()
}

// RetrieveBlockHeaders retrieves block headers for the given block numbers.
// Uses batch RPC when BatchBlockHeaderRetrieval is enabled and an rpcClient is available,
// otherwise falls back to individual requests.
func (c *DefaultEthClient) RetrieveBlockHeaders(
	ctx context.Context, blockNumbers []uint64, maxConcurrency int,
) (*aggkittypes.BlockHeadersResult, error) {
	if c.batchBlockHeaders {
		return RetrieveBlockHeadersBatch(ctx, c.logger, c.RPCClienter, blockNumbers, maxConcurrency)
	}
	return RetrieveBlockHeadersLegacy(ctx, c.logger, c, blockNumbers, maxConcurrency)
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
