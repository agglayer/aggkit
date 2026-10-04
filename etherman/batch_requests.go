package etherman

import (
	"context"
	"fmt"
	"sort"
	"sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"golang.org/x/sync/errgroup"
)

// blockRawEth is eth_getBlockByNumber result structure
type blockRawEth struct {
	Number     string `json:"number"`
	Hash       string `json:"hash"`
	Timestamp  string `json:"timestamp"` // hex string
	ParentHash string `json:"parentHash"`
	LogsBloom  string `json:"logsBloom"` // hex string, empty if not provided
}

func (b *blockRawEth) String() string {
	return fmt.Sprintf("{Number=%s, Hash=%s, Timestamp=%s, ParentHash=%s, LogsBloom=%s}",
		b.Number, b.Hash, b.Timestamp, b.ParentHash, b.LogsBloom)
}

func (b *blockRawEth) ToBlockHeader() (*aggkittypes.BlockHeader, error) {
	if b.Number == "" && b.Hash == "" {
		return nil, fmt.Errorf("blockRawEth.ToBlockHeader: empty: %w", ErrNotFound)
	}
	number, err := aggkitcommon.ParseUint64HexOrDecimal(b.Number)
	if err != nil {
		return nil, fmt.Errorf("blockRawEth.ToBlockHeader: parsing block number %s: %w", b.Number, err)
	}
	timeStamp, err := aggkitcommon.ParseUint64HexOrDecimal(b.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("blockRawEth.ToBlockHeader: parsing timestamp %s: %w", b.Timestamp, err)
	}
	hash := common.HexToHash(b.Hash)
	parentHash := common.HexToHash(b.ParentHash)

	var logsBloom *ethtypes.Bloom
	if b.LogsBloom != "" {
		bloom := ethtypes.BytesToBloom(common.FromHex(b.LogsBloom))
		logsBloom = &bloom
	}

	return &aggkittypes.BlockHeader{
		Number:     number,
		Hash:       hash,
		Time:       timeStamp,
		ParentHash: &parentHash,
		LogsBloom:  logsBloom,
	}, nil
}

// https://www.alchemy.com/docs/reference/batch-requests
const batchRequestLimitHTTP = ethermanconfig.DefaultBatchRequestMaxSize

// BlockHeadersResult is an alias for aggkittypes.BlockHeadersResult for backward compatibility.
type BlockHeadersResult = aggkittypes.BlockHeadersResult

// NewBlockHeadersResult creates a new BlockHeadersResult.
func NewBlockHeadersResult() *BlockHeadersResult {
	return aggkittypes.NewBlockHeadersResult()
}

// RetrieveBlockHeaders retrieves block headers for the given block numbers using batch requests
// if rpcClient is provided. Returns a BlockHeadersResult with successful headers and individual errors.
// The returned error is only for catastrophic failures (context cancelled, etc.)
func RetrieveBlockHeaders(ctx context.Context,
	log aggkitcommon.Logger,
	ethClient aggkittypes.BaseEthereumClienter,
	rpcClient aggkittypes.RPCClienter,
	blockNumbers []uint64,
	maxConcurrency int) (*BlockHeadersResult, error) {
	if rpcClient != nil {
		return RetrieveBlockHeadersBatch(ctx, log, rpcClient, blockNumbers, maxConcurrency)
	}
	return RetrieveBlockHeadersLegacy(ctx, log, ethClient, blockNumbers, maxConcurrency)
}

// RetrieveBlockHeadersBatch retrieves block headers for the given block numbers using batch requests
// with concurrency control. Returns a BlockHeadersResult with successful headers and individual errors.
// The adaptive batch size is local to the call: it is not shared across calls. Callers that issue many
// calls should go through DefaultEthClient, which keeps the learned limit for the lifetime of the client.
func RetrieveBlockHeadersBatch(ctx context.Context,
	log aggkitcommon.Logger,
	rpcClient aggkittypes.RPCClienter,
	blockNumbers []uint64,
	maxConcurrency int) (*BlockHeadersResult, error) {
	return retrieveBlockHeadersBatchAdaptive(ctx, log, rpcClient,
		newBatchSizeLimiter(batchRequestLimitHTTP), blockNumbers, maxConcurrency)
}

// retrieveBlockHeadersBatchAdaptive retrieves block headers using batch requests whose size is bounded by limiter.
// When the provider rejects a batch as too large, the limiter is lowered and only the rejected block numbers
// are requested again; headers already retrieved are kept.
func retrieveBlockHeadersBatchAdaptive(ctx context.Context,
	log aggkitcommon.Logger,
	rpcClient aggkittypes.RPCClienter,
	limiter *batchSizeLimiter,
	blockNumbers []uint64,
	maxConcurrency int) (*BlockHeadersResult, error) {
	var mu sync.Mutex
	remaining := blockNumbers
	finalResult := NewBlockHeadersResult()

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chunks := splitBlockNumbersIntoChunks(remaining, limiter.Size())
		var rejected []uint64

		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(maxConcurrency)
		for _, chunk := range chunks {
			g.Go(func() error {
				chunkResult, err := retrieveBlockHeadersInBatch(gctx, log, rpcClient, chunk)
				if err == nil {
					mu.Lock()
					defer mu.Unlock()
					finalResult.Merge(chunkResult)
					return nil
				}
				if aggkitcommon.IsBatchLimitError(err) && len(chunk) > 1 {
					oldSize, newSize, changed := limiter.shrinkAfterRejection(len(chunk), err)
					if changed {
						log.Warnf("etherman: provider rejected a JSON-RPC batch of %d requests, "+
							"reducing batch size %d -> %d: %v", len(chunk), oldSize, newSize, err)
					}
					mu.Lock()
					defer mu.Unlock()
					rejected = append(rejected, chunk...)
					return nil
				}
				return fmt.Errorf("RetrieveBlockHeadersInBatchParallel: %w", err)
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}
		if len(rejected) == 0 {
			log.Debugf("retrieveRPCBlockHeadersInParallel: Retrieved %d/%d block headers",
				len(finalResult.Headers), len(blockNumbers))
			return finalResult, nil
		}
		sort.Slice(rejected, func(i, j int) bool { return rejected[i] < rejected[j] })
		remaining = rejected
	}
}

// RetrieveBlockHeadersLegacy retrieves block headers for the given block numbers using individual requests
// this is used in simulated environments where batch requests are not supported
// Returns a BlockHeadersResult with successful headers and individual errors for failed blocks
func RetrieveBlockHeadersLegacy(ctx context.Context,
	log aggkitcommon.Logger,
	ethClient aggkittypes.BaseEthereumClienter,
	blockNumbers []uint64,
	maxConcurrency int) (*BlockHeadersResult, error) {
	return retrieveBlockHeadersInBatchParallel(
		ctx,
		log,
		func(ctx context.Context, blocks []uint64) (*BlockHeadersResult, error) {
			result := NewBlockHeadersResult()
			for _, blockNumber := range blocks {
				header, err := ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(blockNumber))
				if err != nil {
					result.AddError(blockNumber, fmt.Errorf("cannot get block header: %w", err))
					continue
				}
				result.AddHeader(blockNumber, header)
			}
			return result, nil
		}, blockNumbers, 1, maxConcurrency)
}

// retrieveBlockHeadersInBatch retrieves block headers for the given block numbers using batch requests
// Returns a BlockHeadersResult with successful headers and individual errors for failed blocks
func retrieveBlockHeadersInBatch(ctx context.Context,
	log aggkitcommon.Logger,
	rpcClient aggkittypes.RPCClienter,
	blockNumbers []uint64,
) (*BlockHeadersResult, error) {
	result := NewBlockHeadersResult()
	if len(blockNumbers) == 0 {
		return result, nil
	}
	headers := make([]*blockRawEth, len(blockNumbers))
	timeTracker := aggkitcommon.NewTimeTracker()
	timeTracker.Start()
	batch := make([]rpc.BatchElem, 0, len(blockNumbers))
	for idx, blockNumber := range blockNumbers {
		headers[idx] = &blockRawEth{}
		bn := fmt.Sprintf("0x%x", blockNumber)
		batch = append(batch, rpc.BatchElem{
			Method: "eth_getBlockByNumber",
			Args:   []any{bn, false},
			Result: headers[idx],
		})
	}

	err := rpcClient.BatchCallContext(ctx, batch)
	timeTracker.Stop()
	if err != nil {
		// Catastrophic error: the whole batch call failed
		return nil, fmt.Errorf("retrieveRPCBlockHeadersInBatch(%d): BatchCallContext error: %w", len(blockNumbers), err)
	}
	// go-ethereum rejects an oversized batch per element with a nil top-level error: lift it to a batch error.
	// A single-element batch cannot be shrunk further, so its error stays a per-block error below.
	if len(batch) > 1 {
		for _, elem := range batch {
			if elem.Error != nil && aggkitcommon.IsBatchLimitError(elem.Error) {
				return nil, fmt.Errorf("retrieveRPCBlockHeadersInBatch(%d): batch rejected by provider: %w",
					len(blockNumbers), elem.Error)
			}
		}
	}

	// Process each element individually, collecting successes and failures
	for i, elem := range batch {
		blockNumber := blockNumbers[i]
		if elem.Error != nil {
			result.AddError(blockNumber, fmt.Errorf("batch element error: %w", elem.Error))
			continue
		}
		// Try to convert the raw block to BlockHeader
		bh, err := headers[i].ToBlockHeader()
		if err != nil {
			result.AddError(blockNumber, fmt.Errorf("converting block: %w", err))
			continue
		}
		result.AddHeader(blockNumber, bh)
	}

	log.Debugf("retrieveRPCBlockHeadersInBatch: Retrieved %d/%d block headers in %s (elapsed)",
		len(result.Headers), len(blockNumbers), timeTracker.Duration().String())
	return result, nil
}

// retrieveBlockHeadersInBatchParallel split request into chuncks and execute it in parallel
// Returns a BlockHeadersResult with all successful headers and individual errors
func retrieveBlockHeadersInBatchParallel(
	ctx context.Context,
	logger aggkitcommon.Logger,
	funcRetrieval func(context.Context, []uint64) (*BlockHeadersResult, error),
	blockNumbers []uint64,
	chunckSize, maxConcurrency int) (*BlockHeadersResult, error) {
	var mu sync.Mutex
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency)
	chuncks := splitBlockNumbersIntoChunks(blockNumbers, chunckSize)
	finalResult := NewBlockHeadersResult()

	for _, chunck := range chuncks {
		g.Go(func() error {
			chunkResult, err := funcRetrieval(ctx, chunck)
			if err != nil {
				// Catastrophic error in this chunk (e.g., context cancelled)
				return fmt.Errorf("RetrieveBlockHeadersInBatchParallel: %w", err)
			}
			mu.Lock()
			defer mu.Unlock()
			finalResult.Merge(chunkResult)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		// Catastrophic error occurred
		return nil, err
	}

	logger.Debugf("retrieveRPCBlockHeadersInParallel: Retrieved %d/%d block headers",
		len(finalResult.Headers), len(blockNumbers))
	return finalResult, nil
}

func splitBlockNumbersIntoChunks(blockNumbers []uint64, chunkSize int) [][]uint64 {
	chunks := make([][]uint64, (len(blockNumbers)+chunkSize-1)/chunkSize)
	currentChunk := make([]uint64, 0, chunkSize)
	idx := 0
	for _, bn := range blockNumbers {
		currentChunk = append(currentChunk, bn)
		if len(currentChunk) >= chunkSize {
			chunks[idx] = currentChunk
			idx++
			currentChunk = make([]uint64, 0, chunkSize)
		}
	}
	if len(currentChunk) > 0 {
		chunks[idx] = currentChunk
	}
	return chunks
}
