package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed reorgdetector0001.sql
var mig001 string

func RunMigrations(dbPath string) error {
	migrations := []types.Migration{
		{
			ID:  "reorgdetector0001",
			SQL: mig001,
		},
	}
	return db.RunMigrations(dbPath, migrations)
}
