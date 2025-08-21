package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed l2gersync0001.sql
var mig001 string

//go:embed l2gersync0002.sql
var mig002 string

//go:embed l2gersync0003.sql
var mig003 string

//go:embed l2gersync0004.sql
var mig004 string

func RunMigrations(dbPath string) error {
	migrations := []types.Migration{
		{
			ID:  "l2gersync0001",
			SQL: mig001,
		},
		{
			ID:  "l2gersync0002",
			SQL: mig002,
		},
		{
			ID:  "l2gersync0003",
			SQL: mig003,
		},
		{
			ID:  "l2gersync0004",
			SQL: mig004,
		},
	}
	return db.RunMigrations(dbPath, migrations)
}
