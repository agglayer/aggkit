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

//go:embed 0002.sql
var mig002 string

var Migrations = []types.Migration{
	{
		ID:  "0001",
		SQL: mig001,
	},
	{
		ID:  "0002",
		SQL: mig002,
	},
}

func RunMigrations(logger aggkitcommon.Logger, database *sql.DB) error {
	return db.RunMigrationsDB(logger, database, Migrations)
}
