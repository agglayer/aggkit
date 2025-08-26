package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed tree0001.sql
var mig001 string

//go:embed tree0002.sql
var mig002 string

var Migrations = []types.Migration{
	{
		ID:  "tree001",
		SQL: mig001,
	},
	{
		ID:  "tree002",
		SQL: mig002,
	},
}

func RunMigrations(dbPath string) error {
	return db.RunMigrations(dbPath, Migrations)
}
