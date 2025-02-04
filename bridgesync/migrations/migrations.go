package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
	treeMigrations "github.com/agglayer/aggkit/tree/migrations"
)

//go:embed bridgesync0001.sql
var mig001 string

//go:embed bridgesync0002.sql
var mig002 string

func RunMigrations(dbPath string) error {
	migrations := []types.Migration{
		{
			ID:  "bridgesync0001",
			SQL: mig001,
		},
		{
			ID:  "bridgesync0002",
			SQL: mig002,
		},
	}
	migrations = append(migrations, treeMigrations.Migrations...)
	return db.RunMigrations(dbPath, migrations)
}
