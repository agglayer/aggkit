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

//go:embed l1infotreesync0004.sql
var mig004 string

func RunMigrations(dbPath string) error {
	migrations := make([]types.Migration, 0, 4+2*len(treeMigrations.Migrations)) //nolint:mnd
	migrations = append(migrations,
		types.Migration{ID: "l1infotreesync0001", SQL: mig001},
		types.Migration{ID: "l1infotreesync0002", SQL: mig002},
		types.Migration{ID: "l1infotreesync0003", SQL: mig003},
		types.Migration{ID: "l1infotreesync0004", SQL: mig004},
	)
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
