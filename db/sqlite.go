package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/russross/meddler"
)

const (
	UniqueConstrain = 1555
)

var (
	ErrNotFound = errors.New("not found")
	tableKVName = "key_value"
	funcTimeNow = time.Now
)

type DBA = sql.DB

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

type KeyValueStorage struct {
	*sql.DB
}

func NewKeyValueStorage(db *sql.DB) *KeyValueStorage {
	return &KeyValueStorage{db}
}

type kvRow struct {
	Owner string `meddler:"owner"`
	Key   string `meddler:"key"`

	Value     string `meddler:"value"`
	UpdatedAt int64  `meddler:"updated_at"`
}

func (kv *KeyValueStorage) InsertValue(tx Querier, owner, key, value string) error {
	updateAt := funcTimeNow().Unix()
	if tx == nil {
		tx = kv.DB
	}
	return meddler.Insert(tx, tableKVName, &kvRow{Owner: owner, Key: key, Value: value, UpdatedAt: updateAt})
}

func (kv *KeyValueStorage) GetValue(tx Querier, owner, key string) (string, error) {
	var data kvRow
	if tx == nil {
		if kv.DB == nil {
			return "", errors.New("keyValueStorage: tx is nil and kv.DB is nil ")
		}
		tx = kv.DB
	}

	err := meddler.QueryRow(tx, &data, fmt.Sprintf("SELECT * FROM %s WHERE owner = $1 and key = $2 LIMIT 1;", tableKVName),
		owner, key)
	return data.Value, ReturnErrNotFound(err)
}

func (kv *KeyValueStorage) ExistsKey(tx Querier, owner, key string) (bool, error) {
	var count int
	if tx == nil {
		tx = kv.DB
	}
	err := tx.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE owner = ? and key = ?", tableKVName),
		owner, key).Scan(&count)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}

		return false, err
	}

	return count > 0, nil
}

func (kv *KeyValueStorage) UpdateValue(tx Querier, owner, key, value string) error {
	if tx == nil {
		tx = kv.DB
	}

	updateAt := funcTimeNow().Unix()
	_, err := tx.Exec(fmt.Sprintf("UPDATE %s SET value = $1, updated_at = $2 WHERE owner = $3 and key = $4", tableKVName),
		value, updateAt, owner, key)

	return ReturnErrNotFound(err)
}
