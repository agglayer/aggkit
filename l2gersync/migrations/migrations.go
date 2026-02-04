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

//go:embed l2gersync0005.sql
var mig005 string

var migrationsL2gersync []types.Migration = []types.Migration{
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
	{
		ID:  "l2gersync0005",
		SQL: mig005,
	},
}

func RunMigrations(dbPath string) error {
	return db.RunMigrations(dbPath, migrationsL2gersync)
}

func RunMigrationsDown(dbPath string, maxMigrations int) error {
	return db.RunMigrationsDown(dbPath, migrationsL2gersync, maxMigrations)
}
