package migrations

import (
	"database/sql"
	_ "embed"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbmigrations "github.com/agglayer/aggkit/db/migrations"
	"github.com/agglayer/aggkit/db/types"
)

// ClaimSync0001 is public because bridegsync needs it to
// set the migrations:this 0001 is equivalent to bridgesync0014,
//
//go:embed claimsync0001.sql
var ClaimSync0001 string

func GetClaimSyncMigrations() []types.Migration {
	return []types.Migration{
		{
			ID:  "claimsync0001",
			SQL: ClaimSync0001,
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
