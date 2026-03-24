package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/agglayer/aggkit/claimsync/storage/migrations"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
)

// requiredBridgeTables are the bridgesync tables that must all exist for the import to proceed.
var requiredBridgeTables = []string{"block", "claim", "set_claim", "unset_claim"}

// requiredBridgeMigration is the ID of the last bridgesync migration that modifies the
// schema of any of the tables listed in requiredBridgeTables.
// The bridge DB must have applied at least this migration before we can safely import.
//   - bridgesync0012 - ALTER TABLE claim ADD COLUMN type
const requiredBridgeMigration = "bridgesync0012"

// ImportDataFromBridgesyncer copies block, claim, set_claim and unset_claim data from a
// bridgesync SQLite database (bridgeDBFilename) into the claimsync SQLite database
// (claimDBFilename), creating and migrating it if it does not yet exist.
//
// Return values:
//   - (false, nil)  - nothing to migrate: bridge DB not found, claimDB already exists,
//     or bridge DB has no claim data. The claimDB is not created.
//   - (true, nil)   - migration completed successfully.
//   - (true, error) - migration was needed but failed (e.g. missing required bridge
//     migration, DB I/O error). The claimDB may be left in a partial state.
//
// Column-level differences between bridge schema versions are handled automatically:
//   - block.hash              - present since bridgesync migration 0003; defaults to ”.
//   - claim.tx_hash           - present since bridgesync migration 0002; defaults to ”.
//   - claim.block_timestamp   - present since bridgesync migration 0002; defaults to 0.
//   - claim.type              - present since bridgesync migration 0012; defaults to ”.
func ImportDataFromBridgesyncer(ctx context.Context,
	logger aggkitcommon.Logger,
	bridgeDBFilename string,
	claimDBFilename string) (bool, error) {
	if logger == nil {
		logger = log.WithFields("module", "ImportDataFromBridgesyncer")
	}

	// Skip import if the bridge DB does not exist yet.
	if _, err := os.Stat(bridgeDBFilename); os.IsNotExist(err) {
		logger.Infof("bridge DB not found - skipping import")
		return false, nil
	}

	// Skip import if the claim DB already exists (import was already performed).
	if _, err := os.Stat(claimDBFilename); err == nil {
		logger.Infof("claim DB already exists - skipping import")
		return false, nil
	}

	// Phase 1 - inspect the bridge DB without touching the claim DB.
	hasData, err := bridgeHasClaimData(ctx, bridgeDBFilename)
	if err != nil {
		return true, fmt.Errorf("ImportDataFromBridgesyncer: failed to inspect bridge DB: %w", err)
	}
	if !hasData {
		logger.Infof("no claim data found in bridge DB - skipping import")
		return false, nil
	}

	// Phase 2 - open / create the claim DB and run migrations.
	claimDB, err := db.NewSQLiteDB(claimDBFilename)
	if err != nil {
		return true, fmt.Errorf("ImportDataFromBridgesyncer: failed to open claim DB: %w", err)
	}
	defer claimDB.Close()

	if err := migrations.RunMigrations(logger, claimDB); err != nil {
		return true, fmt.Errorf("ImportDataFromBridgesyncer: failed to run claim DB migrations: %w", err)
	}

	// Use a single connection so that ATTACH and the subsequent transaction share the
	// same SQLite connection (ATTACH is per-connection in SQLite).
	conn, err := claimDB.Conn(ctx)
	if err != nil {
		return true, fmt.Errorf("ImportDataFromBridgesyncer: failed to acquire DB connection: %w", err)
	}
	defer conn.Close()

	// ATTACH the bridge DB so we can SELECT from it in the same query.
	attachSQL := fmt.Sprintf(`ATTACH DATABASE 'file:%s' AS bridge`, bridgeDBFilename)
	if _, err := conn.ExecContext(ctx, attachSQL); err != nil {
		return true, fmt.Errorf("ImportDataFromBridgesyncer: failed to attach bridge DB: %w", err)
	}
	defer conn.ExecContext(ctx, `DETACH DATABASE bridge`) //nolint:errcheck

	hasBlockHash, err := bridgeColumnExists(ctx, conn, "block", "hash")
	if err != nil {
		return true, err
	}
	hasClaimTxHash, err := bridgeColumnExists(ctx, conn, "claim", "tx_hash")
	if err != nil {
		return true, err
	}
	hasClaimBlockTimestamp, err := bridgeColumnExists(ctx, conn, "claim", "block_timestamp")
	if err != nil {
		return true, err
	}
	hasClaimType, err := bridgeColumnExists(ctx, conn, "claim", "type")
	if err != nil {
		return true, err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return true, fmt.Errorf("ImportDataFromBridgesyncer: failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	blocksImported, err := importBlocks(tx, hasBlockHash)
	if err != nil {
		return true, err
	}
	claimsImported, err := importClaims(tx, hasClaimTxHash, hasClaimBlockTimestamp, hasClaimType)
	if err != nil {
		return true, err
	}
	unsetClaimsImported, err := importUnsetClaims(tx)
	if err != nil {
		return true, err
	}
	setClaimsImported, err := importSetClaims(tx)
	if err != nil {
		return true, err
	}

	if err := tx.Commit(); err != nil {
		return true, fmt.Errorf("ImportDataFromBridgesyncer: failed to commit transaction: %w", err)
	}

	logger.Infof("import from bridgesyncer complete: blocks=%d claims=%d set_claims=%d unset_claims=%d",
		blocksImported, claimsImported, setClaimsImported, unsetClaimsImported)
	return true, nil
}

// bridgeHasClaimData opens bridgeDBFilename directly, checks that all required tables
// exist, and returns true if any of claim/set_claim/unset_claim contain at least one row.
func bridgeHasClaimData(ctx context.Context, bridgeDBFilename string) (bool, error) {
	bdb, err := db.NewSQLiteDB(bridgeDBFilename)
	if err != nil {
		return false, fmt.Errorf("bridgeHasClaimData: failed to open bridge DB: %w", err)
	}
	defer bdb.Close()

	conn, err := bdb.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("bridgeHasClaimData: failed to acquire connection: %w", err)
	}
	defer conn.Close()

	// Re-use the existing helper but against the main schema of the bridge DB.
	present, err := checkBridgeTablesOnConn(ctx, conn)
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}

	if err := checkBridgeMigration(ctx, conn); err != nil {
		return false, err
	}

	var count int
	err = conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT 1 FROM (SELECT 1 FROM claim       LIMIT 1)
			UNION ALL
			SELECT 1 FROM (SELECT 1 FROM set_claim   LIMIT 1)
			UNION ALL
			SELECT 1 FROM (SELECT 1 FROM unset_claim LIMIT 1)
		)`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("bridgeHasClaimData: failed to count claim rows: %w", err)
	}
	return count > 0, nil
}

// checkBridgeMigration returns an error if requiredBridgeMigration has not been applied
// to the bridge DB, which means its schema may be incomplete for a safe import.
func checkBridgeMigration(ctx context.Context, conn *sql.Conn) error {
	var count int
	err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM gorp_migrations WHERE id = $1`, requiredBridgeMigration).
		Scan(&count)
	if err != nil {
		return fmt.Errorf("checkBridgeMigration: failed to query gorp_migrations: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("checkBridgeMigration: bridge DB has not applied required migration %q", requiredBridgeMigration)
	}
	return nil
}

// checkBridgeTablesOnConn returns true only when all requiredBridgeTables exist in the
// main schema of the given connection.
func checkBridgeTablesOnConn(ctx context.Context, conn *sql.Conn) (bool, error) {
	placeholders := make([]string, len(requiredBridgeTables))
	args := make([]any, len(requiredBridgeTables))
	for i, name := range requiredBridgeTables {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = name
	}
	query := fmt.Sprintf( //nolint:gosec // placeholders contain only "$N" positional markers, no user input
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN (%s)`,
		strings.Join(placeholders, ","),
	)
	var count int
	if err := conn.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, fmt.Errorf("checkBridgeTablesOnConn: %w", err)
	}
	return count == len(requiredBridgeTables), nil
}

// bridgeColumnExists reports whether the given column exists in the named table of the
// attached 'bridge' schema by inspecting PRAGMA table_info.
func bridgeColumnExists(ctx context.Context, conn *sql.Conn, tableName, columnName string) (bool, error) {
	rows, err := conn.QueryContext(ctx, fmt.Sprintf(`PRAGMA bridge.table_info(%s)`, tableName))
	if err != nil {
		return false, fmt.Errorf("bridgeColumnExists: PRAGMA table_info(%s): %w", tableName, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return false, fmt.Errorf("bridgeColumnExists: scan table_info(%s): %w", tableName, err)
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, rows.Err()
}

func importBlocks(tx *sql.Tx, hasHash bool) (int64, error) {
	hashExpr := "''"
	if hasHash {
		hashExpr = "COALESCE(hash, '')"
	}
	result, err := tx.Exec(fmt.Sprintf(
		`INSERT OR IGNORE INTO main.block (num, hash) SELECT num, %s FROM bridge.block`,
		hashExpr,
	))
	if err != nil {
		return 0, fmt.Errorf("ImportDataFromBridgesyncer: failed to import blocks: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

func importClaims(tx *sql.Tx, hasTxHash, hasBlockTimestamp, hasType bool) (int64, error) {
	txHashExpr := "''"
	if hasTxHash {
		txHashExpr = "COALESCE(tx_hash, '')"
	}
	blockTimestampExpr := "0"
	if hasBlockTimestamp {
		blockTimestampExpr = "COALESCE(block_timestamp, 0)"
	}
	typeExpr := "''"
	if hasType {
		typeExpr = "COALESCE(type, '')"
	}
	result, err := tx.Exec(fmt.Sprintf(`
		INSERT OR IGNORE INTO main.claim (
			block_num, block_pos, tx_hash, global_index,
			origin_network, origin_address, destination_address, amount,
			proof_local_exit_root, proof_rollup_exit_root,
			mainnet_exit_root, rollup_exit_root, global_exit_root,
			destination_network, metadata, is_message, block_timestamp, type
		)
		SELECT
			block_num, block_pos, %s, global_index,
			origin_network, origin_address, destination_address, amount,
			proof_local_exit_root, proof_rollup_exit_root,
			mainnet_exit_root, rollup_exit_root, global_exit_root,
			destination_network, metadata, is_message, %s, %s
		FROM bridge.claim`, txHashExpr, blockTimestampExpr, typeExpr))
	if err != nil {
		return 0, fmt.Errorf("ImportDataFromBridgesyncer: failed to import claims: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

func importUnsetClaims(tx *sql.Tx) (int64, error) {
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO main.unset_claim
			(block_num, block_pos, tx_hash, global_index, unset_global_index_hash_chain, created_at)
		SELECT block_num, block_pos, tx_hash, global_index, unset_global_index_hash_chain, created_at
		FROM bridge.unset_claim`)
	if err != nil {
		return 0, fmt.Errorf("ImportDataFromBridgesyncer: failed to import unset_claims: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

func importSetClaims(tx *sql.Tx) (int64, error) {
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO main.set_claim
			(block_num, block_pos, tx_hash, global_index, created_at)
		SELECT block_num, block_pos, tx_hash, global_index, created_at
		FROM bridge.set_claim`)
	if err != nil {
		return 0, fmt.Errorf("ImportDataFromBridgesyncer: failed to import set_claims: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// ImportKeyValueFromBridgesyncer copies the single key_value row from the bridgesync
// SQLite database (bridgeDBFilename) into the claimsync SQLite database (claimDBFilename),
// replacing the original owner value with the provided owner parameter.
//
// The function is a no-op when the key_value table does not exist in the bridge DB or
// contains no rows. In that case the claimDB is not created at all.
// The import is idempotent: an existing row with the same (owner, key) is silently skipped
// (INSERT OR IGNORE).
func ImportKeyValueFromBridgesyncer(bridgeDBFilename string, claimDBFilename string, owner string) error {
	logger := log.WithFields("module", "ImportKeyValueFromBridgesyncer")
	ctx := context.Background()

	// Phase 1 - read the single key_value row from the bridge DB without touching the claim DB.
	row, err := readBridgeKeyValueRow(ctx, bridgeDBFilename)
	if err != nil {
		return fmt.Errorf("ImportKeyValueFromBridgesyncer: failed to read bridge key_value: %w", err)
	}
	if row == nil {
		logger.Infof("no key_value data found in bridge DB - skipping import")
		return nil
	}

	// Phase 2 - open / create the claim DB, run migrations and insert the row.
	claimDB, err := db.NewSQLiteDB(claimDBFilename)
	if err != nil {
		return fmt.Errorf("ImportKeyValueFromBridgesyncer: failed to open claim DB: %w", err)
	}
	defer claimDB.Close()

	if err := migrations.RunMigrations(logger, claimDB); err != nil {
		return fmt.Errorf("ImportKeyValueFromBridgesyncer: failed to run claim DB migrations: %w", err)
	}

	_, err = claimDB.ExecContext(ctx, `
		INSERT OR IGNORE INTO key_value (owner, key, value, updated_at)
		VALUES ($1, $2, $3, $4)`,
		owner, row.key, row.value, row.updatedAt)
	if err != nil {
		return fmt.Errorf("ImportKeyValueFromBridgesyncer: failed to insert key_value row: %w", err)
	}

	logger.Infof("key_value import from bridgesyncer complete (owner=%s key=%s)", owner, row.key)
	return nil
}

// keyValueRow holds the fields of a key_value table row.
type keyValueRow struct {
	key       string
	value     string
	updatedAt int64
}

// readBridgeKeyValueRow opens bridgeDBFilename and returns the single key_value row, or
// nil if the table does not exist or is empty.
func readBridgeKeyValueRow(ctx context.Context, bridgeDBFilename string) (*keyValueRow, error) {
	bdb, err := db.NewSQLiteDB(bridgeDBFilename)
	if err != nil {
		return nil, fmt.Errorf("readBridgeKeyValueRow: failed to open bridge DB: %w", err)
	}
	defer bdb.Close()

	// Check that the key_value table exists.
	var tableCount int
	err = bdb.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='key_value'`).
		Scan(&tableCount)
	if err != nil {
		return nil, fmt.Errorf("readBridgeKeyValueRow: failed to check key_value table: %w", err)
	}
	if tableCount == 0 {
		return nil, nil
	}

	row := &keyValueRow{}
	err = bdb.QueryRowContext(ctx, `SELECT key, value, updated_at FROM key_value LIMIT 1`).
		Scan(&row.key, &row.value, &row.updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("readBridgeKeyValueRow: failed to read row: %w", err)
	}
	return row, nil
}
