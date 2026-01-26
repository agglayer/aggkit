package etherman

import (
	"context"
	"fmt"
	"math/big"
	"sync"

	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"golang.org/x/sync/errgroup"
)

// blockRawEth is eth_getBlockByNumber result structure
type blockRawEth struct {
	Number     string `json:"number"`
	Hash       string `json:"hash"`
	Timestamp  string `json:"timestamp"` // hex string
	ParentHash string `json:"parentHash"`
}

func (b *blockRawEth) String() string {
	return fmt.Sprintf("{Number=%s, Hash=%s, Timestamp=%s, ParentHash=%s}",
		b.Number, b.Hash, b.Timestamp, b.ParentHash)
}

func (b *blockRawEth) ToBlockHeader() (*aggkittypes.BlockHeader, error) {
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

	return &aggkittypes.BlockHeader{
		Number:     number,
		Hash:       hash,
		Time:       timeStamp,
		ParentHash: &parentHash,
	}, nil
}

// https://www.alchemy.com/docs/reference/batch-requests
const batchRequestLimitHTTP = 1000

// RetrieveBlockHeaders retrieves block headers for the given block numbers using batch requests
// if rpcClient is provided
func RetrieveBlockHeaders(ctx context.Context,
	log aggkitcommon.Logger,
	ethClient aggkittypes.BaseEthereumClienter,
	rpcClient aggkittypes.RPCClienter,
	blockNumbers []uint64,
	maxConcurrency int) (aggkittypes.ListBlockHeaders, error) {
	if rpcClient != nil {
		return RetrieveBlockHeadersBatch(ctx, log, rpcClient, blockNumbers, maxConcurrency)
	}
	return RetrieveBlockHeadersLegacy(ctx, log, ethClient, blockNumbers, maxConcurrency)
}

// RetrieveBlockHeaders retrieves block headers for the given block numbers using batch requests
// with concurrency control
func RetrieveBlockHeadersBatch(ctx context.Context,
	log aggkitcommon.Logger,
	rpcClient aggkittypes.RPCClienter,
	blockNumbers []uint64,
	maxConcurrency int) (aggkittypes.ListBlockHeaders, error) {
	return retrieveBlockHeadersInBatchParallel(
		ctx,
		log,
		func(ctx context.Context, blocks []uint64) (aggkittypes.ListBlockHeaders, error) {
			return retrieveBlockHeadersInBatch(ctx, log, rpcClient, blocks)
		}, blockNumbers, batchRequestLimitHTTP, maxConcurrency)
}

// RetrieveBlockHeadersLegacy retrieves block headers for the given block numbers using individual requests
// this is used in simulated environments where batch requests are not supported
func RetrieveBlockHeadersLegacy(ctx context.Context,
	log aggkitcommon.Logger,
	ethClient aggkittypes.BaseEthereumClienter,
	blockNumbers []uint64,
	maxConcurrency int) ([]*aggkittypes.BlockHeader, error) {
	return retrieveBlockHeadersInBatchParallel(
		ctx,
		log,
		func(ctx context.Context, blocks []uint64) (aggkittypes.ListBlockHeaders, error) {
			result := aggkittypes.NewListBlockHeaders(len(blocks))
			for i, blockNumber := range blocks {
				header, err := ethClient.HeaderByNumber(ctx, big.NewInt(int64(blockNumber)))
				if err != nil {
					return nil, fmt.Errorf("RetrieveBlockHeadersLegacy: cannot get block header for block %d: %w",
						blockNumber, err)
				}
				result[i] = aggkittypes.NewBlockHeaderFromEthHeader(header)
			}
			return result, nil
		}, blockNumbers, 1, maxConcurrency)
}

// retrieveBlockHeadersInBatch retrieves block headers for the given block numbers using batch requests
func retrieveBlockHeadersInBatch(ctx context.Context,
	log aggkitcommon.Logger,
	rpcClient aggkittypes.RPCClienter,
	blockNumbers []uint64,
) (aggkittypes.ListBlockHeaders, error) {
	if len(blockNumbers) == 0 {
		return aggkittypes.NewListBlockHeadersEmpty(0), nil
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
			Args:   []interface{}{bn, false},
			Result: headers[idx],
		})
	}

	err := rpcClient.BatchCallContext(ctx, batch)
	timeTracker.Stop()
	if err != nil {
		return nil, fmt.Errorf("retrieveRPCBlockHeadersInBatch(%d): BatchCallContext error: %w", len(blockNumbers), err)
	}
	for i, elem := range batch {
		if elem.Error != nil {
			return nil, fmt.Errorf("retrieveRPCBlockHeadersInBatch(%d): batch element %d (%v) error: %w", len(blockNumbers), i,
				elem.Args,
				elem.Error)
		}
	}
	log.Debugf("retrieveRPCBlockHeadersInBatch: Retrieved block headers for blocks %d in %s (elapsed)",
		len(blockNumbers), timeTracker.Duration().String())
	return convertSliceBlockRawEth(headers)
}

// retrieveBlockHeadersInBatchParallel split request into chuncks and execute it in parallel
func retrieveBlockHeadersInBatchParallel(
	ctx context.Context,
	logger aggkitcommon.Logger,
	funcRetrieval func(context.Context, []uint64) (aggkittypes.ListBlockHeaders, error),
	blockNumbers []uint64,
	chunckSize, maxConcurrency int) (aggkittypes.ListBlockHeaders, error) {
	var mu sync.Mutex
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency)
	chuncks := splitBlockNumbersIntoChunks(blockNumbers, chunckSize)
	results := make(map[uint64]*aggkittypes.BlockHeader, len(blockNumbers))
	for _, chunck := range chuncks {
		g.Go(func() error {
			headers, err := funcRetrieval(ctx, chunck)
			if err != nil {
				return fmt.Errorf("RetrieveBlockHeadersInBatchParallel:  %w", err)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, header := range headers {
				results[header.Number] = header
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	// convert map to sorted slice by block number
	finalResults := make([]*aggkittypes.BlockHeader, len(blockNumbers))
	for idx, bn := range blockNumbers {
		finalResults[idx] = results[bn]
	}

	logger.Debugf("retrieveRPCBlockHeadersInParallel: Retrieved block headers for blocks %d",
		len(blockNumbers))
	return finalResults, nil
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

func convertSliceBlockRawEth(blocks []*blockRawEth) ([]*aggkittypes.BlockHeader, error) {
	result := make([]*aggkittypes.BlockHeader, 0, len(blocks))
	for idx, blockRawEth := range blocks {
		bh, err := blockRawEth.ToBlockHeader()
		if err != nil {
			return nil, fmt.Errorf("convert: converting block number %d (%s): %w", idx, blocks[idx].String(), err)
		}
		result = append(result, bh)
	}
	return result, nil
}
