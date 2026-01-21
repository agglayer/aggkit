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
	return NewBlockHeader(ethHeader.Number.Uint64(),
		ethHeader.Hash(),
		ethHeader.Time,
		&ethHeader.ParentHash)
}
func (gb *BlockHeader) Empty() bool {
	return gb == nil
}

func (gb *BlockHeader) String() string {
	if gb == nil {
		return "<nil>"
	}
	return fmt.Sprintf("BlockHeader{Number: %d, Hash: %s, Time: %d, ParentHash: %s}",
		gb.Number, gb.Hash.Hex(), gb.Time, gb.ParentHash.Hex())
}
