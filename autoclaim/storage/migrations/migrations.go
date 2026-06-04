package migrations

import (
	"database/sql"
	_ "embed"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	dbmigrations "github.com/agglayer/aggkit/db/migrations"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed autoclaim0001.sql
var autoClaim0001 string

//go:embed autoclaim0002.sql
var autoClaim0002 string

// GetAutoClaimMigrations returns Auto Claim storage migrations.
func GetAutoClaimMigrations() []types.Migration {
	return []types.Migration{
		{
			ID:  "autoclaim0001",
			SQL: autoClaim0001,
		},
		{
			ID:  "autoclaim0002",
			SQL: autoClaim0002,
		},
	}
}

// GetFullMigrations returns base DB migrations followed by Auto Claim migrations.
func GetFullMigrations() []types.Migration {
	baseMigrations := dbmigrations.GetBaseMigrations()
	autoClaimMigrations := GetAutoClaimMigrations()
	combined := make([]types.Migration, 0, len(baseMigrations)+len(autoClaimMigrations))
	combined = append(combined, baseMigrations...)
	combined = append(combined, autoClaimMigrations...)
	return combined
}

// RunMigrations applies all pending Auto Claim migrations to database.
func RunMigrations(logger aggkitcommon.Logger, database *sql.DB) error {
	return db.RunMigrationsDB(logger, database, GetFullMigrations())
}
