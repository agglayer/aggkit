package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed lastgersync0001.sql
var mig001 string

func RunMigrations(dbPath string) error {
	migrations := []types.Migration{
		{
			ID:  "lastgersync0001",
			SQL: mig001,
		},
	}
	return db.RunMigrations(dbPath, migrations)
}
