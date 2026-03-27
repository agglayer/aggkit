package migrations

import (
	"database/sql"
	"embed"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/agglayer/aggkit/db"
	dbmigrations "github.com/agglayer/aggkit/db/migrations"
	"github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	treemigrations "github.com/agglayer/aggkit/tree/migrations"
)

var (
	//go:embed *.sql
	migrationFS embed.FS
	migrations  []types.Migration
)

func init() {
	entries, err := migrationFS.ReadDir(".")
	if err != nil {
		panic(fmt.Errorf("failed to read embedded migrations: %w", err))
	}

	for _, e := range entries {
		name := e.Name() // e.g. "bridgesync0004.sql"

		sqlBytes, err := migrationFS.ReadFile(name)
		if err != nil {
			panic(err)
		}

		id := strings.TrimSuffix(name, ".sql") // "bridgesync0004"

		migrations = append(migrations, types.Migration{
			ID:  id,
			SQL: string(sqlBytes),
		})
	}

	// Ensure deterministic canonical order
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].ID < migrations[j].ID
	})
}

func addSourceField(database *sql.DB) error {
	migrations, err := db.GetMigrationsIDsApplied(database)
	if err != nil {
		return fmt.Errorf("error getting applied migrations: %w", err)
	}
	// This code is to undo the change where bridgesync0014 drops the field
	if !slices.Contains(migrations, "bridgesync0014") {
		log.Warn("migration 'bridgesync0014' not applied, skipping addSourceField." +
			" This means that the 'source' column on 'bridge' table will not be added.")
		return nil
	}
	_, err = database.Exec("ALTER TABLE bridge ADD COLUMN source TEXT DEFAULT '';")
	if err != nil {
		// Check if the column already exists
		var count int
		checkErr := database.QueryRow("SELECT COUNT(*) FROM pragma_table_info('bridge') WHERE name='source'").Scan(&count)
		if checkErr != nil {
			return fmt.Errorf("error adding column source:%w and also checking if source column exists: %w", err, checkErr)
		}
		if count == 0 {
			return fmt.Errorf("error adding source column: %w", err)
		}
	}
	return nil
}


func GetFullMigrations() []types.Migration {
	baseMigrations := dbmigrations.GetBaseMigrations()
	total := len(baseMigrations) + len(migrations) + len(treemigrations.Migrations)

	combined := make([]types.Migration, 0, total)
	// Copy migrations

	combined = append(combined, baseMigrations...)
	combined = append(combined, migrations...)
	combined = append(combined, treemigrations.Migrations...)
	return combined
}

func RunMigrations(dbPath string) error {
	combined := GetFullMigrations()
	// Pass the copy to db.RunMigrations
	return db.RunMigrationsExtended(dbPath, combined, addSourceField)
}
func RunMigrationsDown(dbPath string, maxMigrations int) error {
	return fmt.Errorf("it's not possible to remove migrations " +
		"because tree migrations are at end of list and removing implies remove all of them " +
		"losing data")
}

// GetUpTo returns all migrations up to and including the migration with the given ID.
func GetUpTo(lastID string) []types.Migration {
	idx := sort.Search(len(migrations), func(i int) bool {
		return migrations[i].ID > lastID
	})

	out := make([]types.Migration, idx)
	copy(out, migrations[:idx])
	return out
}
