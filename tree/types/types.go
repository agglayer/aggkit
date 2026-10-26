package types

import (
	"fmt"
	"strings"

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
	// BlockHash is the hash of the block BlockNum, as stored by the syncer. It is not a column of
	// the tree, so the tree never fills it: only the syncer queries that serve a root with its block
	// hash (e.g. BridgeSync.GetRootByLER) set it, reading it in the same statement as the root.
	// Zero if that block has no hash, or when the root was read by any other query.
	BlockHash common.Hash `meddler:"-"`
}

type TreeNode struct {
	Hash  common.Hash `meddler:"hash,hash"`
	Left  common.Hash `meddler:"left,hash"`
	Right common.Hash `meddler:"right,hash"`
}

type Proof [DefaultHeight]common.Hash

// NewProof creates a proof from an array of hashes
func NewProof(proofRaw [DefaultHeight][common.HashLength]byte) Proof {
	var proof Proof
	for i, currentLeaf := range proofRaw {
		proof[i] = currentLeaf
	}
	return proof
}

func (p Proof) String() string {
	var builder strings.Builder
	builder.WriteString("Proof{[")
	for i, hash := range p {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(hash.String())
	}
	builder.WriteString("]}")
	return builder.String()
}

func (r *Root) String() string {
	return fmt.Sprintf("Root{Hash: %s, Index: %d, BlockNum: %d, BlockPosition: %d}",
		r.Hash.Hex(), r.Index, r.BlockNum, r.BlockPosition)
}
