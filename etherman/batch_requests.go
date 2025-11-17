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
)

// blockRawEth is eth_getBlockByNumber result structure
type blockRawEth struct {
	Number     string `json:"number"`
	Hash       string `json:"hash"`
	Timestamp  string `json:"timestamp"` // hex string
	ParentHash string `json:"parentHash"`
}

func (b *blockRawEth) ToBlockHeader() (*aggkittypes.BlockHeader, error) {
	number, err := aggkitcommon.ParseHexUint64(b.Number)
	if err != nil {
		return nil, fmt.Errorf("blockRawEth.ToBlockHeader: parsing block number %s: %w", b.Number, err)
	}
	timeStamp, err := aggkitcommon.ParseHexUint64(b.Timestamp)
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

func convertMapBlockRawEth(blocks map[uint64]*blockRawEth) (map[uint64]*aggkittypes.BlockHeader, error) {
	result := make(map[uint64]*aggkittypes.BlockHeader)
	for bn, bre := range blocks {
		bh, err := bre.ToBlockHeader()
		if err != nil {
			return nil, fmt.Errorf("convert: converting block number %d: %w", bn, err)
		}
		result[bn] = bh
	}
	return result, nil
}

// https://www.alchemy.com/docs/reference/batch-requests
const batchRequestLimitHTTP = 1000

// RetrieveBlockHeaders retrieves block headers for the given block numbers using batch requests
// if rpcClient is provided
func RetrieveBlockHeaders(ctx context.Context,
	log aggkitcommon.Logger,
	ethClient aggkittypes.BaseEthereumClienter,
	rpcClient aggkittypes.RPCClienter,
	blockNumbers map[uint64]struct{},
	maxConcurrency int) (map[uint64]*aggkittypes.BlockHeader, error) {
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
	blockNumbers map[uint64]struct{},
	maxConcurrency int) (map[uint64]*aggkittypes.BlockHeader, error) {
	return retrieveBlockHeadersInBatchParallel(
		log,
		func(blocks map[uint64]struct{}) (map[uint64]*aggkittypes.BlockHeader, error) {
			return retrieveBlockHeadersInBatch(ctx, log, rpcClient, blocks)
		}, blockNumbers, batchRequestLimitHTTP, maxConcurrency)
}

// RetrieveBlockHeadersLegacy retrieves block headers for the given block numbers using individual requests
// this is used in simulated environments where batch requests are not supported
func RetrieveBlockHeadersLegacy(ctx context.Context,
	log aggkitcommon.Logger,
	ethClient aggkittypes.BaseEthereumClienter,
	blockNumbers map[uint64]struct{},
	maxConcurrency int) (map[uint64]*aggkittypes.BlockHeader, error) {
	return retrieveBlockHeadersInBatchParallel(
		log,
		func(blocks map[uint64]struct{}) (map[uint64]*aggkittypes.BlockHeader, error) {
			result := make(map[uint64]*aggkittypes.BlockHeader, len(blocks))
			for blockNumber := range blocks {
				header, err := ethClient.HeaderByNumber(ctx, big.NewInt(int64(blockNumber)))
				if err != nil {
					return nil, fmt.Errorf("RetrieveBlockHeadersLegacy: cannot get block header for block %d: %w",
						blockNumber, err)
				}
				result[blockNumber] = aggkittypes.NewBlockHeaderFromEthBlockHeader(header)
			}
			return result, nil
		}, blockNumbers, 1, maxConcurrency)
}

// retrieveBlockHeadersInBatch retrieves block headers for the given block numbers using batch requests
func retrieveBlockHeadersInBatch(ctx context.Context,
	log aggkitcommon.Logger,
	rpcClient aggkittypes.RPCClienter,
	blockNumbers map[uint64]struct{},
) (map[uint64]*aggkittypes.BlockHeader, error) {
	if len(blockNumbers) == 0 {
		return make(map[uint64]*aggkittypes.BlockHeader), nil
	}
	headers := make(map[uint64]*blockRawEth)
	timeTracker := aggkitcommon.NewTimeTracker()
	timeTracker.Start()
	batch := make([]rpc.BatchElem, 0, len(blockNumbers))
	for blockNumber := range blockNumbers {
		headers[blockNumber] = &blockRawEth{}
		bn := fmt.Sprintf("0x%x", blockNumber)
		batch = append(batch, rpc.BatchElem{
			Method: "eth_getBlockByNumber",
			Args:   []interface{}{bn, false},
			Result: headers[blockNumber],
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
	return convertMapBlockRawEth(headers)
}

// retrieveBlockHeadersInBatchParallel split request into chuncks and execute it in parallel
func retrieveBlockHeadersInBatchParallel(
	logger aggkitcommon.Logger,
	funcRetrieval func(map[uint64]struct{}) (map[uint64]*aggkittypes.BlockHeader, error),
	blockNumbers map[uint64]struct{},
	chunckSize, maxConcurrency int) (map[uint64]*aggkittypes.BlockHeader, error) {
	results := make(map[uint64]*aggkittypes.BlockHeader)
	errs := make([]error, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency)
	chuncks := splitBlockNumbersIntoChunks(blockNumbers, chunckSize)
	for _, chunck := range chuncks {
		wg.Add(1)
		sem <- struct{}{} // get an slot
		go func(map[uint64]struct{}) {
			defer wg.Done()
			defer func() { <-sem }() // free slot
			headers, err := funcRetrieval(chunck)
			if err != nil {
				mu.Lock()
				defer mu.Unlock()
				errs = append(errs, fmt.Errorf("RetrieveBlockHeadersInBatchParallel:  %w",
					err))

				return
			}
			mu.Lock()
			defer mu.Unlock()
			for blockNumber, header := range headers {
				results[blockNumber] = header
			}
		}(chunck)
	}
	wg.Wait()

	logger.Debugf("retrieveRPCBlockHeadersInParallel: Retrieved block headers for blocks %d",
		len(blockNumbers))
	if len(errs) > 0 {
		return results, fmt.Errorf("retrieveRPCBlockHeadersInParallel: errors: %v", errs)
	}
	return results, nil
}

func splitBlockNumbersIntoChunks(blockNumbers map[uint64]struct{}, chunkSize int) []map[uint64]struct{} {
	chunks := make([]map[uint64]struct{}, 0)
	currentChunk := make(map[uint64]struct{})
	count := 0
	for bn := range blockNumbers {
		currentChunk[bn] = struct{}{}
		count++
		if count >= chunkSize {
			chunks = append(chunks, currentChunk)
			currentChunk = make(map[uint64]struct{})
			count = 0
		}
	}
	if len(currentChunk) > 0 {
		chunks = append(chunks, currentChunk)
	}
	return chunks
}
