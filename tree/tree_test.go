package tree

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/tree/migrations"
	"github.com/agglayer/aggkit/tree/testvectors"
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestCheckExpectedRoot(t *testing.T) {
	createTreeDB := func() *sql.DB {
		dbPath := path.Join(t.TempDir(), "treeTestCheckExpectedRoot.sqlite")
		log.Debug("DB created at: ", dbPath)
		require.NoError(t, migrations.RunMigrations(dbPath))
		treeDB, err := db.NewSQLiteDB(dbPath)
		require.NoError(t, err)

		return treeDB
	}

	addLeaves := func(merkletree *AppendOnlyTree,
		treeDB *sql.DB,
		numOfLeavesToAdd, from int) {
		tx, err := db.NewTx(context.Background(), treeDB)
		require.NoError(t, err)

		for i := from; i < from+numOfLeavesToAdd; i++ {
			require.NoError(t, merkletree.AddLeaf(tx, uint64(i), 0, types.Leaf{
				Index: uint32(i),
				Hash:  common.HexToHash(fmt.Sprintf("%x", i)),
			}))
		}

		require.NoError(t, tx.Commit())
	}

	t.Run("Check when no reorg", func(t *testing.T) {
		numOfLeavesToAdd := 10
		indexToCheck := uint32(numOfLeavesToAdd - 1)

		treeDB := createTreeDB()
		merkleTree := NewAppendOnlyTree(treeDB, "")

		addLeaves(merkleTree, treeDB, numOfLeavesToAdd, 0)

		expectedRoot, err := merkleTree.GetLastRoot(nil)
		require.NoError(t, err)

		addLeaves(merkleTree, treeDB, numOfLeavesToAdd, numOfLeavesToAdd)

		root2, err := merkleTree.GetRootByIndex(context.Background(), indexToCheck)
		require.NoError(t, err)
		require.Equal(t, expectedRoot.Hash, root2.Hash)
		require.Equal(t, expectedRoot.Index, root2.Index)
	})

	t.Run("Check after rebuild tree when reorg", func(t *testing.T) {
		numOfLeavesToAdd := 10
		indexToCheck := uint32(numOfLeavesToAdd - 1)
		treeDB := createTreeDB()
		merkleTree := NewAppendOnlyTree(treeDB, "")

		addLeaves(merkleTree, treeDB, numOfLeavesToAdd, 0)

		expectedRoot, err := merkleTree.GetLastRoot(nil)
		require.NoError(t, err)

		addLeaves(merkleTree, treeDB, numOfLeavesToAdd, numOfLeavesToAdd)

		// reorg tree
		tx, err := db.NewTx(context.Background(), treeDB)
		require.NoError(t, err)
		require.NoError(t, merkleTree.Reorg(tx, uint64(indexToCheck+1)))
		require.NoError(t, tx.Commit())

		// rebuild cache on adding new leaf
		tx, err = db.NewTx(context.Background(), treeDB)
		require.NoError(t, err)
		require.NoError(t, merkleTree.AddLeaf(tx, uint64(indexToCheck+1), 0, types.Leaf{
			Index: indexToCheck + 1,
			Hash:  common.HexToHash(fmt.Sprintf("%x", indexToCheck+1)),
		}))
		require.NoError(t, tx.Commit())

		root2, err := merkleTree.GetRootByIndex(context.Background(), indexToCheck)
		require.NoError(t, err)
		require.Equal(t, expectedRoot.Hash, root2.Hash)
		require.Equal(t, expectedRoot.Index, root2.Index)
	})
}

func TestMTAddLeaf(t *testing.T) {
	data, err := os.ReadFile("testvectors/root-vectors.json")
	require.NoError(t, err)

	var mtTestVectors []testvectors.MTRootVectorRaw
	err = json.Unmarshal(data, &mtTestVectors)
	require.NoError(t, err)
	ctx := context.Background()

	for ti, testVector := range mtTestVectors {
		t.Run(fmt.Sprintf("Test vector %d", ti), func(t *testing.T) {
			dbPath := path.Join(t.TempDir(), "treeTestMTAddLeaf.sqlite")
			log.Debug("DB created at: ", dbPath)
			err := migrations.RunMigrations(dbPath)
			require.NoError(t, err)
			treeDB, err := db.NewSQLiteDB(dbPath)
			require.NoError(t, err)
			_, err = treeDB.Exec(`select * from root`)
			require.NoError(t, err)
			merkletree := NewAppendOnlyTree(treeDB, "")

			// Add exisiting leaves
			tx, err := db.NewTx(ctx, treeDB)
			require.NoError(t, err)
			for i, leaf := range testVector.ExistingLeaves {
				err = merkletree.AddLeaf(tx, uint64(i), 0, types.Leaf{
					Index: uint32(i),
					Hash:  common.HexToHash(leaf),
				})
				require.NoError(t, err)
			}
			require.NoError(t, tx.Commit())
			if len(testVector.ExistingLeaves) > 0 {
				root, err := merkletree.GetLastRoot(nil)
				require.NoError(t, err)
				require.Equal(t, common.HexToHash(testVector.CurrentRoot), root.Hash)
			}

			// Add new bridge
			tx, err = db.NewTx(ctx, treeDB)
			require.NoError(t, err)
			err = merkletree.AddLeaf(tx, uint64(len(testVector.ExistingLeaves)), 0, types.Leaf{
				Index: uint32(len(testVector.ExistingLeaves)),
				Hash:  common.HexToHash(testVector.NewLeaf.CurrentHash),
			})
			require.NoError(t, err)
			require.NoError(t, tx.Commit())

			root, err := merkletree.GetLastRoot(nil)
			require.NoError(t, err)
			require.Equal(t, common.HexToHash(testVector.NewRoot), root.Hash)
		})
	}
}

func TestMTGetProof(t *testing.T) {
	data, err := os.ReadFile("testvectors/claim-vectors.json")
	require.NoError(t, err)

	var mtTestVectors []testvectors.MTClaimVectorRaw
	err = json.Unmarshal(data, &mtTestVectors)
	require.NoError(t, err)
	ctx := context.Background()

	for ti, testVector := range mtTestVectors {
		t.Run(fmt.Sprintf("Test vector %d", ti), func(t *testing.T) {
			dbPath := path.Join(t.TempDir(), "treeTestMTGetProof.sqlite")
			err := migrations.RunMigrations(dbPath)
			require.NoError(t, err)
			treeDB, err := db.NewSQLiteDB(dbPath)
			require.NoError(t, err)
			tre := NewAppendOnlyTree(treeDB, "")

			tx, err := db.NewTx(ctx, treeDB)
			require.NoError(t, err)
			for li, leaf := range testVector.Deposits {
				err = tre.AddLeaf(tx, uint64(li), 0, types.Leaf{
					Index: uint32(li),
					Hash:  leaf.Hash(),
				})
				require.NoError(t, err)
			}
			require.NoError(t, tx.Commit())

			root, err := tre.GetLastRoot(nil)
			require.NoError(t, err)
			expectedRoot := common.HexToHash(testVector.ExpectedRoot)
			require.Equal(t, expectedRoot, root.Hash)

			proof, err := tre.GetProof(ctx, testVector.Index, expectedRoot)
			require.NoError(t, err)
			for i, sibling := range testVector.MerkleProof {
				require.Equal(t, common.HexToHash(sibling), proof[i])
			}
		})
	}
}

func createTreeDBForTest(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := path.Join(t.TempDir(), "tree_createTreeDBForTest.sqlite")
	err := migrations.RunMigrations(dbPath)
	require.NoError(t, err)
	treeDB, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	return treeDB
}
