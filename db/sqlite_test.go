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
	_, err = kv.GetValue(db, owner, "key")
	require.ErrorIs(t, err, ErrNotFound)
	err = kv.InsertValue(db, owner, "key", "value")
	require.NoError(t, err)
	value, err := kv.GetValue(db, owner, "key")
	require.NoError(t, err)
	require.Equal(t, "value", value)
}
