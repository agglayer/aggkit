package migrations

import (
	"context"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

func TestMigration0001(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "lastgersyncTest0001.sqlite")

	err := RunMigrations(dbPath)
	require.NoError(t, err)
	db, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO block (num) VALUES (1);

		INSERT INTO imported_global_exit_root (
			block_num,
			global_exit_root,
			l1_info_tree_index
		) VALUES (1, '0x1', '2');
	`)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)

	var block struct {
		Num uint64 `meddler:"num"`
	}
	err = meddler.QueryRow(db, &block, `SELECT * FROM block WHERE num = 1;`)
	require.NoError(t, err)
	require.NotNil(t, block)
	require.Equal(t, uint64(1), block.Num)

	var importedGER struct {
		BlockNum        uint64 `meddler:"block_num"`
		GlobalExitRoot  string `meddler:"global_exit_root"`
		L1InfoTreeIndex uint32 `meddler:"l1_info_tree_index"`
	}
	err = meddler.QueryRow(db, &importedGER, `SELECT * FROM imported_global_exit_root`)
	require.NoError(t, err)
	require.NotNil(t, importedGER)
	require.Equal(t, uint64(1), importedGER.BlockNum)
	require.Equal(t, "0x1", importedGER.GlobalExitRoot)
	require.Equal(t, uint32(2), importedGER.L1InfoTreeIndex)
}
