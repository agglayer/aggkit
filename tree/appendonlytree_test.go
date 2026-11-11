package tree

import (
	"context"
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func testLeaf(i int) types.Leaf {
	return types.Leaf{
		Index: uint32(i),
		Hash:  common.HexToHash(fmt.Sprintf("%x", i+1)),
	}
}

func putLeavesInTx(t *testing.T, tree *AppendOnlyTree, tx dbtypes.Txer, from, to int) {
	t.Helper()

	for i := from; i < to; i++ {
		_, err := tree.PutLeaf(tx, uint64(i), 0, testLeaf(i))
		require.NoError(t, err)
	}
}

func referenceRoot(t *testing.T, numLeaves int) types.Root {
	t.Helper()

	treeDB := createTreeDBForTest(t)
	tree := NewAppendOnlyTree(treeDB, "")
	tx, err := db.NewTx(context.Background(), treeDB)
	require.NoError(t, err)
	putLeavesInTx(t, tree, tx, 0, numLeaves)
	require.NoError(t, tx.Commit())

	root, err := tree.GetLastRoot(nil)
	require.NoError(t, err)
	return root
}

func TestAppendOnlyTree_PutLeafRollback(t *testing.T) {
	tests := []struct {
		name string
		// leaves 0..committed-1 are committed before the failing tx
		committed int
		// leaves committed..rolledBackUntil-1 are put and rolled back
		rolledBackUntil int
		// the tx is retried up to this leaf (exclusive) and committed
		finalLeaves int
	}{
		{name: "two leaves crossing a boundary", committed: 1, rolledBackUntil: 3, finalLeaves: 3},
		{name: "odd index followed by the next one", committed: 3, rolledBackUntil: 5, finalLeaves: 5},
		{name: "leaves across several levels", committed: 3, rolledBackUntil: 9, finalLeaves: 9},
		{name: "rolled back more leaves than are retried", committed: 1, rolledBackUntil: 10, finalLeaves: 4},
		{name: "retry adds more leaves than were rolled back", committed: 1, rolledBackUntil: 3, finalLeaves: 12},
		{name: "nothing committed before", committed: 0, rolledBackUntil: 7, finalLeaves: 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expected := referenceRoot(t, tc.finalLeaves)

			treeDB := createTreeDBForTest(t)
			tree := NewAppendOnlyTree(treeDB, "")

			if tc.committed > 0 {
				tx, err := db.NewTx(context.Background(), treeDB)
				require.NoError(t, err)
				putLeavesInTx(t, tree, tx, 0, tc.committed)
				require.NoError(t, tx.Commit())
			}

			tx, err := db.NewTx(context.Background(), treeDB)
			require.NoError(t, err)
			putLeavesInTx(t, tree, tx, tc.committed, tc.rolledBackUntil)
			require.NoError(t, tx.Rollback())

			tx, err = db.NewTx(context.Background(), treeDB)
			require.NoError(t, err)
			putLeavesInTx(t, tree, tx, tc.committed, tc.finalLeaves)
			require.NoError(t, tx.Commit())

			root, err := tree.GetLastRoot(nil)
			require.NoError(t, err)
			require.Equal(t, expected.Hash, root.Hash)
			require.Equal(t, expected.Index, root.Index)

			// a tree restarted from the stored data agrees with the cached one
			restarted := NewAppendOnlyTree(treeDB, "")
			tx, err = db.NewTx(context.Background(), treeDB)
			require.NoError(t, err)
			require.NoError(t, restarted.initCache(tx))
			require.NoError(t, tx.Rollback())
			require.Equal(t, restarted.lastLeftCache, tree.lastLeftCache)
			require.Equal(t, restarted.lastIndex, tree.lastIndex)
		})
	}
}

func TestAppendOnlyTree_PutLeafStoreRootFails(t *testing.T) {
	expected := referenceRoot(t, 3)
	rootAfterTwoLeaves := referenceRoot(t, 2)
	rootAfterThreeLeaves := expected

	treeDB := createTreeDBForTest(t)
	tree := NewAppendOnlyTree(treeDB, "")

	tx, err := db.NewTx(context.Background(), treeDB)
	require.NoError(t, err)
	putLeavesInTx(t, tree, tx, 0, 1)
	require.NoError(t, tx.Commit())

	// a root with the same hash as the one of leaf 2 makes storeRoot fail for that leaf
	_, err = treeDB.Exec(
		`INSERT INTO root (hash, position, block_num, block_position) VALUES ($1, $2, 0, 0)`,
		rootAfterThreeLeaves.Hash.Hex(), 100,
	)
	require.NoError(t, err)
	require.NotEqual(t, rootAfterTwoLeaves.Hash, rootAfterThreeLeaves.Hash)

	tx, err = db.NewTx(context.Background(), treeDB)
	require.NoError(t, err)
	putLeavesInTx(t, tree, tx, 1, 2)
	_, err = tree.PutLeaf(tx, 2, 0, testLeaf(2))
	require.Error(t, err)
	require.NoError(t, tx.Rollback())

	_, err = treeDB.Exec(`DELETE FROM root WHERE position = 100`)
	require.NoError(t, err)

	tx, err = db.NewTx(context.Background(), treeDB)
	require.NoError(t, err)
	putLeavesInTx(t, tree, tx, 1, 3)
	require.NoError(t, tx.Commit())

	root, err := tree.GetLastRoot(nil)
	require.NoError(t, err)
	require.Equal(t, expected.Hash, root.Hash)
}

func TestAppendOnlyTree_PutLeafFailureKeepsCache(t *testing.T) {
	treeDB := createTreeDBForTest(t)
	tree := NewAppendOnlyTree(treeDB, "")

	tx, err := db.NewTx(context.Background(), treeDB)
	require.NoError(t, err)
	putLeavesInTx(t, tree, tx, 0, 2)
	require.NoError(t, tx.Commit())

	cacheBefore := tree.lastLeftCache
	indexBefore := tree.lastIndex

	rootAfterThreeLeaves := referenceRoot(t, 3)
	_, err = treeDB.Exec(
		`INSERT INTO root (hash, position, block_num, block_position) VALUES ($1, $2, 0, 0)`,
		rootAfterThreeLeaves.Hash.Hex(), 100,
	)
	require.NoError(t, err)

	tx, err = db.NewTx(context.Background(), treeDB)
	require.NoError(t, err)
	_, err = tree.PutLeaf(tx, 2, 0, testLeaf(2))
	require.Error(t, err)
	require.NoError(t, tx.Rollback())

	require.Equal(t, cacheBefore, tree.lastLeftCache)
	require.Equal(t, indexBefore, tree.lastIndex)
}

func TestAppendOnlyTree_FailedCacheRebuildIsNotReused(t *testing.T) {
	expected := referenceRoot(t, 5)

	treeDB := createTreeDBForTest(t)
	tree := NewAppendOnlyTree(treeDB, "")

	tx, err := db.NewTx(context.Background(), treeDB)
	require.NoError(t, err)
	putLeavesInTx(t, tree, tx, 0, 3)
	require.NoError(t, tx.Commit())

	tx, err = db.NewTx(context.Background(), treeDB)
	require.NoError(t, err)
	putLeavesInTx(t, tree, tx, 3, 5)
	require.NoError(t, tx.Rollback())

	// make the cache rebuild fail halfway by hiding a node of the last root
	lastRoot, err := tree.GetLastRoot(nil)
	require.NoError(t, err)
	lowerNode, err := tree.getRHTNode(treeDB, lastRoot.Hash)
	require.NoError(t, err)
	_, err = treeDB.Exec(`UPDATE rht SET hash = $1 WHERE hash = $2`, "hidden", lowerNode.Left.Hex())
	require.NoError(t, err)

	for range 2 {
		tx, err = db.NewTx(context.Background(), treeDB)
		require.NoError(t, err)
		_, err = tree.PutLeaf(tx, 3, 0, testLeaf(3))
		require.Error(t, err)
		require.NoError(t, tx.Rollback())
	}

	_, err = treeDB.Exec(`UPDATE rht SET hash = $1 WHERE hash = $2`, lowerNode.Left.Hex(), "hidden")
	require.NoError(t, err)

	tx, err = db.NewTx(context.Background(), treeDB)
	require.NoError(t, err)
	putLeavesInTx(t, tree, tx, 3, 5)
	require.NoError(t, tx.Commit())

	root, err := tree.GetLastRoot(nil)
	require.NoError(t, err)
	require.Equal(t, expected.Hash, root.Hash)
}
