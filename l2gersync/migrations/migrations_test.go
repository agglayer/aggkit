package migrations

import (
	"context"
	"database/sql"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/migrations"
	"github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	_ "github.com/mattn/go-sqlite3"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

func TestMigration0001(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "l2gersyncTest0001.sqlite")

	migrationsTo1 := migrationsL2gersync[:1] // only first migration
	err := RunMigrationsWithList(dbPath, migrationsTo1)
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
func getKeysFromListMigrations(migs []types.Migration) []string {
	keys := make([]string, 0, len(migs))
	for _, m := range migs {
		keys = append(keys, m.ID)
	}
	return keys
}

func TestMigration0005(t *testing.T) {
	migrationsTo4 := migrationsL2gersync[:4] // migration to 'l2gersync0004' (previous to 0005)
	log.Debugf("Total migrations till 0004: %d, %+v", len(migrationsL2gersync), getKeysFromListMigrations(migrationsL2gersync))
	dbPath := path.Join(t.TempDir(), "l2gersyncTest0005.sqlite")

	err := RunMigrationsWithList(dbPath, migrationsTo4)
	require.NoError(t, err)
	testDB, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer testDB.Close()

	ctx := context.Background()
	tx, err := testDB.BeginTx(ctx, nil)
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
	testDB.Close()
	// Now execute migration 5
	migrationsTo5 := migrationsL2gersync[:5]
	log.Debugf("Total migrations including mig0005 test: %d, %+v", len(migrationsTo5), getKeysFromListMigrations(migrationsTo5))
	err = RunMigrationsWithList(dbPath, migrationsTo5)
	require.NoError(t, err)
	testDB, err = db.NewSQLiteDB(dbPath)
	require.NoError(t, err)

	var block struct {
		Num uint64 `meddler:"num"`
	}
	err = meddler.QueryRow(testDB, &block, `SELECT * FROM block WHERE num = 1;`)
	require.NoError(t, err)
	require.NotNil(t, block)
	require.Equal(t, uint64(1), block.Num)

	var importedGER struct {
		BlockNum        uint64 `meddler:"block_num"`
		BlockPos        uint64 `meddler:"block_pos"`
		GlobalExitRoot  string `meddler:"global_exit_root"`
		L1InfoTreeIndex uint32 `meddler:"l1_info_tree_index"`
	}
	err = meddler.QueryRow(testDB, &importedGER, `SELECT * FROM imported_global_exit_root_v2`)
	require.NoError(t, err)
	require.NotNil(t, importedGER)
	require.Equal(t, uint64(1), importedGER.BlockNum)
	require.Equal(t, uint64(0), importedGER.BlockPos)
	require.Equal(t, "0x1", importedGER.GlobalExitRoot)
	require.Equal(t, uint32(2), importedGER.L1InfoTreeIndex)
	testDB.Close()
	// Now execute migration down to 4
	log.Debugf("Total migration down 1 step: %d, %+v", len(migrationsTo5), getKeysFromListMigrations(migrationsTo5))
	err = RunMigrationsDown(dbPath, migrationsTo5, 1)
	require.NoError(t, err)
	testDB, err = db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer testDB.Close()
	var importedGERV1 struct {
		BlockNum        uint64 `meddler:"block_num"`
		GlobalExitRoot  string `meddler:"global_exit_root"`
		L1InfoTreeIndex uint32 `meddler:"l1_info_tree_index"`
	}
	err = meddler.QueryRow(testDB, &importedGERV1, `SELECT * FROM imported_global_exit_root`)
	require.NoError(t, err)
	require.Equal(t, uint64(1), importedGERV1.BlockNum)
	require.Equal(t, "0x1", importedGERV1.GlobalExitRoot)
	require.Equal(t, uint32(2), importedGERV1.L1InfoTreeIndex)
}

func TestMigrations_UpDown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	const totalMigrations = 4

	migs := []types.Migration{
		{
			ID:  "l2gersync0001",
			SQL: readFile(t, "l2gersync0001.sql"),
		},
		{
			ID:  "l2gersync0002",
			SQL: readFile(t, "l2gersync0002.sql"),
		},
		{
			ID:  "l2gersync0003",
			SQL: readFile(t, "l2gersync0003.sql"),
		},
	}

	// Apply migrations Up
	err := db.RunMigrations(dbPath, migs)
	require.NoError(t, err, "failed to run up migrations")

	conn, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer conn.Close()

	// Check that tables exist after Up
	tables := []string{"block", "imported_global_exit_root"}
	for _, table := range tables {
		exists := checkTableExists(t, conn, table)
		require.True(t, exists, "table %s should exist after up migration", table)
	}

	// Rollback all migrations (Down)
	n, err := rollbackMigrations(conn, migs)
	require.NoError(t, err)
	require.Equal(t, totalMigrations, n, "expected to rollback all migrations")

	// Check that tables are dropped
	for _, table := range tables {
		exists := checkTableExists(t, conn, table)
		require.False(t, exists, "table %s should not exist after down migration", table)
	}
}

// rollbackMigrations executes all down migrations
func rollbackMigrations(database *sql.DB, migs []types.Migration) (int, error) {
	memSource := &migrate.MemoryMigrationSource{Migrations: []*migrate.Migration{}}
	for _, m := range append(migs, migrations.GetBaseMigrations()...) {
		upDown := strings.Split(m.SQL, db.UpDownSeparator)
		memSource.Migrations = append(memSource.Migrations, &migrate.Migration{
			Id:   m.ID,
			Up:   []string{upDown[1]},
			Down: []string{upDown[0]},
		})
	}
	return migrate.Exec(database, "sqlite3", memSource, migrate.Down)
}

func checkTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()

	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?;", table).Scan(&name)
	if err == sql.ErrNoRows {
		return false
	}
	require.NoError(t, err)
	return name == table
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}
