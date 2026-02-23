package db

import (
	"database/sql"
	"errors"
	"path"
	"testing"
	"time"

	"github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

const (
	testOwner = "test_owner"
	testKey   = "test_key"
	testValue = "test_value"
)

func TestNewKeyValueStorage(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "test.sqlite")
	db, err := NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	kv := NewKeyValueStorage(db)
	require.NotNil(t, kv)
	require.Equal(t, db, kv.DB)
}

func TestKeyValueStorage_InsertValue(t *testing.T) {
	logger := log.WithFields("test", "key_value_storage")
	dbPath := path.Join(t.TempDir(), "test.sqlite")
	db, err := NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	err = RunMigrationsDB(logger, db, []types.Migration{})
	require.NoError(t, err)

	kv := NewKeyValueStorage(db)
	owner := testOwner
	key := testKey
	value := testValue

	t.Run("Insert with tx nil", func(t *testing.T) {
		err := kv.InsertValue(nil, owner, key, value)
		require.NoError(t, err)

		// Verify the value was inserted
		retrievedValue, err := kv.GetValue(nil, owner, key)
		require.NoError(t, err)
		require.Equal(t, value, retrievedValue)
	})

	t.Run("Insert duplicate key returns error", func(t *testing.T) {
		// Try to insert the same key again
		err := kv.InsertValue(nil, owner, key, "another_value")
		require.Error(t, err)
	})

	t.Run("Insert with transaction", func(t *testing.T) {
		tx, err := db.Begin()
		require.NoError(t, err)

		newKey := "test_key_tx"
		newValue := "test_value_tx"
		err = kv.InsertValue(tx, owner, newKey, newValue)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify the value was inserted
		retrievedValue, err := kv.GetValue(nil, owner, newKey)
		require.NoError(t, err)
		require.Equal(t, newValue, retrievedValue)
	})

	t.Run("Insert sets updated_at timestamp", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		originalTimeFunc := funcTimeNow
		funcTimeNow = func() time.Time { return fixedTime }
		defer func() { funcTimeNow = originalTimeFunc }()

		testKey := "time_test_key"
		err := kv.InsertValue(nil, owner, testKey, "value")
		require.NoError(t, err)

		// Query the database directly to check the timestamp
		var data kvRow
		err = meddler.QueryRow(db, &data, "SELECT * FROM key_value WHERE owner = $1 and key = $2 LIMIT 1;",
			owner, testKey)
		require.NoError(t, err)
		require.Equal(t, fixedTime.Unix(), data.UpdatedAt)
	})
}

func TestKeyValueStorage_GetValue(t *testing.T) {
	logger := log.WithFields("test", "key_value_storage")
	dbPath := path.Join(t.TempDir(), "test.sqlite")
	db, err := NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	err = RunMigrationsDB(logger, db, []types.Migration{})
	require.NoError(t, err)

	kv := NewKeyValueStorage(db)
	owner := testOwner
	key := testKey
	value := testValue

	t.Run("Get non-existent key returns ErrNotFound", func(t *testing.T) {
		_, err := kv.GetValue(nil, owner, "non_existent_key")
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("Get existing value with tx nil", func(t *testing.T) {
		err := kv.InsertValue(nil, owner, key, value)
		require.NoError(t, err)

		retrievedValue, err := kv.GetValue(nil, owner, key)
		require.NoError(t, err)
		require.Equal(t, value, retrievedValue)
	})

	t.Run("Get value with transaction", func(t *testing.T) {
		tx, err := db.Begin()
		require.NoError(t, err)
		defer func() {
			_ = tx.Rollback()
		}()

		retrievedValue, err := kv.GetValue(tx, owner, key)
		require.NoError(t, err)
		require.Equal(t, value, retrievedValue)
	})

	t.Run("Get value returns ErrNotFound when DB returns sql.ErrNoRows", func(t *testing.T) {
		_, err := kv.GetValue(nil, owner, "missing_key")
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("Get value with nil DB returns error", func(t *testing.T) {
		kvNilDB := &KeyValueStorage{DB: nil}
		_, err := kvNilDB.GetValue(nil, owner, key)
		require.Error(t, err)
		require.Contains(t, err.Error(), "tx is nil and kv.DB is nil")
	})
}

func TestKeyValueStorage_UpdateValue(t *testing.T) {
	logger := log.WithFields("test", "key_value_storage")
	dbPath := path.Join(t.TempDir(), "test.sqlite")
	db, err := NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	err = RunMigrationsDB(logger, db, []types.Migration{})
	require.NoError(t, err)

	kv := NewKeyValueStorage(db)
	owner := testOwner
	key := testKey
	value := testValue

	t.Run("Update inserts new key-value when not exists", func(t *testing.T) {
		newKey := "update_test_key"
		err := kv.UpdateValue(nil, owner, newKey, value)
		require.NoError(t, err)

		// Verify the value was inserted
		retrievedValue, err := kv.GetValue(nil, owner, newKey)
		require.NoError(t, err)
		require.Equal(t, value, retrievedValue)
	})

	t.Run("Update updates existing key-value", func(t *testing.T) {
		// First insert
		err := kv.InsertValue(nil, owner, key, value)
		require.NoError(t, err)

		// Update the value
		newValue := "updated_value"
		err = kv.UpdateValue(nil, owner, key, newValue)
		require.NoError(t, err)

		// Verify the value was updated
		retrievedValue, err := kv.GetValue(nil, owner, key)
		require.NoError(t, err)
		require.Equal(t, newValue, retrievedValue)
	})

	t.Run("Update with transaction", func(t *testing.T) {
		tx, err := db.Begin()
		require.NoError(t, err)

		txKey := "tx_update_key"
		err = kv.UpdateValue(tx, owner, txKey, value)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify the value was updated
		retrievedValue, err := kv.GetValue(nil, owner, txKey)
		require.NoError(t, err)
		require.Equal(t, value, retrievedValue)
	})

	t.Run("Update sets updated_at timestamp", func(t *testing.T) {
		// Insert initial value with old timestamp
		oldTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
		originalTimeFunc := funcTimeNow
		funcTimeNow = func() time.Time { return oldTime }

		timeKey := "time_update_key"
		err := kv.InsertValue(nil, owner, timeKey, "old_value")
		require.NoError(t, err)

		// Update with new timestamp
		newTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		funcTimeNow = func() time.Time { return newTime }
		defer func() { funcTimeNow = originalTimeFunc }()

		err = kv.UpdateValue(nil, owner, timeKey, "new_value")
		require.NoError(t, err)

		// Query the database directly to check the timestamp
		var data kvRow
		err = meddler.QueryRow(db, &data, "SELECT * FROM key_value WHERE owner = $1 and key = $2 LIMIT 1;",
			owner, timeKey)
		require.NoError(t, err)
		require.Equal(t, newTime.Unix(), data.UpdatedAt)
		require.Equal(t, "new_value", data.Value)
	})

	t.Run("Update multiple times preserves latest value", func(t *testing.T) {
		multiKey := "multi_update_key"

		// First update (insert)
		err := kv.UpdateValue(nil, owner, multiKey, "value1")
		require.NoError(t, err)

		// Second update
		err = kv.UpdateValue(nil, owner, multiKey, "value2")
		require.NoError(t, err)

		// Third update
		err = kv.UpdateValue(nil, owner, multiKey, "value3")
		require.NoError(t, err)

		// Verify final value
		retrievedValue, err := kv.GetValue(nil, owner, multiKey)
		require.NoError(t, err)
		require.Equal(t, "value3", retrievedValue)
	})
}

func TestKeyValueStorage_TransactionRollback(t *testing.T) {
	logger := log.WithFields("test", "key_value_storage")
	dbPath := path.Join(t.TempDir(), "test.sqlite")
	db, err := NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()

	err = RunMigrationsDB(logger, db, []types.Migration{})
	require.NoError(t, err)

	kv := NewKeyValueStorage(db)
	owner := "test_owner"

	t.Run("InsertValue rollback does not persist data", func(t *testing.T) {
		tx, err := db.Begin()
		require.NoError(t, err)

		key := "rollback_insert_key"
		err = kv.InsertValue(tx, owner, key, "value")
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify the value was not persisted
		_, err = kv.GetValue(nil, owner, key)
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("UpdateValue rollback does not persist data", func(t *testing.T) {
		key := "rollback_update_key"

		// Insert initial value
		err := kv.InsertValue(nil, owner, key, "original")
		require.NoError(t, err)

		// Start transaction and update
		tx, err := db.Begin()
		require.NoError(t, err)

		err = kv.UpdateValue(tx, owner, key, "updated")
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify the value was not updated
		retrievedValue, err := kv.GetValue(nil, owner, key)
		require.NoError(t, err)
		require.Equal(t, "original", retrievedValue)
	})
}

func TestReturnErrNotFound(t *testing.T) {
	t.Run("Returns ErrNotFound for sql.ErrNoRows", func(t *testing.T) {
		err := ReturnErrNotFound(sql.ErrNoRows)
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("Returns original error for other errors", func(t *testing.T) {
		originalErr := errors.New("some other error")
		err := ReturnErrNotFound(originalErr)
		require.Equal(t, originalErr, err)
	})

	t.Run("Returns nil for nil error", func(t *testing.T) {
		err := ReturnErrNotFound(nil)
		require.NoError(t, err)
	})
}
