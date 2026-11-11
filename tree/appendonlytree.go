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

// cacheNotInitialized is the lastIndex value that forces the cache to be rebuilt from the database
const cacheNotInitialized int64 = -2

var (
	ErrInvalidIndex                 = errors.New("invalid index")
	_               types.FullTreer = (*AppendOnlyTree)(nil)
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
		lastIndex: cacheNotInitialized,
	}
}

func (t *AppendOnlyTree) PutLeaf(tx dbtypes.Txer,
	blockNum, blockPosition uint64, leaf types.Leaf) (common.Hash, error) {
	if int64(leaf.Index) != t.lastIndex+1 {
		// rebuild cache
		if err := t.initCache(tx); err != nil {
			return common.Hash{}, err
		}
		if int64(leaf.Index) != t.lastIndex+1 {
			log.Errorf(
				"mismatched index. Expected: %d, actual: %d",
				t.lastIndex+1, leaf.Index,
			)
			return common.Hash{}, ErrInvalidIndex
		}
	}
	// Calculate new tree nodes. The cache is updated on a copy so a failure
	// before the leaf is stored leaves the tree untouched
	newLeftCache := t.lastLeftCache
	currentChildHash := leaf.Hash
	newNodes := []types.TreeNode{}
	for h := uint8(0); h < types.DefaultHeight; h++ {
		var parent types.TreeNode
		if leaf.Index&(1<<h) > 0 {
			// Add child to the right
			parent = newTreeNode(newLeftCache[h], currentChildHash)
		} else {
			// Add child to the left
			parent = newTreeNode(currentChildHash, t.zeroHashes[h])
			// Update cache
			newLeftCache[h] = currentChildHash
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
		return common.Hash{}, err
	}

	// store nodes
	if err := t.storeNodes(tx, newNodes); err != nil {
		return common.Hash{}, err
	}
	t.lastLeftCache = newLeftCache
	t.lastIndex++
	// The cache can't be partially undone leaf by leaf, so it is discarded and
	// rebuilt from the database on the next PutLeaf
	tx.AddRollbackCallback(func() {
		log.Debugf("invalidating the cache due to rollback")
		t.lastIndex = cacheNotInitialized
	})
	return currentChildHash, nil
}

// initCache initializes the lastLeftCache and lastIndex values
func (t *AppendOnlyTree) initCache(tx dbtypes.Txer) error {
	// if the rebuild fails, the cache must not be reused on the next PutLeaf
	t.lastIndex = cacheNotInitialized
	siblings := [types.DefaultHeight]common.Hash{}
	lastRoot, err := t.GetLastRoot(tx)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			t.lastIndex = -1
			t.lastLeftCache = siblings
			return nil
		}
		return err
	}
	lastIndex := int64(lastRoot.Index)
	currentNodeHash := lastRoot.Hash
	index := lastIndex
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

	// lastIndex and the cache are only updated together, once the cache was fully rebuilt
	t.lastIndex = lastIndex
	t.lastLeftCache = siblings
	return nil
}
