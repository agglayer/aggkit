package db

import (
	"path"
	"testing"

	"github.com/agglayer/aggkit/db/types"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func TestSqlite(t *testing.T) {
	logger := log.WithFields("test", "sqlite")
	path := path.Join(t.TempDir(), "base.sqlite")
	db, err := NewSQLiteDB(path)
	require.NoError(t, err)
	err = RunMigrationsDB(logger, db, []types.Migration{})
	require.NoError(t, err)
	owner := "unittest"
	kv := KeyValueStorage{db}

	// Test InsertValue and GetValue
	_, err = kv.GetValue(db, owner, "key")
	require.ErrorIs(t, err, ErrNotFound)
	err = kv.InsertValue(db, owner, "key", "value")
	require.NoError(t, err)
	value, err := kv.GetValue(db, owner, "key")
	require.NoError(t, err)
	require.Equal(t, "value", value)

	// Test ExistsKey
	exists, err := kv.ExistsKey(db, owner, "key")
	require.NoError(t, err)
	require.True(t, exists)
	exists, err = kv.ExistsKey(db, owner, "nonexistent_key")
	require.NoError(t, err)
	require.False(t, exists)
	exists, err = kv.ExistsKey(db, "some_new_owner", "nonexistent_key")
	require.NoError(t, err)
	require.False(t, exists)

	// Test UpdateValue
	err = kv.UpdateValue(db, owner, "key", "new_value")
	require.NoError(t, err)
	value, err = kv.GetValue(db, owner, "key")
	require.NoError(t, err)
	require.Equal(t, "new_value", value)
}
