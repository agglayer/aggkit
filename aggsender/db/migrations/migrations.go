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

//go:embed 0003.sql
var mig003 string

//go:embed 0004.sql
var mig004 string

//go:embed 0005.sql
var mig005 string

var Migrations = []types.Migration{
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
	{
		ID:  "0004",
		SQL: mig004,
	},
	{
		ID:  "0005",
		SQL: mig005,
	},
}

func RunMigrations(logger aggkitcommon.Logger, database *sql.DB) error {
	return db.RunMigrationsDB(logger, database, Migrations)
}
