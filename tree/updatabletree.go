package tree

import (
	"database/sql"
	"errors"

	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// UpdatableTree is a tree that have updatable leaves, and doesn't need to have sequential inserts
type UpdatableTree struct {
	*Tree
}

// NewUpdatableTree returns an UpdatableTree
func NewUpdatableTree(db *sql.DB, dbPrefix string) *UpdatableTree {
	t := newTree(db, dbPrefix)
	ut := &UpdatableTree{
		Tree: t,
	}
	return ut
}

func (t *UpdatableTree) UpsertLeaf(tx dbtypes.Txer,
	blockNum, blockPosition uint64, leaf types.Leaf) (common.Hash, error) {
	var rootHash common.Hash
	root, err := t.getLastRootWithTx(tx)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			rootHash = t.zeroHashes[types.DefaultHeight]
		} else {
			return common.Hash{}, err
		}
	} else {
		rootHash = root.Hash
	}
	siblings, _, err := t.getSiblings(tx, leaf.Index, rootHash)
	if err != nil {
		return common.Hash{}, err
	}
	currentChildHash := leaf.Hash
	newNodes := []types.TreeNode{}
	for h := uint8(0); h < types.DefaultHeight; h++ {
		var parent types.TreeNode
		if leaf.Index&(1<<h) > 0 {
			// Add child to the right
			parent = newTreeNode(siblings[h], currentChildHash)
		} else {
			// Add child to the left
			parent = newTreeNode(currentChildHash, siblings[h])
		}
		currentChildHash = parent.Hash
		newNodes = append(newNodes, parent)
	}
	if err := t.storeRoot(tx, types.Root{
		Hash:          currentChildHash,
		Index:         leaf.Index,
		BlockNum:      blockNum,
		BlockPosition: blockPosition,
	}); err != nil {
		return common.Hash{}, err
	}
	if err := t.storeNodes(tx, newNodes); err != nil {
		return common.Hash{}, err
	}
	return currentChildHash, nil
}
