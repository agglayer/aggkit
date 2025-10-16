package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const (
	UniqueConstrain = 1555
)

var (
	ErrNotFound = errors.New("not found")
	tableKVName = "key_value"
	funcTimeNow = time.Now
)

// NewSQLiteDB creates a new SQLite DB
func NewSQLiteDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", fmt.Sprintf(
		"file:%s?_txlock=immediate&_foreign_keys=on&_journal_mode=WAL&_busy_timeout=30000",
		dbPath,
	))
	if err != nil {
		return nil, err
	}
	// TODO - check if this is needed
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}
	return db, nil
}

func ReturnErrNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
