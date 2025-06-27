package db

import (
	"database/sql"
	"fmt"
	"strings"

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
	db, err := NewSQLiteDB(dbPath)
	if err != nil {
		return fmt.Errorf("error creating DB %w", err)
	}
	return RunMigrationsDB(log.GetDefaultLogger(), db, migrations)
}

func RunMigrationsDB(logger *log.Logger, db *sql.DB, migrationsParam []types.Migration) error {
	return RunMigrationsDBExtendedFull(logger, db, migrationsParam, migrate.Up, NoLimitMigrations)
}

// RunMigrationsDBExtended is an extended version of RunMigrationsDB that allows
// dir: can be migrate.Up or migrate.Down
// maxMigrations: Will apply at most `max` migrations. Pass 0 for no limit (or use Exec)
func RunMigrationsDBExtendedFull(logger *log.Logger,
	db *sql.DB,
	migrationsParam []types.Migration,
	dir migrate.MigrationDirection,
	maxMigrations int) error {
	if err := RunMigrationsDBExtended(logger, db, migrationsParam, dir, maxMigrations); err != nil {
		return fmt.Errorf("error running migrations %w", err)
	}
	// Ignore previous migration when apply this ones
	migrate.SetIgnoreUnknown(true)
	if err := RunMigrationsDBExtended(logger, db, migrations.GetBaseMigrations(), dir, NoLimitMigrations); err != nil {
		return fmt.Errorf("error running base migrations %w", err)
	}
	return nil
}

func RunMigrationsDBExtended(logger *log.Logger,
	db *sql.DB,
	migrationsParam []types.Migration,
	dir migrate.MigrationDirection,
	maxMigrations int) error {
	migs := &migrate.MemoryMigrationSource{Migrations: []*migrate.Migration{}}
	fullmigrations := migrationsParam
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

	logger.Debugf("running migrations: (max %d) migrations: %s", maxMigrations, listMigrations.String())
	nMigrations, err := migrate.ExecMax(db, "sqlite3", migs, dir, maxMigrations)

	if err != nil {
		return fmt.Errorf("error executing migration %w", err)
	}

	logger.Infof("successfully ran %d migrations from migrations: %s", nMigrations, listMigrations.String())
	return nil
}
