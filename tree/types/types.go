package types

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

const (
	DefaultHeight uint8 = 32
)

type Leaf struct {
	Index uint32
	Hash  common.Hash
}

type Root struct {
	Hash          common.Hash `meddler:"hash,hash"`
	Index         uint32      `meddler:"position"`
	BlockNum      uint64      `meddler:"block_num"`
	BlockPosition uint64      `meddler:"block_position"`
}

type TreeNode struct {
	Hash  common.Hash `meddler:"hash,hash"`
	Left  common.Hash `meddler:"left,hash"`
	Right common.Hash `meddler:"right,hash"`
}

type Proof [DefaultHeight]common.Hash

func (r *Root) String() string {
	return fmt.Sprintf("Root{Hash: %s, Index: %d, BlockNum: %d, BlockPosition: %d}",
		r.Hash.Hex(), r.Index, r.BlockNum, r.BlockPosition)
}
