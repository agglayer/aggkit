package migrations

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
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

//go:embed bridgesync0012.sql
var mig0012 string

func RunMigrations(dbPath string) error {
	// Pre-calculate total length
	total := len(migrations) + len(treemigrations.Migrations)

	combined := make([]types.Migration, 0, total)
	// Copy migrations
	combined = append(combined, migrations...)
	combined = append(combined, treemigrations.Migrations...)

	// Pass the copy to db.RunMigrations
	return db.RunMigrations(dbPath, combined)
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
