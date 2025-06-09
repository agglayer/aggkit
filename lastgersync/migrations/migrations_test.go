package migrations

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/migrations"
	"github.com/agglayer/aggkit/db/types"
	_ "github.com/mattn/go-sqlite3"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/stretchr/testify/require"
)

func TestMigrations_UpDown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	const totalMigrations = 3

	migs := []types.Migration{
		{
			ID:  "lastgersync0001",
			SQL: readFile(t, "lastgersync0001.sql"),
		},
		{
			ID:  "lastgersync0002",
			SQL: readFile(t, "lastgersync0002.sql"),
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
