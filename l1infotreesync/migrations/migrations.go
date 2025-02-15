package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
	treeMigrations "github.com/agglayer/aggkit/tree/migrations"
)

const (
	RollupExitTreePrefix = "rollup_exit_"
	L1InfoTreePrefix     = "l1_info_"
)

//go:embed l1infotreesync0001.sql
var mig001 string

//go:embed l1infotreesync0002.sql
var mig002 string

//go:embed l1infotreesync0003.sql
var mig003 string

func RunMigrations(dbPath string) error {
	migrations := []types.Migration{
		{
			ID:  "l1infotreesync0001",
			SQL: mig001,
		},
		{
			ID:  "l1infotreesync0002",
			SQL: mig002,
		},
		{
			ID:  "l1infotreesync0003",
			SQL: mig003,
		},
	}
	for _, tm := range treeMigrations.Migrations {
		migrations = append(migrations, types.Migration{
			ID:     tm.ID,
			SQL:    tm.SQL,
			Prefix: RollupExitTreePrefix,
		})
		migrations = append(migrations, types.Migration{
			ID:     tm.ID,
			SQL:    tm.SQL,
			Prefix: L1InfoTreePrefix,
		})
	}
	return db.RunMigrations(dbPath, migrations)
}
