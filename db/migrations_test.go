package db

import (
	"path"
	"testing"

	"github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/stretchr/testify/require"
)

func TestRunMigrations(t *testing.T) {
	t.Run("successfully runs migrations with file path", func(t *testing.T) {
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "test_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS test_table;
-- +migrate Up
CREATE TABLE IF NOT EXISTS test_table (
    id INTEGER PRIMARY KEY,
    name TEXT
);`,
			},
		}

		err := RunMigrations(dbPath, migrations)
		require.NoError(t, err)

		// Verify database was created and migration was applied
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		// Check that the table exists
		var tableName string
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='test_table'").Scan(&tableName)
		require.NoError(t, err)
		require.Equal(t, "test_table", tableName)
	})

	t.Run("returns error with invalid db path", func(t *testing.T) {
		// Use an invalid path (directory that doesn't exist)
		dbPath := "/nonexistent/directory/test.sqlite"
		migrations := []types.Migration{}

		err := RunMigrations(dbPath, migrations)
		require.Error(t, err)
	})
}

func TestRunMigrationsDB(t *testing.T) {
	t.Run("successfully runs migrations on existing db connection", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "test_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS users;
-- +migrate Up
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL
);`,
			},
		}

		err = RunMigrationsDB(logger, db, migrations)
		require.NoError(t, err)

		// Verify the migration was applied
		var tableName string
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='users'").Scan(&tableName)
		require.NoError(t, err)
		require.Equal(t, "users", tableName)
	})

	t.Run("runs with empty migrations list", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		err = RunMigrationsDB(logger, db, []types.Migration{})
		require.NoError(t, err)
	})
}

func TestRunMigrationsDBExtended(t *testing.T) {
	t.Run("successfully runs migrations up with no limit", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "test_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS products;
-- +migrate Up
CREATE TABLE IF NOT EXISTS products (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL
);`,
			},
			{
				ID:     "0002",
				Prefix: "test_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS orders;
-- +migrate Up
CREATE TABLE IF NOT EXISTS orders (
    id INTEGER PRIMARY KEY,
    product_id INTEGER
);`,
			},
		}

		err = RunMigrationsDBExtended(logger, db, migrations, migrate.Up, NoLimitMigrations)
		require.NoError(t, err)

		// Verify both tables were created
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('products', 'orders')").Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 2, count)
	})

	t.Run("successfully runs migrations up with limit", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "test_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS table1;
-- +migrate Up
CREATE TABLE IF NOT EXISTS table1 (
    id INTEGER PRIMARY KEY
);`,
			},
			{
				ID:     "0002",
				Prefix: "test_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS table2;
-- +migrate Up
CREATE TABLE IF NOT EXISTS table2 (
    id INTEGER PRIMARY KEY
);`,
			},
		}

		// Run only 1 migration
		err = RunMigrationsDBExtended(logger, db, migrations, migrate.Up, 1)
		require.NoError(t, err)

		// Verify only first table was created
		var table1Exists bool
		err = db.QueryRow("SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name='table1'").Scan(&table1Exists)
		require.NoError(t, err)
		require.True(t, table1Exists)

		var table2Exists bool
		err = db.QueryRow("SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name='table2'").Scan(&table2Exists)
		require.NoError(t, err)
		require.False(t, table2Exists)
	})

	t.Run("successfully runs migrations down", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "test_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS temp_table;
-- +migrate Up
CREATE TABLE IF NOT EXISTS temp_table (
    id INTEGER PRIMARY KEY
);`,
			},
		}

		// First run migration up
		err = RunMigrationsDBExtended(logger, db, migrations, migrate.Up, NoLimitMigrations)
		require.NoError(t, err)

		// Verify table exists
		var tableExists bool
		err = db.QueryRow("SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name='temp_table'").Scan(&tableExists)
		require.NoError(t, err)
		require.True(t, tableExists)

		// Run migration down
		err = RunMigrationsDBExtended(logger, db, migrations, migrate.Down, 1)
		require.NoError(t, err)

		// Verify table was dropped
		err = db.QueryRow("SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name='temp_table'").Scan(&tableExists)
		require.NoError(t, err)
		require.False(t, tableExists)
	})

	t.Run("successfully replaces db prefix placeholder", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "prefix_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS /*dbprefix*/custom_table;
-- +migrate Up
CREATE TABLE IF NOT EXISTS /*dbprefix*/custom_table (
    id INTEGER PRIMARY KEY
);`,
			},
		}

		err = RunMigrationsDBExtended(logger, db, migrations, migrate.Up, NoLimitMigrations)
		require.NoError(t, err)

		// Verify table with prefix was created
		var tableName string
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='prefix_custom_table'").Scan(&tableName)
		require.NoError(t, err)
		require.Equal(t, "prefix_custom_table", tableName)
	})

	t.Run("handles empty prefix", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS no_prefix_table;
-- +migrate Up
CREATE TABLE IF NOT EXISTS no_prefix_table (
    id INTEGER PRIMARY KEY
);`,
			},
		}

		err = RunMigrationsDBExtended(logger, db, migrations, migrate.Up, NoLimitMigrations)
		require.NoError(t, err)

		// Verify table was created
		var tableName string
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='no_prefix_table'").Scan(&tableName)
		require.NoError(t, err)
		require.Equal(t, "no_prefix_table", tableName)
	})

	t.Run("returns error on invalid SQL", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "test_",
				SQL: `-- +migrate Down
INVALID SQL STATEMENT;
-- +migrate Up
CREATE INVALID TABLE SYNTAX;`,
			},
		}

		err = RunMigrationsDBExtended(logger, db, migrations, migrate.Up, NoLimitMigrations)
		require.Error(t, err)
	})

	t.Run("handles closed database connection", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)

		// Close the database before running migrations
		db.Close()

		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "test_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS test;
-- +migrate Up
CREATE TABLE IF NOT EXISTS test (id INTEGER);`,
			},
		}

		err = RunMigrationsDBExtended(logger, db, migrations, migrate.Up, NoLimitMigrations)
		require.Error(t, err)
	})

	t.Run("runs multiple migrations with different prefixes", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "module_a_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS module_a_data;
-- +migrate Up
CREATE TABLE IF NOT EXISTS module_a_data (
    id INTEGER PRIMARY KEY
);`,
			},
			{
				ID:     "0001",
				Prefix: "module_b_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS module_b_data;
-- +migrate Up
CREATE TABLE IF NOT EXISTS module_b_data (
    id INTEGER PRIMARY KEY
);`,
			},
		}

		err = RunMigrationsDBExtended(logger, db, migrations, migrate.Up, NoLimitMigrations)
		require.NoError(t, err)

		// Verify both tables with different prefixes were created
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('module_a_data', 'module_b_data')").Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 2, count)
	})
}

func TestMigrationConstants(t *testing.T) {
	t.Run("constants have expected values", func(t *testing.T) {
		require.Equal(t, "-- +migrate Up", UpDownSeparator)
		require.Equal(t, "/*dbprefix*/", dbPrefixReplacer)
		require.Equal(t, 0, NoLimitMigrations)
	})
}

func TestRunMigrationsWithBaseMigrations(t *testing.T) {
	t.Run("includes base migrations when no limit is set", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		customMigrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "custom_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS custom_table;
-- +migrate Up
CREATE TABLE IF NOT EXISTS custom_table (
    id INTEGER PRIMARY KEY
);`,
			},
		}

		// Run with NoLimitMigrations to include base migrations
		err = RunMigrationsDBExtended(logger, db, customMigrations, migrate.Up, NoLimitMigrations)
		require.NoError(t, err)

		// Verify both custom table and base key_value table exist
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('custom_table', 'key_value')").Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 2, count)
	})

	t.Run("excludes base migrations when limit is set", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		customMigrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "custom_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS custom_table;
-- +migrate Up
CREATE TABLE IF NOT EXISTS custom_table (
    id INTEGER PRIMARY KEY
);`,
			},
		}

		// Run with limit to exclude base migrations
		err = RunMigrationsDBExtended(logger, db, customMigrations, migrate.Up, 1)
		require.NoError(t, err)

		// Verify custom table exists
		var customExists bool
		err = db.QueryRow("SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name='custom_table'").Scan(&customExists)
		require.NoError(t, err)
		require.True(t, customExists)

		// Verify base key_value table does NOT exist (base migrations were excluded)
		var baseExists bool
		err = db.QueryRow("SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name='key_value'").Scan(&baseExists)
		require.NoError(t, err)
		require.False(t, baseExists)
	})
}

func TestMigrationIDFormatting(t *testing.T) {
	t.Run("migration ID is correctly formatted with prefix", func(t *testing.T) {
		logger := log.WithFields("test", "migrations")
		dbPath := path.Join(t.TempDir(), "test.sqlite")
		db, err := NewSQLiteDB(dbPath)
		require.NoError(t, err)
		defer db.Close()

		migrations := []types.Migration{
			{
				ID:     "0001",
				Prefix: "my_prefix_",
				SQL: `-- +migrate Down
DROP TABLE IF EXISTS test;
-- +migrate Up
CREATE TABLE IF NOT EXISTS test (id INTEGER);`,
			},
		}

		err = RunMigrationsDBExtended(logger, db, migrations, migrate.Up, NoLimitMigrations)
		require.NoError(t, err)

		// Query the migrations table to verify the ID format
		var migrationID string
		err = db.QueryRow("SELECT id FROM gorp_migrations WHERE id LIKE 'my_prefix_%'").Scan(&migrationID)
		require.NoError(t, err)
		require.Equal(t, "my_prefix_0001", migrationID)
	})
}
