package migrations

import (
	"context"
	"database/sql"
	"testing"

	dbmigrations "github.com/agglayer/aggkit/db/migrations/testutils"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

type migrationTester004 struct{}

func (m *migrationTester004) FilenameTemplateDatabase(t *testing.T) string {
	t.Helper()
	return ""
}
func (m *migrationTester004) InsertDataBeforeMigrationUp(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO certificate_info (
			height,
			retry_count,
			certificate_id,
			status,
			previous_local_exit_root,
			new_local_exit_root,
			from_block,
			to_block,
			created_at,
			updated_at,
			signed_certificate
		) VALUES (10, 0, '0x789abc', 4, '0x123456', '0x23456', 1000, 2000, 0, 0, 'N/A');

		INSERT INTO certificate_info_history (
			height,
			retry_count,
			certificate_id,
			status,
			previous_local_exit_root,
			new_local_exit_root,
			from_block,
			to_block,
			created_at,
			updated_at,
			signed_certificate
		) VALUES (3, 2, '0x789abc', 2, '0x123456', '0x23456', 310, 520, 0, 0, 'N/A');
	`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

func (m *migrationTester004) RunAssertsAfterMigrationUp(t *testing.T, db *sql.DB) {
	t.Helper()
	var certificateInfo certificateInfoRowMigration003
	err := meddler.QueryRow(db, &certificateInfo,
		"SELECT * FROM certificate_info WHERE height = $1;", 10)
	require.NoError(t, err)
	require.Equal(t, uint64(10), certificateInfo.Height)
	require.NotNil(t, certificateInfo.L1InfoTreeLeafCount)
	require.Equal(t, uint32(0), *certificateInfo.L1InfoTreeLeafCount)
	// cert heigh=3 must be deleted
	err = meddler.QueryRow(db, &certificateInfo,
		"SELECT * FROM certificate_info WHERE height = $1;", 3)
	require.Error(t, err, "Expected certificate_info with height 3 to be deleted after migration")
}

func (m *migrationTester004) RunAssertsAfterMigrationDown(t *testing.T, db *sql.DB) {
	t.Helper()
	var certificateInfo certificateInfoRowMigration003
	err := meddler.QueryRow(db, &certificateInfo,
		"SELECT * FROM certificate_info WHERE height = $1;", 10)
	require.NoError(t, err)
	require.Equal(t, uint64(10), certificateInfo.Height)
}

func TestMigration004(t *testing.T) {
	dbmigrations.TestMigration(t, "aggsender", Migrations, 4, &migrationTester004{})
}
