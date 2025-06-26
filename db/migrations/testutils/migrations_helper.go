package migrationsutils

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/stretchr/testify/require"
)

type MigrationTester interface {
	// FilenameTemplateDatabase returns the template for the database filename
	// if it's empty create a new one, if not copy this to a temporary file
	FilenameTemplateDatabase() string
	// InsertData used to insert data in the affected tables of the migration that is being tested
	// data will be inserted with the schema as it was previous the migration that is being tested
	InsertDataBeforeMigrationUp(*testing.T, *sql.DB)
	// RunAssertsAfterMigrationUp this function will be called after running the migration is being tested
	// and should assert that the data inserted in the function InsertData is persisted properly
	RunAssertsAfterMigrationUp(*testing.T, *sql.DB)
	// RunAssertsAfterMigrationDown this function will be called after reverting the migration that is being tested
	// and should assert that the data inserted in the function InsertData is persisted properly
	RunAssertsAfterMigrationDown(*testing.T, *sql.DB)
}

func copyFile(src string, dst string) error {
	// Read all content of src to data, may cause OOM for a large file.
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", src, err)
	}
	// Write data to dst
	err = os.WriteFile(dst, data, 0600) //nolint:mnd
	if err != nil {
		return fmt.Errorf("failed to write file %s: %w", dst, err)
	}
	return nil
}

func TestMigration(t *testing.T, dbName string, migrationData []types.Migration,
	migrationNumber int, miter MigrationTester) {
	t.Helper()
	logger := log.WithFields("module", "migration-test-"+dbName+"-"+fmt.Sprintf("%03d", migrationNumber))
	dbPath := t.TempDir() + "/test_migration_" + dbName + "-" + fmt.Sprintf("%03d", migrationNumber) + ".sqlite"
	templateDBFilename := miter.FilenameTemplateDatabase()
	if templateDBFilename != "" {
		logger.Infof("Copying template database file %s to %s", templateDBFilename, dbPath)
		err := copyFile(templateDBFilename, dbPath)
		require.NoError(t, err, "failed to copy "+templateDBFilename+"template database file")
	}
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)

	if migrationNumber > 1 {
		logger.Infof("Running UP migration before  %d migration", migrationNumber)
		err := db.RunMigrationsDBExtended(logger, database, migrationData, migrate.Up, migrationNumber-1)
		require.NoError(t, err, "failed to run migration up %d", migrationNumber-1)
		miter.InsertDataBeforeMigrationUp(t, database)
	} else {
		t.Log("this is the first migration")
	}
	// We just run the pending migration that is the one that we want to test
	logger.Infof("Running UP migration from: %d  to next one", migrationNumber)
	err = db.RunMigrationsDBExtended(logger, database, migrationData, migrate.Up, 1)
	require.NoError(t, err, "failed to run migration up %d", migrationNumber)
	miter.RunAssertsAfterMigrationUp(t, database)
	// We downgrade to the previous miration
	logger.Infof("Running DOWN migration from: %d to previous one", migrationNumber)

	err = db.RunMigrationsDBExtended(logger, database, migrationData, migrate.Down, 1)
	require.NoError(t, err, "failed to run migration down %d", migrationNumber)
	miter.RunAssertsAfterMigrationDown(t, database)
}

func GetTableColumnNames(db *sql.DB, tableName string) ([]string, error) {
	query := "SELECT name FROM pragma_table_info(?)"
	rows, err := db.Query(query, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return columns, nil
}
