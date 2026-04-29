// Package migrations embeds and runs SQL schema migrations for the dvnsyncer database.
package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed dvnsyncer0001.sql
var migration0001 string

// GetMigrations returns all dvnsyncer migrations in order.
func GetMigrations() []types.Migration {
	return []types.Migration{
		{
			ID:  "dvnsyncer0001",
			SQL: migration0001,
		},
	}
}

// RunMigrations runs all dvnsyncer migrations against the SQLite database at dbPath.
func RunMigrations(dbPath string) error {
	return db.RunMigrations(dbPath, GetMigrations())
}
