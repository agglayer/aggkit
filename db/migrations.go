package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db/migrations"
	"github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	_ "github.com/mattn/go-sqlite3"
	migrate "github.com/rubenv/sql-migrate"
)

const (
	UpDownSeparator   = "-- +migrate Up"
	dbPrefixReplacer  = "/*dbprefix*/"
	NoLimitMigrations = 0 // indicate that there is no limit on the number of migrations to run
)

// RunMigrations will execute pending migrations if needed to keep
// the database updated with the latest changes in either direction,
// up or down.
func RunMigrations(dbPath string, migrations []types.Migration) error {
	return RunMigrationsExtended(dbPath, migrations, nil)
}

// RunMigrationsExtended is an extended version of RunMigrations that allows
// to execute an idempotent function after running the migrations, which can be useful to
// execute some extra SQL statements that are not included in the migrations or to do some checks.
func RunMigrationsExtended(dbPath string, migrations []types.Migration,
	idempotentFunc func(*sql.DB) error) error {
	start := time.Now()
	db, err := NewSQLiteDB(dbPath)
	if err != nil {
		return fmt.Errorf("error creating DB %s: %w", dbPath, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.GetDefaultLogger().Errorf("error closing DB %s: %v", dbPath, err)
		}
	}()
	if err = RunMigrationsDBExtended(log.GetDefaultLogger(), db, migrations,
		idempotentFunc, migrate.Up, NoLimitMigrations); err != nil {
		return fmt.Errorf("error migrating DB %s: %w", dbPath, err)
	}

	log.GetDefaultLogger().Infof("migrations for DB %s completed in %s", dbPath, time.Since(start))
	return nil
}

// GetMigrationsIDsApplied returns the list of migration IDs that have been applied to the database,
// ordered by application time. It is useful to know which migrations have been applied,
// e.g. before executing migrations or after a down migration.
func GetMigrationsIDsApplied(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT id, applied_at FROM gorp_migrations ORDER BY applied_at")
	if err != nil {
		return nil, fmt.Errorf("error querying applied migrations: %w", err)
	}
	defer rows.Close()

	var appliedMigrations []string
	for rows.Next() {
		var id string
		var appliedAt string
		if err := rows.Scan(&id, &appliedAt); err != nil {
			return nil, fmt.Errorf("error scanning applied migration row: %w", err)
		}
		appliedMigrations = append(appliedMigrations, id)
	}
	return appliedMigrations, nil
}

func RunMigrationsDown(dbPath string, migrations []types.Migration,
	idempotentFunc func(*sql.DB) error, maxMigrations int) error {
	db, err := NewSQLiteDB(dbPath)
	if err != nil {
		return fmt.Errorf("error creating DB %s: %w", dbPath, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.GetDefaultLogger().Errorf("error closing DB %s: %v", dbPath, err)
		}
	}()
	return RunMigrationsDBExtended(log.GetDefaultLogger(), db, migrations, idempotentFunc, migrate.Down, maxMigrations)
}

func RunMigrationsDB(logger aggkitcommon.Logger, db *sql.DB, migrationsParam []types.Migration) error {
	return RunMigrationsDBExtended(logger, db, migrationsParam, nil, migrate.Up, NoLimitMigrations)
}

// RunMigrationsDBExtended is an extended version of RunMigrationsDB that allows
// dir: can be migrate.Up or migrate.Down
// maxMigrations: Will apply at most `max` migrations. Pass 0 for no limit (or use Exec)
func RunMigrationsDBExtended(logger aggkitcommon.Logger,
	db *sql.DB,
	migrationsParam []types.Migration,
	idempotentFunc func(*sql.DB) error,
	dir migrate.MigrationDirection,
	maxMigrations int) error {
	migs := &migrate.MemoryMigrationSource{Migrations: []*migrate.Migration{}}
	fullmigrations := migrationsParam
	// In case of partial execution we ignore the base migrations
	if maxMigrations == NoLimitMigrations {
		baseMigrations := migrations.GetBaseMigrations()
		found := false
		// If the base migration is included we skip adding twice
		for _, m := range migrationsParam {
			if m.ID == baseMigrations[0].ID {
				found = true
				break
			}
		}
		if !found {
			fullmigrations = append(fullmigrations, baseMigrations...)
		}
	} else {
		migrate.SetIgnoreUnknown(true)
	}
	for _, m := range fullmigrations {
		prefixed := strings.ReplaceAll(m.SQL, dbPrefixReplacer, m.Prefix)
		splitted := strings.Split(prefixed, UpDownSeparator)
		migs.Migrations = append(migs.Migrations, &migrate.Migration{
			Id:   m.Prefix + m.ID,
			Up:   []string{splitted[1]},
			Down: []string{splitted[0]},
		})
	}

	var listMigrations strings.Builder
	for _, m := range migs.Migrations {
		listMigrations.WriteString(m.Id + ", ")
	}

	logger.Debugf("running migrations: (max %d/%d) migrations: %s", maxMigrations,
		len(migs.Migrations),
		listMigrations.String())
	nMigrations, err := migrate.ExecMax(db, "sqlite3", migs, dir, maxMigrations)

	if err != nil {
		return fmt.Errorf("error executing migration (max %d/%d) migrations: %s . Err: %w",
			maxMigrations, len(migs.Migrations), listMigrations.String(), err)
	}

	if idempotentFunc != nil {
		// Get migrations in DB
		logger.Debugf("running migrations idempotent function on DB")
		if err := idempotentFunc(db); err != nil {
			return fmt.Errorf("error executing idempotent function on DB: %w", err)
		}
	}

	logger.Infof("successfully ran %d migrations from migrations: %s", nMigrations, listMigrations.String())
	return nil
}
