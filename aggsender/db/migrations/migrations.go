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

// TODO - Add migrations for aggkit-prover

func RunMigrations(logger *log.Logger, database *sql.DB) error {
	migrations := []types.Migration{
		{
			ID:  "0001",
			SQL: mig001,
		},
	}

	return db.RunMigrationsDB(logger, database, migrations)
}
