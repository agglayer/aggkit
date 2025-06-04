package db

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/db/types"
	"github.com/russross/meddler"
)

// KeyValueStorage wraps a sql.DB instance to provide key-value storage functionality.
// It can be used to interact with a relational database as a key-value store.
type KeyValueStorage struct {
	*sql.DB
}

// NewKeyValueStorage creates and returns a new instance of KeyValueStorage
// using the provided sql.DB connection.
func NewKeyValueStorage(db *sql.DB) *KeyValueStorage {
	return &KeyValueStorage{db}
}

// kvRow represents a key-value pair owned by a specific user,
// including the value and the timestamp of the last update.
// Fields are tagged for use with the meddler ORM.
type kvRow struct {
	Owner string `meddler:"owner"`
	Key   string `meddler:"key"`

	Value     string `meddler:"value"`
	UpdatedAt int64  `meddler:"updated_at"`
}

// InsertValue inserts a new key-value pair into the storage for the specified owner.
// If a transaction (tx) is provided, it uses that transaction; otherwise,
// it uses the default database connection.
// The function sets the current Unix timestamp as the update time for the entry.
// Returns an error if the insertion fails.
func (kv *KeyValueStorage) InsertValue(tx types.Querier, owner, key, value string) error {
	updateAt := funcTimeNow().Unix()
	if tx == nil {
		tx = kv.DB
	}
	return meddler.Insert(tx, tableKVName, &kvRow{Owner: owner, Key: key, Value: value, UpdatedAt: updateAt})
}

// GetValue retrieves the value associated with the specified owner and key from the key-value storage.
// It uses the provided transaction (tx) for the query;
// if tx is nil, it falls back to the storage's default DB connection.
// Returns the value as a string if found,
// or an error if the key does not exist or if there is a database issue.
func (kv *KeyValueStorage) GetValue(tx types.Querier, owner, key string) (string, error) {
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

// UpdateValue inserts or updates a key-value pair for a given owner in the key-value storage.
// If a record with the specified owner and key already exists,
// its value and updated_at timestamp are updated.
// If no transaction (tx) is provided, the method uses the default database connection.
// Returns an error if the operation fails, wrapped with ReturnErrNotFound.
func (kv *KeyValueStorage) UpdateValue(tx types.Querier, owner, key, value string) error {
	if tx == nil {
		tx = kv.DB
	}

	updateAt := funcTimeNow().Unix()
	query := fmt.Sprintf(`
		INSERT INTO %s (owner, key, value, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (owner, key) DO UPDATE 
		SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at
	`, tableKVName)
	_, err := tx.Exec(query, owner, key, value, updateAt)

	return ReturnErrNotFound(err)
}
