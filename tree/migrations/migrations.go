package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed tree0001.sql
var mig001 string

var Migrations = []types.Migration{
	{
		ID:  "tree001",
		SQL: mig001,
	},
}

func RunMigrations(dbPath string) error {
	return db.RunMigrations(dbPath, Migrations)
}
