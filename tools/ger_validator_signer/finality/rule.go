// Package finality is the rule that decides if the L1 block that inserted a GER is final enough to sign it.
package finality

// Rule is the finality a block needs: it is final if its number is not greater than the number of the block
// of the Tag minus NumBlocks. For example, the tag latest with NumBlocks 6 accepts the blocks that are, at least,
// 6 blocks behind the head, which is the setup of the AggOracle before the validators existed.
type Rule struct {
	// Tag is the block tag: latest, safe or finalized
	Tag string
	// NumBlocks is subtracted from the number of the block of the tag
	NumBlocks uint64
}

// FinalBlock returns the number of the highest block that is final, given the number of the block of the tag.
// ok is false if the chain is shorter than NumBlocks: no block is final yet.
func (r Rule) FinalBlock(tagged uint64) (finalBlock uint64, ok bool) {
	if tagged < r.NumBlocks {
		return 0, false
	}
	return tagged - r.NumBlocks, true
}

// IsFinal returns true if the block is final, given the number of the block of the tag
func (r Rule) IsFinal(block, tagged uint64) bool {
	finalBlock, ok := r.FinalBlock(tagged)
	return ok && block <= finalBlock
}
