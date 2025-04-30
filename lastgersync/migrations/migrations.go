package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed lastgersync0001.sql
var mig001 string

//go:embed lastgersync0002.sql
var mig002 string

func RunMigrations(dbPath string) error {
	migrations := []types.Migration{
		{
			ID:  "lastgersync0001",
			SQL: mig001,
		},
		{
			ID:  "lastgersync0002",
			SQL: mig002,
		},
	}
	return db.RunMigrations(dbPath, migrations)
}
