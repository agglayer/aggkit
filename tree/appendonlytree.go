package tree

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrInvalidIndex = errors.New("invalid index")
)

// AppendOnlyTree is a tree where leaves are added sequentially (by index)
type AppendOnlyTree struct {
	*Tree
	lastLeftCache [types.DefaultHeight]common.Hash
	lastIndex     int64
}

// NewAppendOnlyTree creates a AppendOnlyTree
func NewAppendOnlyTree(db *sql.DB, dbPrefix string) *AppendOnlyTree {
	t := newTree(db, dbPrefix)
	return &AppendOnlyTree{
		Tree: t,
		// -1 is used to indicate no leafs, 0 means the first leaf is added (at index 0) and so on.
		// In order to differentiate the "cache not initialised" we need any value smaller than -1
		lastIndex: -2,
	}
}

func (t *AppendOnlyTree) AddLeaf(tx dbtypes.Txer, blockNum, blockPosition uint64, leaf types.Leaf) error {
	if int64(leaf.Index) != t.lastIndex+1 {
		// rebuild cache
		if err := t.initCache(tx); err != nil {
			return err
		}
		if int64(leaf.Index) != t.lastIndex+1 {
			log.Errorf(
				"mismatched index. Expected: %d, actual: %d",
				t.lastIndex+1, leaf.Index,
			)
			return ErrInvalidIndex
		}
	}
	// Calculate new tree nodes
	currentChildHash := leaf.Hash
	newNodes := []types.TreeNode{}
	for h := uint8(0); h < types.DefaultHeight; h++ {
		var parent types.TreeNode
		if leaf.Index&(1<<h) > 0 {
			// Add child to the right
			parent = newTreeNode(t.lastLeftCache[h], currentChildHash)
		} else {
			// Add child to the left
			parent = newTreeNode(currentChildHash, t.zeroHashes[h])
			// Update cache
			t.lastLeftCache[h] = currentChildHash
		}
		currentChildHash = parent.Hash
		newNodes = append(newNodes, parent)
	}

	// store root
	if err := t.storeRoot(tx, types.Root{
		Hash:          currentChildHash,
		Index:         leaf.Index,
		BlockNum:      blockNum,
		BlockPosition: blockPosition,
	}); err != nil {
		return err
	}

	// store nodes
	if err := t.storeNodes(tx, newNodes); err != nil {
		return err
	}
	t.lastIndex++
	tx.AddRollbackCallback(func() {
		log.Debugf("decreasing index due to rollback")
		t.lastIndex--
	})
	return nil
}

func (t *AppendOnlyTree) initCache(tx dbtypes.Txer) error {
	siblings := [types.DefaultHeight]common.Hash{}
	lastRoot, err := t.getLastRootWithTx(tx)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			t.lastIndex = -1
			t.lastLeftCache = siblings
			return nil
		}
		return err
	}
	t.lastIndex = int64(lastRoot.Index)
	currentNodeHash := lastRoot.Hash
	index := t.lastIndex
	// It starts in height-1 because 0 is the level of the leafs
	for h := int(types.DefaultHeight - 1); h >= 0; h-- {
		currentNode, err := t.getRHTNode(tx, currentNodeHash)
		if err != nil {
			return fmt.Errorf(
				"error getting node %s from the RHT at height %d with root %s: %w",
				currentNodeHash.Hex(), h, lastRoot.Hash.Hex(), err,
			)
		}
		if currentNode == nil {
			return db.ErrNotFound
		}
		siblings[h] = currentNode.Left
		if index&(1<<h) > 0 {
			currentNodeHash = currentNode.Right
		} else {
			currentNodeHash = currentNode.Left
		}
	}

	// Reverse the siblings to go from leafs to root
	for i, j := 0, len(siblings)-1; i == j; i, j = i+1, j-1 {
		siblings[i], siblings[j] = siblings[j], siblings[i]
	}

	t.lastLeftCache = siblings
	return nil
}
