package etherman

import (
	"context"
	"fmt"
	"maps"
	"math/big"
	"sort"
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

	return &aggkittypes.BlockHeader{
		Number:     number,
		Hash:       hash,
		Time:       timeStamp,
		ParentHash: &parentHash,
	}, nil
}

// https://www.alchemy.com/docs/reference/batch-requests
const batchRequestLimitHTTP = 1000

// BlockHeadersResult contiene los resultados de la recuperación de block headers,
// separando los exitosos de los fallidos
type BlockHeadersResult struct {
	// Headers contiene los block headers recuperados exitosamente, mapeados por block number
	Headers map[uint64]*aggkittypes.BlockHeader

	// Errors contiene los errores de recuperación, mapeados por block number
	Errors map[uint64]error
}

// NewBlockHeadersResult crea un nuevo BlockHeadersResult
func NewBlockHeadersResult() *BlockHeadersResult {
	return &BlockHeadersResult{
		Headers: make(map[uint64]*aggkittypes.BlockHeader),
		Errors:  make(map[uint64]error),
	}
}

// Success retorna true si todos los bloques se recuperaron exitosamente
func (r *BlockHeadersResult) Success() bool {
	return len(r.Errors) == 0
}

// PartialSuccess retorna true si al menos un bloque se recuperó exitosamente
func (r *BlockHeadersResult) PartialSuccess() bool {
	return len(r.Headers) > 0
}

// GetOrderedHeaders retorna los headers en el orden de blockNumbers solicitados,
// solo para los bloques que se recuperaron exitosamente
func (r *BlockHeadersResult) GetOrderedHeaders(blockNumbers []uint64) []*aggkittypes.BlockHeader {
	result := make([]*aggkittypes.BlockHeader, 0, len(r.Headers))
	for _, bn := range blockNumbers {
		if header, ok := r.Headers[bn]; ok {
			result = append(result, header)
		}
	}
	return result
}

// AddHeader añade un header exitoso al resultado
func (r *BlockHeadersResult) AddHeader(blockNumber uint64, header *aggkittypes.BlockHeader) {
	r.Headers[blockNumber] = header
}

// AddError añade un error para un block number específico
func (r *BlockHeadersResult) AddError(blockNumber uint64, err error) {
	r.Errors[blockNumber] = err
}

// Merge combina otro BlockHeadersResult en este
func (r *BlockHeadersResult) Merge(other *BlockHeadersResult) {
	maps.Copy(r.Headers, other.Headers)
	maps.Copy(r.Errors, other.Errors)
}

func (r *BlockHeadersResult) AreAllErrorsNotFound() bool {
	for _, err := range r.Errors {
		if !IsErrNotFound(err) {
			return false
		}
	}
	return true
}

// ListBlocksNumberNotFound returns the list of not found block numbers in the result ordered by block number
func (r *BlockHeadersResult) ListBlocksNumberNotFound() []uint64 {
	var notFoundBlocks []uint64
	for bn, err := range r.Errors {
		if IsErrNotFound(err) {
			notFoundBlocks = append(notFoundBlocks, bn)
		}
	}
	sort.Slice(notFoundBlocks, func(i, j int) bool {
		return notFoundBlocks[i] < notFoundBlocks[j]
	})
	return notFoundBlocks
}

// ComposeError returns a single error summarizing the errors in the result, or nil if there are no errors
func (r *BlockHeadersResult) ComposeError() error {
	if len(r.Errors) == 0 {
		return nil
	}
	errResult := fmt.Errorf("RetrieveBlockHeaders errors")
	errBlockNumbers := r.ListBlocksNumberNotFound()
	for _, bn := range errBlockNumbers {
		errResult = fmt.Errorf("%w\nBlock %d: %w", errResult, bn, r.Errors[bn])
	}
	return errResult
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
func RetrieveBlockHeadersBatch(ctx context.Context,
	log aggkitcommon.Logger,
	rpcClient aggkittypes.RPCClienter,
	blockNumbers []uint64,
	maxConcurrency int) (*BlockHeadersResult, error) {
	return retrieveBlockHeadersInBatchParallel(
		ctx,
		log,
		func(ctx context.Context, blocks []uint64) (*BlockHeadersResult, error) {
			return retrieveBlockHeadersInBatch(ctx, log, rpcClient, blocks)
		}, blockNumbers, batchRequestLimitHTTP, maxConcurrency)
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
				header, err := ethClient.HeaderByNumber(ctx, big.NewInt(int64(blockNumber)))
				if err != nil {
					result.AddError(blockNumber, fmt.Errorf("cannot get block header: %w", err))
					continue
				}
				result.AddHeader(blockNumber, aggkittypes.NewBlockHeaderFromEthHeader(header))
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
