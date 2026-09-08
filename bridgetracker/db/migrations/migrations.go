// Package migrations holds the bridge tracker's own SQLite migrations (see bridgetracker's
// sqliteRegistry), following the same //go:embed + db.RunMigrations pattern as
// reorgdetector/migrations.
package migrations

import (
	_ "embed"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/db/types"
)

//go:embed bridgetracker0001.sql
var mig001 string

//go:embed bridgetracker0002.sql
var mig002 string

// RunMigrations applies every bridgetracker migration to dbPath, creating the DB file first if
// it does not exist yet. Shared by every store in this package (sqliteRegistry,
// sqliteActivityStore): each may open its own connection to the same dbPath, but all migrations
// are always applied together, so either one can be constructed first without leaving the
// other's tables missing.
func RunMigrations(dbPath string) error {
	migrations := []types.Migration{
		{
			ID:  "bridgetracker0001",
			SQL: mig001,
		},
		{
			ID:  "bridgetracker0002",
			SQL: mig002,
		},
	}
	return db.RunMigrations(dbPath, migrations)
}
