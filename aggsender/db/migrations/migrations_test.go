package migrations

import (
	"context"
	"path"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func Test001(t *testing.T) {
	t.Parallel()

	dbPath := path.Join(t.TempDir(), "aggsenderTest001.sqlite")
	db, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)

	log := log.WithFields("aggsender-test", "migrations001")

	require.NoError(t, RunMigrations(log, db))

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

func Test002(t *testing.T) {
	t.Parallel()
	dbPath := path.Join(t.TempDir(), "aggsenderTest002.sqlite")
	db, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)

	log := log.WithFields("aggsender-test", "migrations002")
	require.NoError(t, RunMigrations(log, db))

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
			signed_certificate,
			aggchain_proof,
			finalized_l1_info_tree_root,
			l1_info_tree_leaf_count
		) VALUES (10, 0, '0x789abc', 4, '0x123456', '0x23456', 1000, 2000, 0, 0, 'N/A', 'proof_data',  'root_data', 5);

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
			signed_certificate,
			aggchain_proof,
			finalized_l1_info_tree_root,
			l1_info_tree_leaf_count
		) VALUES (3, 2, '0x789abc', 2, '0x123456', '0x23456', 310, 520, 0, 0, 'N/A', 'proof_data_2', 'root_data_2', 15);
	`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}
