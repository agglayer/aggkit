package sync

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

type EVMBlocks []*EVMBlock

func (e EVMBlocks) Len() int {
	return len(e)
}

func (e EVMBlocks) LastBlock() *EVMBlock {
	if len(e) == 0 {
		return nil
	}
	return e[len(e)-1]
}

type EVMBlock struct {
	EVMBlockHeader
	IsFinalizedBlock bool
	Events           []interface{}
}

func (e *EVMBlock) Brief() string {
	if e == nil {
		return "EVMBlock<nil>"
	}
	return fmt.Sprintf("EVMBlock{Num: %d, IsFinalizedBlock: %t, EventsCount: %d}",
		e.Num, e.IsFinalizedBlock, len(e.Events))
}

type EVMBlockHeader struct {
	Num        uint64
	Hash       common.Hash
	ParentHash common.Hash
	Timestamp  uint64
}
