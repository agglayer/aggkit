package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/db"
	logger "github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), t.Name()+".sqlite")
	lg := logger.WithFields("module", "test")

	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)

	err = RunMigrations(lg, database)
	require.NoError(t, err)

	t.Cleanup(func() { database.Close() })
	return database
}

func TestMigration0001_TablesExist(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	tx, err := database.BeginTx(ctx, nil)
	require.NoError(t, err)

	// Insert into all four tables created by claimsync0001
	_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES (1, '0xBLOCK1')`)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO claim (
			block_num, block_pos, global_index,
			origin_network, origin_address, destination_address,
			amount, proof_local_exit_root, proof_rollup_exit_root,
			mainnet_exit_root, rollup_exit_root, global_exit_root,
			destination_network, metadata, is_message, tx_hash, block_timestamp, type
		) VALUES (1, 0, '100', 1, '0xORIGIN', '0xDEST', '50',
			'0x00', '0x00', '0x01', '0x02', '0x03',
			2, NULL, FALSE, '0xTXHASH', 1000, 'ClaimEvent')
	`)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO unset_claim (
			block_num, block_pos, tx_hash, global_index, unset_global_index_hash_chain
		) VALUES (1, 1, '0xTXU', '200', '0xCHAIN')
	`)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO set_claim (
			block_num, block_pos, tx_hash, global_index
		) VALUES (1, 2, '0xTXS', '300')
	`)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	// Verify all four tables have the expected row counts
	var count int
	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM block`).Scan(&count))
	require.Equal(t, 1, count)

	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim`).Scan(&count))
	require.Equal(t, 1, count)

	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM unset_claim`).Scan(&count))
	require.Equal(t, 1, count)

	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM set_claim`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestMigration0001_ForeignKeyConstraint(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	// Inserting a claim without a matching block should fail
	_, err := database.ExecContext(ctx, `
		INSERT INTO claim (
			block_num, block_pos, global_index,
			origin_network, origin_address, destination_address,
			amount, proof_local_exit_root, proof_rollup_exit_root,
			mainnet_exit_root, rollup_exit_root, global_exit_root,
			destination_network, metadata, is_message, tx_hash, block_timestamp, type
		) VALUES (999, 0, '1', 1, '0x0', '0x0', '0', '0x0', '0x0', '0x0', '0x0', '0x0',
			1, NULL, FALSE, '0x0', 0, 'ClaimEvent')
	`)
	require.Error(t, err, "inserting claim with non-existent block_num should fail")
}

func TestMigration0001_PrimaryKeyConstraint(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	tx, err := database.BeginTx(ctx, nil)
	require.NoError(t, err)

	_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES (1, '0xBLOCK1')`)
	require.NoError(t, err)

	insertClaim := `
		INSERT INTO claim (
			block_num, block_pos, global_index,
			origin_network, origin_address, destination_address,
			amount, proof_local_exit_root, proof_rollup_exit_root,
			mainnet_exit_root, rollup_exit_root, global_exit_root,
			destination_network, metadata, is_message, tx_hash, block_timestamp, type
		) VALUES (1, 0, '1', 1, '0x0', '0x0', '0', '0x0', '0x0', '0x0', '0x0', '0x0',
			1, NULL, FALSE, '0x0', 0, 'ClaimEvent')`

	_, err = tx.Exec(insertClaim)
	require.NoError(t, err)

	// Inserting the same (block_num, block_pos) again should fail
	_, err = tx.Exec(insertClaim)
	require.Error(t, err, "duplicate primary key (block_num, block_pos) in claim should fail")

	require.NoError(t, tx.Rollback())
}

func TestMigration0001_CascadeDelete(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	tx, err := database.BeginTx(ctx, nil)
	require.NoError(t, err)

	_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES (1, '0xBLOCK1')`)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO claim (
			block_num, block_pos, global_index,
			origin_network, origin_address, destination_address,
			amount, proof_local_exit_root, proof_rollup_exit_root,
			mainnet_exit_root, rollup_exit_root, global_exit_root,
			destination_network, metadata, is_message, tx_hash, block_timestamp, type
		) VALUES (1, 0, '100', 1, '0x0', '0x0', '0', '0x0', '0x0', '0x0', '0x0', '0x0',
			1, NULL, FALSE, '0x0', 0, 'ClaimEvent')
	`)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO unset_claim (block_num, block_pos, tx_hash, global_index, unset_global_index_hash_chain)
		VALUES (1, 1, '0xTXU', '200', '0xCHAIN')
	`)
	require.NoError(t, err)

	_, err = tx.Exec(`
		INSERT INTO set_claim (block_num, block_pos, tx_hash, global_index)
		VALUES (1, 2, '0xTXS', '300')
	`)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	// Delete the block — claims, unset_claims and set_claims should cascade
	_, err = database.ExecContext(ctx, `DELETE FROM block WHERE num = 1`)
	require.NoError(t, err)

	var count int
	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim`).Scan(&count))
	require.Equal(t, 0, count, "claims should be deleted on block cascade")

	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM unset_claim`).Scan(&count))
	require.Equal(t, 0, count, "unset_claims should be deleted on block cascade")

	require.NoError(t, database.QueryRowContext(ctx, `SELECT COUNT(*) FROM set_claim`).Scan(&count))
	require.Equal(t, 0, count, "set_claims should be deleted on block cascade")
}

func TestMigration0001_IndexExists(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	// SQLite stores index info in sqlite_master
	var name string
	err := database.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_claim_type_block'`,
	).Scan(&name)
	require.NoError(t, err)
	require.Equal(t, "idx_claim_type_block", name)
}

func TestMigration0001_ClaimDefaultType(t *testing.T) {
	database := setupTestDB(t)
	ctx := context.Background()

	tx, err := database.BeginTx(ctx, nil)
	require.NoError(t, err)

	_, err = tx.Exec(`INSERT INTO block (num, hash) VALUES (1, '0xBLOCK1')`)
	require.NoError(t, err)

	// Insert a claim without specifying 'type' — should default to ''
	_, err = tx.Exec(`
		INSERT INTO claim (
			block_num, block_pos, global_index,
			origin_network, origin_address, destination_address,
			amount, destination_network
		) VALUES (1, 0, '1', 0, '0x0', '0x0', '0', 0)
	`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	var claimType string
	require.NoError(t, database.QueryRowContext(ctx, `SELECT type FROM claim WHERE block_num = 1`).Scan(&claimType))
	require.Equal(t, "", claimType, "default type should be empty string")
}

func TestMigration0001_Idempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "idempotent.sqlite")
	lg := logger.WithFields("module", "test")

	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Run migrations twice — should not error
	require.NoError(t, RunMigrations(lg, database))
	require.NoError(t, RunMigrations(lg, database))
}
