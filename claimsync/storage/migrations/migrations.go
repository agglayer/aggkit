package migrations

import (
	"database/sql"
	_ "embed"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbmigrations "github.com/agglayer/aggkit/db/migrations"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed claimsync0001.sql
var claimSync0001 string

func GetClaimSyncMigrations() []types.Migration {
	return []types.Migration{
		{
			ID:  "claimsync0001",
			SQL: claimSync0001,
		},
	}
}

func GetFullMigrations() []types.Migration {
	baseMigrations := dbmigrations.GetBaseMigrations()
	claimSyncMigrations := GetClaimSyncMigrations()
	total := len(baseMigrations) + len(claimSyncMigrations)
	combined := make([]types.Migration, 0, total)
	combined = append(combined, baseMigrations...)
	combined = append(combined, claimSyncMigrations...)
	return combined
}

// RunMigrations applies all pending migrations to the given database
func RunMigrations(logger aggkitcommon.Logger, database *sql.DB) error {
	return db.RunMigrationsDB(logger, database, GetFullMigrations())
}
