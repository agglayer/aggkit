package types

import (
	"fmt"
	"maps"
	"sort"
)

// BlockHeadersResult holds the results of block header retrieval,
// separating successful headers from failed ones.
type BlockHeadersResult struct {
	// Headers contains successfully retrieved block headers, mapped by block number
	Headers map[uint64]*BlockHeader
	// Errors contains retrieval errors, mapped by block number
	Errors map[uint64]error
}

// NewBlockHeadersResult creates a new BlockHeadersResult
func NewBlockHeadersResult() *BlockHeadersResult {
	return &BlockHeadersResult{
		Headers: make(map[uint64]*BlockHeader),
		Errors:  make(map[uint64]error),
	}
}

// Success returns true if all blocks were retrieved successfully
func (r *BlockHeadersResult) Success() bool {
	return len(r.Errors) == 0
}

// PartialSuccess returns true if at least one block was retrieved successfully
func (r *BlockHeadersResult) PartialSuccess() bool {
	return len(r.Headers) > 0
}

// GetOrderedHeaders returns headers in the order of the requested blockNumbers,
// only for blocks that were retrieved successfully
func (r *BlockHeadersResult) GetOrderedHeaders(blockNumbers []uint64) []*BlockHeader {
	result := make([]*BlockHeader, 0, len(r.Headers))
	for _, bn := range blockNumbers {
		if header, ok := r.Headers[bn]; ok {
			result = append(result, header)
		}
	}
	return result
}

// AddHeader adds a successful header to the result
func (r *BlockHeadersResult) AddHeader(blockNumber uint64, header *BlockHeader) {
	r.Headers[blockNumber] = header
}

// AddError adds an error for a specific block number
func (r *BlockHeadersResult) AddError(blockNumber uint64, err error) {
	r.Errors[blockNumber] = err
}

// Merge combines another BlockHeadersResult into this one
func (r *BlockHeadersResult) Merge(other *BlockHeadersResult) {
	maps.Copy(r.Headers, other.Headers)
	maps.Copy(r.Errors, other.Errors)
}

// AreAllErrorsNotFound returns true if all errors are "not found" errors
func (r *BlockHeadersResult) AreAllErrorsNotFound() bool {
	for _, err := range r.Errors {
		if !IsErrNotFound(err) {
			return false
		}
	}
	return true
}

// ListBlocksNumberNotFound returns the list of not-found block numbers ordered by block number
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
