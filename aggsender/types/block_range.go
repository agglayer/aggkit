package types

import "fmt"

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
// "FromBlock: <from>, ToBlock: <to>".
func (b BlockRange) String() string {
	return fmt.Sprintf("FromBlock: %d, ToBlock: %d", b.FromBlock, b.ToBlock)
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
		return BlockRange{}
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

func getBlockMinusOne(fromBlock uint64) uint64 {
	if fromBlock > 0 {
		return fromBlock - 1
	}
	return 0
}
