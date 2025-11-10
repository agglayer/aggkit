package types

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

type BlockHeader struct {
	Number     uint64
	Hash       common.Hash
	Time       uint64
	ParentHash *common.Hash
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

func (gb *BlockHeader) String() string {
	if gb == nil {
		return "<nil>"
	}
	return fmt.Sprintf("BlockHeader{Number: %d, Hash: %s, Time: %d, ParentHash: %s}", gb.Number, gb.Hash.Hex(), gb.Time, gb.ParentHash.Hex())
}

func NewBlockHeaderFromEthBlockHeader(ethHeader *types.Header) *BlockHeader {
	if ethHeader == nil {
		return nil
	}
	return NewBlockHeader(ethHeader.Number.Uint64(), ethHeader.Hash(), ethHeader.Time, &ethHeader.ParentHash)
}
