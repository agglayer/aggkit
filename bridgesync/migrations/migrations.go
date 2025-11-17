package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
	treeMigrations "github.com/agglayer/aggkit/tree/migrations"
)

//go:embed bridgesync0001.sql
var mig0001 string

//go:embed bridgesync0002.sql
var mig0002 string

//go:embed bridgesync0003.sql
var mig0003 string

//go:embed bridgesync0004.sql
var mig0004 string

//go:embed bridgesync0005.sql
var mig0005 string

//go:embed bridgesync0006.sql
var mig0006 string

//go:embed bridgesync0007.sql
var mig0007 string

//go:embed bridgesync0008.sql
var mig0008 string

//go:embed bridgesync0009.sql
var mig0009 string

//go:embed bridgesync0010.sql
var mig0010 string

func RunMigrations(dbPath string) error {
	migrations := []types.Migration{
		{
			ID:  "bridgesync0001",
			SQL: mig0001,
		},
		{
			ID:  "bridgesync0002",
			SQL: mig0002,
		},
		{
			ID:  "bridgesync0003",
			SQL: mig0003,
		},
		{
			ID:  "bridgesync0004",
			SQL: mig0004,
		},
		{
			ID:  "bridgesync0005",
			SQL: mig0005,
		},
		{
			ID:  "bridgesync0006",
			SQL: mig0006,
		},
		{
			ID:  "bridgesync0007",
			SQL: mig0007,
		},
		{
			ID:  "bridgesync0008",
			SQL: mig0008,
		},
		{
			ID:  "bridgesync0009",
			SQL: mig0009,
		},
		{
			ID:  "bridgesync0010",
			SQL: mig0010,
		},
	}
	migrations = append(migrations, treeMigrations.Migrations...)
	return db.RunMigrations(dbPath, migrations)
}
