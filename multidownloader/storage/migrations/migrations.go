package migrations

import (
	"database/sql"
	_ "embed"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed 0001.sql
var mig001 string

var Migrations = []types.Migration{
	{
		ID:  "0001",
		SQL: mig001,
	},
}

func RunMigrations(logger aggkitcommon.Logger, database *sql.DB) error {
	return db.RunMigrationsDB(logger, database, Migrations)
}
