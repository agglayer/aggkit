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
	t.Run("Check when no reorg", func(t *testing.T) {
		numOfLeavesToAdd := 10
		indexToCheck := uint32(numOfLeavesToAdd - 1)

		treeDB := createTreeDBForTest(t)
		merkleTree := NewAppendOnlyTree(treeDB, "")

		putTestLeaves(t, merkleTree, treeDB, numOfLeavesToAdd, 0)

		expectedRoot, err := merkleTree.GetLastRoot(nil)
		require.NoError(t, err)

		putTestLeaves(t, merkleTree, treeDB, numOfLeavesToAdd, numOfLeavesToAdd)

		root2, err := merkleTree.GetRootByIndex(context.Background(), indexToCheck)
		require.NoError(t, err)
		require.Equal(t, expectedRoot.Hash, root2.Hash)
		require.Equal(t, expectedRoot.Index, root2.Index)
	})

	t.Run("Check after rebuild tree when reorg", func(t *testing.T) {
		numOfLeavesToAdd := 10
		indexToCheck := uint32(numOfLeavesToAdd - 1)
		treeDB := createTreeDBForTest(t)
		tree := NewAppendOnlyTree(treeDB, "")

		putTestLeaves(t, tree, treeDB, numOfLeavesToAdd, 0)

		expectedRoot, err := tree.GetLastRoot(nil)
		require.NoError(t, err)

		putTestLeaves(t, tree, treeDB, numOfLeavesToAdd, numOfLeavesToAdd)

		// reorg tree
		tx, err := db.NewTx(context.Background(), treeDB)
		require.NoError(t, err)
		require.NoError(t, tree.Reorg(tx, uint64(indexToCheck+1)))
		require.NoError(t, tx.Commit())

		// rebuild cache on adding new leaf
		tx, err = db.NewTx(context.Background(), treeDB)
		require.NoError(t, err)
		_, err = tree.PutLeaf(tx, uint64(indexToCheck+1), 0, types.Leaf{
			Index: indexToCheck + 1,
			Hash:  common.HexToHash(fmt.Sprintf("%x", indexToCheck+1)),
		})
		require.NoError(t, err)
		require.NoError(t, tx.Commit())

		root2, err := tree.GetRootByIndex(context.Background(), indexToCheck)
		require.NoError(t, err)
		require.Equal(t, expectedRoot.Hash, root2.Hash)
		require.Equal(t, expectedRoot.Index, root2.Index)
	})
}

func TestTree_PutLeaf(t *testing.T) {
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
			tree := NewAppendOnlyTree(treeDB, "")

			// Add exisiting leaves
			tx, err := db.NewTx(ctx, treeDB)
			require.NoError(t, err)
			for i, leaf := range testVector.ExistingLeaves {
				_, err = tree.PutLeaf(tx, uint64(i), 0, types.Leaf{
					Index: uint32(i),
					Hash:  common.HexToHash(leaf),
				})
				require.NoError(t, err)
			}
			require.NoError(t, tx.Commit())
			if len(testVector.ExistingLeaves) > 0 {
				root, err := tree.GetLastRoot(nil)
				require.NoError(t, err)
				require.Equal(t, common.HexToHash(testVector.CurrentRoot), root.Hash)
			}

			// Add new bridge
			tx, err = db.NewTx(ctx, treeDB)
			require.NoError(t, err)
			_, err = tree.PutLeaf(tx, uint64(len(testVector.ExistingLeaves)), 0, types.Leaf{
				Index: uint32(len(testVector.ExistingLeaves)),
				Hash:  common.HexToHash(testVector.NewLeaf.CurrentHash),
			})
			require.NoError(t, err)
			require.NoError(t, tx.Commit())

			root, err := tree.GetLastRoot(nil)
			require.NoError(t, err)
			require.Equal(t, common.HexToHash(testVector.NewRoot), root.Hash)
		})
	}
}

func TestTree_GetProof(t *testing.T) {
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
				_, err = tre.PutLeaf(tx, uint64(li), 0, types.Leaf{
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

func TestTree_GetRootByHash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	testCases := []struct {
		name           string
		setup          func(t *testing.T) *Tree
		queryHash      common.Hash
		expectedRoot   *types.Root
		expectedErrMsg string
	}{
		{
			name: "existing root found",
			setup: func(t *testing.T) *Tree {
				t.Helper()
				dbPath := path.Join(t.TempDir(), "tree_GetRootByHash_found.sqlite")
				require.NoError(t, migrations.RunMigrations(dbPath))
				treeDB, err := db.NewSQLiteDB(dbPath)
				require.NoError(t, err)
				tree := NewAppendOnlyTree(treeDB, "")
				putTestLeaves(t, tree, treeDB, 6, 0)

				return tree.Tree
			},
			queryHash: common.HexToHash("0x440213f4dff167e3f5c655fbb6a3327af3512affed50ce3c1a3f139458a8a6d1"),
			expectedRoot: &types.Root{
				Hash:          common.HexToHash("0x440213f4dff167e3f5c655fbb6a3327af3512affed50ce3c1a3f139458a8a6d1"),
				Index:         5,
				BlockNum:      5,
				BlockPosition: 0,
			},
		},
		{
			name: "root not found",
			setup: func(t *testing.T) *Tree {
				t.Helper()
				dbPath := path.Join(t.TempDir(), "tree_GetRootByHash_notfound.sqlite")
				require.NoError(t, migrations.RunMigrations(dbPath))
				treeDB, err := db.NewSQLiteDB(dbPath)
				require.NoError(t, err)

				return &Tree{
					db:        treeDB,
					rootTable: "root",
				}
			},
			queryHash:      common.HexToHash("0xdeadbeef"),
			expectedErrMsg: db.ErrNotFound.Error(),
		},
		{
			name: "database error (malformed SQL)",
			setup: func(t *testing.T) *Tree {
				t.Helper()
				dbPath := path.Join(t.TempDir(), "tree_GetRootByHash_dberr.sqlite")
				require.NoError(t, migrations.RunMigrations(dbPath))
				treeDB, err := db.NewSQLiteDB(dbPath)
				require.NoError(t, err)

				// Intentionally invalid table name to trigger SQL error
				return &Tree{
					db:        treeDB,
					rootTable: "nonexistent_table",
				}
			},
			queryHash:      common.HexToHash("0xbeef"),
			expectedErrMsg: "no such table", // part of SQLite error message
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tree := tc.setup(t)
			root, err := tree.GetRootByHash(ctx, tc.queryHash)

			if tc.expectedErrMsg != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tc.expectedErrMsg)
				require.Nil(t, root)
			} else {
				require.NoError(t, err)
				require.NotNil(t, root)
				require.Equal(t, tc.expectedRoot, root)
			}
		})
	}
}

func TestVerifyProof(t *testing.T) {
	ctx := context.Background()
	treeDB := createTreeDBForTest(t)
	tree := NewAppendOnlyTree(treeDB, "")

	numOfLeavesToAdd := 11

	// add a few leaves
	tx, err := db.NewTx(ctx, treeDB)
	require.NoError(t, err)
	for i := range numOfLeavesToAdd {
		_, err := tree.PutLeaf(tx, uint64(i), 0, types.Leaf{
			Index: uint32(i),
			Hash:  common.HexToHash(fmt.Sprintf("%x", i)),
		})
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	root, err := tree.GetLastRoot(nil)
	require.NoError(t, err)

	for i := range numOfLeavesToAdd {
		leaf := common.HexToHash(fmt.Sprintf("%x", i))
		proof, err := tree.GetProof(ctx, uint32(i), root.Hash)
		require.NoError(t, err)

		// valid proof should return nil
		require.NoError(t, VerifyProof(leaf, proof, uint32(i), root.Hash))

		// corrupted root should produce an error
		corruptedRoot := root.Hash
		corruptedRoot[0] ^= 0xFF
		require.Error(t, VerifyProof(leaf, proof, uint32(i), corruptedRoot))

		// wrong leaf should produce an error
		wrongLeaf := common.HexToHash("deadbeef")
		require.Error(t, VerifyProof(wrongLeaf, proof, uint32(i), root.Hash))

		// wrong index should produce an error
		require.Error(t, VerifyProof(leaf, proof, uint32(i+1), root.Hash))
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

func putTestLeaves(t *testing.T, tree *AppendOnlyTree, treeDB *sql.DB, numOfLeaves, from int) {
	t.Helper()

	tx, err := db.NewTx(context.Background(), treeDB)
	require.NoError(t, err)

	for i := from; i < from+numOfLeaves; i++ {
		_, err := tree.PutLeaf(tx, uint64(i), 0, types.Leaf{
			Index: uint32(i),
			Hash:  common.HexToHash(fmt.Sprintf("%x", i)),
		})
		require.NoError(t, err)
	}

	require.NoError(t, tx.Commit())
}
