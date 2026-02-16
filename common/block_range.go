package common

import (
	"context"
	"fmt"
)

var (
	BlockRangeZero = BlockRange{}
)

// BlockRange represents a range of blocks with inclusive starting (FromBlock) and ending (ToBlock) block numbers.
type BlockRange struct {
	FromBlock uint64
	ToBlock   uint64
	// isNotEmpty have a negation because creating a BlockRange{} a bool field
	// is set to false by default, so the natural name 'IsEmpty' would produce a false value by default
	isNotEmpty bool
}

// NewBlockRange creates and returns a new BlockRange with the specified fromBlock and toBlock values.
func NewBlockRange(fromBlock, toBlock uint64) BlockRange {
	return BlockRange{
		FromBlock:  fromBlock,
		ToBlock:    toBlock,
		isNotEmpty: true,
	}
}

// CountBlocks returns the total number of blocks in the BlockRange, inclusive of both FromBlock and ToBlock.
// If both FromBlock and ToBlock are zero, or if FromBlock is greater than ToBlock, it returns 0.
func (b BlockRange) CountBlocks() uint64 {
	if b.IsEmpty() {
		return 0
	}
	if b.FromBlock > b.ToBlock {
		return 0
	}
	return b.ToBlock - b.FromBlock + 1
}

// IsEmpty returns true if the BlockRange contains no blocks.
// the invalid case of FromBlock > ToBlock is also considered empty
func (b BlockRange) IsEmpty() bool {
	return !b.isNotEmpty || b.FromBlock > b.ToBlock
}

// String returns a string representation of the BlockRange in the format
// "From: <from>, To: <to>".
func (b BlockRange) String() string {
	if b.IsEmpty() {
		return "Empty"
	}
	return fmt.Sprintf("From: %d, To: %d (%d)", b.FromBlock, b.ToBlock, b.CountBlocks())
}

// Gap returns the BlockRange representing the gap between the receiver BlockRange (b)
// and another BlockRange (other). If the two ranges overlap or are adjacent (touching),
// it returns an empty BlockRange. If there is a gap, it returns the range of blocks
// strictly between b and other. The direction of the gap depends on the relative positions
// of the two ranges.
func (b BlockRange) Gap(other BlockRange) BlockRange {
	if b.IsEmpty() || other.IsEmpty() {
		return BlockRangeZero
	}
	// If they overlap or touch, return empty
	if b.ToBlock >= getBlockMinusOne(other.FromBlock) &&
		other.ToBlock >= getBlockMinusOne(b.FromBlock) {
		return BlockRangeZero
	}

	if b.ToBlock < other.FromBlock {
		return NewBlockRange(
			b.ToBlock+1,
			other.FromBlock-1,
		)
	}

	return NewBlockRange(
		other.ToBlock+1,
		getBlockMinusOne(b.FromBlock),
	)
}

// Greater returns true if the receiver BlockRange (b) is strictly greater than the other BlockRange (other).
// [ 10 - 50 ] > [ 1 - 9 ] = true
// [ 10 - 50 ] > [ 5 - 15 ] = false (overlap)
// [ 10 - 50 ] > [ 51 - 100 ] = false (not greater)
// empty > [0 - 0] = false
// [0 - 0] > empty = true
// empty > empty = false
func (b BlockRange) Greater(other BlockRange) bool {
	if b.IsEmpty() && other.IsEmpty() {
		return false
	}
	if b.IsEmpty() {
		return false
	}
	if other.IsEmpty() {
		return true
	}
	return b.FromBlock > other.ToBlock
}

func getBlockMinusOne(fromBlock uint64) uint64 {
	if fromBlock > 0 {
		return fromBlock - 1
	}
	return 0
}

// IsNextContigousBlock checks if 'next' BlockRange is exactly the next contiguous block
// so the way to use this is:  previousBlockRange.IsNextContigousBlock(nextBlockRange)
func (b BlockRange) IsNextContigousBlock(next BlockRange) bool {
	if b.IsEmpty() || next.IsEmpty() {
		return false
	}
	return b.ToBlock+1 == next.FromBlock
}

// Merge merges two BlockRanges and returns a slice of BlockRanges.
// If the two BlockRanges overlap, it returns a single BlockRange that encompasses both.
// If they do not overlap, it returns both BlockRanges in sorted order.
// If some of them is empty is ignored:
// [ 10 - 50 ] Merge [ 1 - 9 ] = [ 1 - 50 ]
// [ 10 - 50 ] Merge [ 5 - 75 ] = [ 5 - 75 ]
// [ 10 - 50 ] Merge [ 70 - 100 ] = [ 10 - 50 ], [ 70 - 100 ]
// empty Merge [ 1 - 10 ] = [ 1 - 10 ]
// [ 1 - 10 ] Merge empty = [ 1 - 10 ]
// empty Merge empty = empty
func (b BlockRange) Merge(other BlockRange) []BlockRange {
	if b.IsEmpty() {
		return []BlockRange{other}
	}
	if other.IsEmpty() {
		return []BlockRange{b}
	}
	if b.Overlaps(other) {
		// If overlaps, just extend it
		return []BlockRange{b.Extend(other)}
	}
	// If not overlaps, return both ranges sorted
	if b.FromBlock < other.FromBlock {
		return []BlockRange{b, other}
	}
	return []BlockRange{other, b}
}

// Extend merges two BlockRanges into one encompassing BlockRange.
func (b BlockRange) Extend(other BlockRange) BlockRange {
	if b.IsEmpty() {
		return other
	}
	if other.IsEmpty() {
		return b
	}
	return NewBlockRange(
		min(b.FromBlock, other.FromBlock),
		max(b.ToBlock, other.ToBlock),
	)
}

// Subtract two BlockRanges
// A----(C---D)----B -> [A-C-1] , [D+1 - B]
// A----B (C---D) -> [A-B]
// (C---D) A----B -> [A-B]
// A----B  C----D -> [A-B]
// (C---A---B---D) -> []
func (b BlockRange) Subtract(other BlockRange) []BlockRange {
	result := []BlockRange{}
	// This cover the case that b is empty or other is empty
	if !b.Overlaps(other) {
		return []BlockRange{b}
	}
	if b.FromBlock < other.FromBlock {
		result = append(result, NewBlockRange(b.FromBlock, other.FromBlock-1))
	}
	if b.ToBlock > other.ToBlock {
		result = append(result, NewBlockRange(other.ToBlock+1, b.ToBlock))
	}
	return result
}

func (b BlockRange) Cap(maxBlockNumber uint64) BlockRange {
	if b.IsEmpty() {
		return BlockRangeZero
	}
	if b.FromBlock > maxBlockNumber {
		return BlockRangeZero
	}
	return NewBlockRange(b.FromBlock, min(b.ToBlock, maxBlockNumber))
}
func (b BlockRange) Contains(other BlockRange) bool {
	if b.IsEmpty() {
		return false
	}
	return b.FromBlock <= other.FromBlock && b.ToBlock >= other.ToBlock
}

// ContainsBlockNumber returns true if the given block number is within the BlockRange (inclusive).
func (b BlockRange) ContainsBlockNumber(number uint64) bool {
	if b.IsEmpty() {
		return false
	}
	return b.FromBlock <= number && number <= b.ToBlock
}

func (b BlockRange) Overlaps(other BlockRange) bool {
	if b.IsEmpty() || other.IsEmpty() {
		return false
	}
	return b.FromBlock <= other.ToBlock && other.FromBlock <= b.ToBlock
}

func (b BlockRange) Equal(other BlockRange) bool {
	if b.IsEmpty() && other.IsEmpty() {
		return true
	}
	return b.FromBlock == other.FromBlock && b.ToBlock == other.ToBlock && b.IsEmpty() == other.IsEmpty()
}

func (b BlockRange) Intersect(other BlockRange) BlockRange {
	// If either range is empty or they don't overlap, return an empty range
	if !b.Overlaps(other) {
		return BlockRangeZero
	}
	return NewBlockRange(
		max(b.FromBlock, other.FromBlock),
		min(b.ToBlock, other.ToBlock),
	)
}

// ChunkedRangeQuery is a generic chunker for block range queries.
// T is the result type (e.g., []Unclaim, map[common.Hash]GlobalExitRootInfo, []*RemovedGER, etc.)
func ChunkedRangeQuery[T any](
	ctx context.Context,
	fromBlock, toBlock, maxRange uint64,
	fetchChunk func(ctx context.Context, from, to uint64) (T, error),
	combine func(all T, chunk T) T,
	empty T,
) (T, error) {
	if maxRange == 0 {
		return empty, fmt.Errorf("maxRange must be greater than 0")
	}

	all := empty
	for currentFrom := fromBlock; currentFrom <= toBlock; {
		currentTo := min(currentFrom+maxRange-1, toBlock)

		chunk, err := fetchChunk(ctx, currentFrom, currentTo)
		if err != nil {
			return empty, fmt.Errorf("error in chunk %d-%d: %w", currentFrom, currentTo, err)
		}

		all = combine(all, chunk)
		currentFrom = currentTo + 1
	}

	return all, nil
}

func (b BlockRange) ListBlockNumbers() []uint64 {
	if b.IsEmpty() {
		return []uint64{}
	}
	blockNumbers := make([]uint64, 0, b.CountBlocks())
	for i := b.FromBlock; i <= b.ToBlock; i++ {
		blockNumbers = append(blockNumbers, i)
	}
	return blockNumbers
}

// SplitByBlockNumber splits a BlockRange into two parts at the given block number
// The first range includes blocks from FromBlock to blockNumber (inclusive)
// The second range includes blocks from blockNumber+1 to ToBlock (inclusive)
// If blockNumber is outside the range, one of the returned ranges will be empty
func (b BlockRange) SplitByBlockNumber(blockNumber uint64) (BlockRange, BlockRange) {
	// If the original range is empty, return two empty ranges
	if b.IsEmpty() {
		return BlockRangeZero, BlockRangeZero
	}

	// If blockNumber is before FromBlock, first range is empty
	if blockNumber < b.FromBlock {
		return BlockRangeZero, b
	}

	// If blockNumber is at or after ToBlock, second range is empty
	if blockNumber >= b.ToBlock {
		return b, BlockRangeZero
	}

	// Split in the middle
	first := NewBlockRange(b.FromBlock, blockNumber)
	second := NewBlockRange(blockNumber+1, b.ToBlock)

	return first, second
}
