package migrations

import (
	"database/sql"
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
)

//go:embed 0001.sql
var mig001 string

//go:embed 0002.sql
var mig002 string

//go:embed 0003.sql
var mig003 string

func RunMigrations(logger *log.Logger, database *sql.DB) error {
	migrations := []types.Migration{
		{
			ID:  "0001",
			SQL: mig001,
		},
		{
			ID:  "0002",
			SQL: mig002,
		},
		{
			ID:  "0003",
			SQL: mig003,
		},
	}

	return db.RunMigrationsDB(logger, database, migrations)
}
