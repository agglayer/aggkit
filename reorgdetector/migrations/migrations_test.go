package migrations

import (
	"context"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestMigration0001(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "reorgdetectorTest0001.sqlite")

	err := RunMigrations(dbPath)
	require.NoError(t, err)
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	ctx := context.Background()
	tx, err := database.BeginTx(ctx, nil)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO tracked_block (subscriber_id, num, hash) VALUES ('subscriber1', 1, '0xABCD');
		INSERT INTO tracked_block (subscriber_id, num, hash) VALUES ('subscriber1', 2, '0xBEEF');
		INSERT INTO tracked_block (subscriber_id, num, hash) VALUES ('subscriber2', 1, '0xCAFE');
	`)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)

	// verify the tracked blocks from db
	var count int
	err = database.QueryRow(`SELECT COUNT(*) FROM tracked_block`).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 3, count)

	var hash string
	err = database.QueryRow(`SELECT hash FROM tracked_block WHERE subscriber_id = 'subscriber1' AND num = 1`).Scan(&hash)
	require.NoError(t, err)
	require.Equal(t, "0xABCD", hash)
}

func TestMigration0002(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "reorgdetectorTest0002.sqlite")

	err := RunMigrations(dbPath)
	require.NoError(t, err)
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	ctx := context.Background()
	tx, err := database.BeginTx(ctx, nil)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO reorg_event (
			detected_at,
			from_block,
			to_block,
			subscriber_id,
			current_hash,
			tracked_hash,
			version
		) VALUES (1234567890, 100, 150, 'subscriber1', '0xCURRENT', '0xTRACKED', 'v1.0.0');
	`)
	require.NoError(t, err)
	err = tx.Commit()
	require.NoError(t, err)

	// verify the reorg event from db
	var count int
	err = database.QueryRow(`SELECT COUNT(*) FROM reorg_event`).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	var currentHash string
	err = database.QueryRow(`SELECT current_hash FROM reorg_event WHERE subscriber_id = 'subscriber1'`).Scan(&currentHash)
	require.NoError(t, err)
	require.Equal(t, "0xCURRENT", currentHash)
}
