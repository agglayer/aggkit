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

//go:embed bridgetracker0003.sql
var mig003 string

//go:embed bridgetracker0004.sql
var mig004 string

//go:embed bridgetracker0005.sql
var mig005 string

//go:embed bridgetracker0006.sql
var mig006 string

// all lists every bridgetracker migration, in the order they apply
func all() []types.Migration {
	return []types.Migration{
		{ID: "bridgetracker0001", SQL: mig001},
		{ID: "bridgetracker0002", SQL: mig002},
		{ID: "bridgetracker0003", SQL: mig003},
		{ID: "bridgetracker0004", SQL: mig004},
		{ID: "bridgetracker0005", SQL: mig005},
		{ID: "bridgetracker0006", SQL: mig006},
	}
}

// RunMigrations applies every bridgetracker migration to dbPath, creating the DB file first if
// it does not exist yet. Shared by every store in this package (sqliteRegistry,
// sqliteActivityStore): each may open its own connection to the same dbPath, but all migrations
// are always applied together, so either one can be constructed first without leaving the
// other's tables missing.
func RunMigrations(dbPath string) error {
	return db.RunMigrations(dbPath, all())
}

// GetUpTo returns the bridgetracker migrations up to and including lastID, in order: for tests
// that need a DB at an older schema, to check a later migration carries its rows over
func GetUpTo(lastID string) []types.Migration {
	migrations := all()
	for i, m := range migrations {
		if m.ID == lastID {
			return migrations[:i+1]
		}
	}
	return migrations
}
