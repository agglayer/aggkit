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
	return sql.Open("sqlite3", fmt.Sprintf("file:%s?_txlock=exclusive&_foreign_keys=on&_journal_mode=WAL", dbPath))
}

func ReturnErrNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
