package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
	treeMigrations "github.com/agglayer/aggkit/tree/migrations"
)

//go:embed bridgesync0001.sql
var mig001 string

func RunMigrations(dbPath string) error {
	migrations := []types.Migration{
		{
			ID:  "bridgesync0001",
			SQL: mig001,
		},
	}
	migrations = append(migrations, treeMigrations.Migrations...)
	return db.RunMigrations(dbPath, migrations)
}
