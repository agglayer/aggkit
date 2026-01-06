package types

import (
	"context"

	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/ethereum/go-ethereum/common"
)

// ReadTreer provides read-only access to a Merkle tree.
type ReadTreer interface {
	GetRootByIndex(ctx context.Context, index uint32) (Root, error)
	GetRootByHash(ctx context.Context, hash common.Hash) (*Root, error)
	GetLastRoot(tx dbtypes.Querier) (Root, error)
	GetLeaf(tx dbtypes.Querier, index uint32, root common.Hash) (common.Hash, error)
	GetProof(ctx context.Context, index uint32, root common.Hash) (Proof, error)
}

// LeafWriter provides write access to a Merkle tree.
type LeafWriter interface {
	PutLeaf(tx dbtypes.Txer, blockNum, blockPosition uint64, leaf Leaf) (common.Hash, error)
}

// ReorganizeTreer provides reorg handling for a Merkle tree.
type ReorganizeTreer interface {
	ReadTreer
	Reorg(tx dbtypes.Txer, firstReorgedBlock uint64) error
	BackwardToIndex(ctx context.Context, tx dbtypes.Txer, targetIndex uint32) error
}

// FullTreer = fully-capable tree (read, write, reorg)
type FullTreer interface {
	ReadTreer
	LeafWriter
	ReorganizeTreer
}
