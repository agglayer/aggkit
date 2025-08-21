package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed reorgdetector0001.sql
var mig001 string

//go:embed reorgdetector0002.sql
var mig002 string

func RunMigrations(dbPath string) error {
	migrations := []types.Migration{
		{
			ID:  "reorgdetector0001",
			SQL: mig001,
		},
		{
			ID:  "reorgdetector0002",
			SQL: mig002,
		},
	}
	return db.RunMigrations(dbPath, migrations)
}
