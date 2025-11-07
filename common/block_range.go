package common

import "fmt"

var (
	BlockRangeZero = BlockRange{}
)

// BlockRange represents a range of blocks with inclusive starting (FromBlock) and ending (ToBlock) block numbers.
type BlockRange struct {
	FromBlock uint64
	ToBlock   uint64
}

// NewBlockRange creates and returns a new BlockRange with the specified fromBlock and toBlock values.
func NewBlockRange(fromBlock, toBlock uint64) BlockRange {
	return BlockRange{
		FromBlock: fromBlock,
		ToBlock:   toBlock,
	}
}

// CountBlocks returns the total number of blocks in the BlockRange, inclusive of both FromBlock and ToBlock.
// If both FromBlock and ToBlock are zero, or if FromBlock is greater than ToBlock, it returns 0.
func (b BlockRange) CountBlocks() uint64 {
	if b.FromBlock == 0 && b.ToBlock == 0 {
		return 0
	}
	if b.FromBlock > b.ToBlock {
		return 0
	}
	return b.ToBlock - b.FromBlock + 1
}

// IsEmpty returns true if the BlockRange contains no blocks.
func (b BlockRange) IsEmpty() bool {
	return b.CountBlocks() == 0
}

// String returns a string representation of the BlockRange in the format
// "From: <from>, To: <to>".
func (b BlockRange) String() string {
	return fmt.Sprintf("From: %d, To: %d (%d)", b.FromBlock, b.ToBlock, b.CountBlocks())
}

// Gap returns the BlockRange representing the gap between the receiver BlockRange (b)
// and another BlockRange (other). If the two ranges overlap or are adjacent (touching),
// it returns an empty BlockRange. If there is a gap, it returns the range of blocks
// strictly between b and other. The direction of the gap depends on the relative positions
// of the two ranges.
func (b BlockRange) Gap(other BlockRange) BlockRange {
	// If they overlap or touch, return empty
	if b.ToBlock >= getBlockMinusOne(other.FromBlock) &&
		other.ToBlock >= getBlockMinusOne(b.FromBlock) {
		return BlockRangeZero
	}

	if b.ToBlock < other.FromBlock {
		return BlockRange{
			FromBlock: b.ToBlock + 1,
			ToBlock:   other.FromBlock - 1,
		}
	}

	return BlockRange{
		FromBlock: other.ToBlock + 1,
		ToBlock:   getBlockMinusOne(b.FromBlock),
	}
}

// Greater returns true if the receiver BlockRange (b) is strictly greater than the other BlockRange (other).
// [ 10 - 50 ] > [ 1 - 9 ] = true
// [ 10 - 50 ] > [ 5 - 15 ] = false (overlap)
// [ 10 - 50 ] > [ 51 - 100 ] = false (not greater)
func (b BlockRange) Greater(other BlockRange) bool {
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
	return b.ToBlock+1 == next.FromBlock
}

// Merge merges two BlockRanges into one encompassing BlockRange.
func (b BlockRange) Merge(other BlockRange) BlockRange {
	return NewBlockRange(
		min(b.FromBlock, other.FromBlock),
		max(b.ToBlock, other.ToBlock),
	)
}

// Substract a BlockRanges
// A----(C---D)----B -> [A-C-1] , [D+1 - B]
// A----B (C---D) -> [A-B]
// (C---D) A----B -> [A-B]
// A----B  C----D -> [A-B]
// (C---A---B---D) -> []
func (b BlockRange) Substract(other BlockRange) []BlockRange {
	result := []BlockRange{}
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
	if b.FromBlock > maxBlockNumber {
		return BlockRangeZero
	}
	return NewBlockRange(b.FromBlock, min(b.ToBlock, maxBlockNumber))
}
func (b BlockRange) Contains(other BlockRange) bool {
	return b.FromBlock <= other.FromBlock && b.ToBlock >= other.ToBlock
}

func (b BlockRange) Overlaps(other BlockRange) bool {
	return b.FromBlock <= other.ToBlock && other.FromBlock <= b.ToBlock
}

func (b BlockRange) Equal(other BlockRange) bool {
	return b.FromBlock == other.FromBlock && b.ToBlock == other.ToBlock
}

func (b BlockRange) Intersect(other BlockRange) BlockRange {
	if !b.Overlaps(other) {
		return BlockRangeZero
	}
	return NewBlockRange(
		max(b.FromBlock, other.FromBlock),
		min(b.ToBlock, other.ToBlock),
	)
}
