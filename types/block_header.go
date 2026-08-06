package types

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type BlockHeader struct {
	Number     uint64       `json:"number"`
	Hash       common.Hash  `json:"hash"`
	Time       uint64       `json:"timestamp"`
	ParentHash *common.Hash `json:"parentHash"`
	// the RequestedBlock is the original Block requested
	RequestedBlock *BlockNumberFinality
	// LogsBloom is the block header's logs bloom filter. Nil means the bloom was not provided by
	// the retrieval path, in which case completeness verification is skipped for that block.
	LogsBloom *types.Bloom
}

func (gb *BlockHeader) Brief() string {
	if gb == nil {
		return "<nil>"
	}
	return fmt.Sprintf("BlockHeader{Number: %d, Hash: %s}", gb.Number, gb.Hash.Hex())
}

func NewBlockHeader(number uint64, hash common.Hash, time uint64, parentHash *common.Hash) *BlockHeader {
	return &BlockHeader{
		Number:     number,
		Hash:       hash,
		Time:       time,
		ParentHash: parentHash,
	}
}

func NewBlockHeaderFromEthHeader(ethHeader *types.Header) *BlockHeader {
	if ethHeader == nil {
		return nil
	}
	bh := NewBlockHeader(ethHeader.Number.Uint64(),
		ethHeader.Hash(),
		ethHeader.Time,
		&ethHeader.ParentHash)
	bh.LogsBloom = &ethHeader.Bloom
	return bh
}
func (gb *BlockHeader) Empty() bool {
	return gb == nil
}

// BloomMightContainAddresses reports whether the header's logs bloom indicates that at least
// one of the given addresses may have emitted a log in this block. It returns false when the
// bloom is not available (nil), since no assertion can be made in that case. Blooms have no
// false negatives: a false result with a non-nil bloom is a guarantee that none of the
// addresses logged in this block.
func (bh *BlockHeader) BloomMightContainAddresses(addrs []common.Address) bool {
	if bh == nil || bh.LogsBloom == nil {
		return false
	}
	for _, addr := range addrs {
		if types.BloomLookup(*bh.LogsBloom, addr) {
			return true
		}
	}
	return false
}

func (gb *BlockHeader) String() string {
	if gb == nil {
		return "<nil>"
	}
	return fmt.Sprintf("BlockHeader{Number: %d, Hash: %s, Time: %d, ParentHash: %s}",
		gb.Number, gb.Hash.Hex(), gb.Time, gb.ParentHash.Hex())
}
