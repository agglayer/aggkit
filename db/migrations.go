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
	UpDownSeparator  = "-- +migrate Up"
	dbPrefixReplacer = "/*dbprefix*/"
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
	migs := &migrate.MemoryMigrationSource{Migrations: []*migrate.Migration{}}
	// Add base migrations to general ones
	fullmigrations := migrationsParam
	fullmigrations = append(fullmigrations, migrations.GetBaseMigrations()...)
	for _, m := range fullmigrations {
		prefixed := strings.ReplaceAll(m.SQL, dbPrefixReplacer, m.Prefix)
		splitted := strings.Split(prefixed, UpDownSeparator)
		migs.Migrations = append(migs.Migrations, &migrate.Migration{
			Id:   m.Prefix + m.ID,
			Up:   []string{splitted[1]},
			Down: []string{splitted[0]},
		})
	}

	logger.Debugf("running migrations:")
	for _, m := range migs.Migrations {
		logger.Debugf("%+v", m.Id)
	}
	nMigrations, err := migrate.Exec(db, "sqlite3", migs, migrate.Up)
	if err != nil {
		return fmt.Errorf("error executing migration %w", err)
	}

	logger.Infof("successfully ran %d migrations", nMigrations)
	return nil
}
