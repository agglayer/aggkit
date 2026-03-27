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

// BridgeSyncerStatus holds the read-only inspection results produced by
// InspectBridgeSyncer. Each field is only meaningful when the fields it depends
// on are true (see field comments).
type BridgeSyncerStatus struct {
	// BridgeDBExists reports whether the bridgesync database file exists on disk.
	BridgeDBExists bool
	// ClaimDBExists reports whether the claimsync database file already exists on disk.
	ClaimDBExists bool
	// MigrationOK reports whether requiredBridgeMigration has been applied to the
	// bridge DB. Only meaningful when BridgeDBExists is true.
	MigrationOK bool
	// HasClaimData reports whether any of the claim, set_claim or unset_claim tables
	// contain at least one row. Only meaningful when BridgeDBExists is true and the
	// required tables are present.
	HasClaimData bool
}

// String returns a human-readable summary of the status.
func (s BridgeSyncerStatus) String() string {
	return fmt.Sprintf(
		"BridgeDBExists=%t ClaimDBExists=%t MigrationOK=%t HasClaimData=%t ShouldMigrate=%t",
		s.BridgeDBExists, s.ClaimDBExists, s.MigrationOK, s.HasClaimData, s.ShouldMigrate(),
	)
}

// Validate returns an error when the status indicates a blocking condition that
// requires user intervention. Specifically, it errors when the bridge DB contains
// claim data but the required bridge migration has not been applied, meaning the
// node must first be upgraded to an intermediate version that runs that migration.
func (s BridgeSyncerStatus) Validate() error {
	if s.BridgeDBExists && !s.ClaimDBExists && s.HasClaimData && !s.MigrationOK {
		return fmt.Errorf(
			"bridge DB contains claim data but required migration %q has not been applied; "+
				"upgrade to the intermediate version first",
			requiredBridgeMigration,
		)
	}
	return nil
}

// ShouldMigrate reports whether a data migration from the bridgesync DB into the
// claimsync DB should be performed. It returns true only when all of the following
// conditions hold:
//   - the bridge DB exists on disk,
//   - the claim DB does not yet exist (migration not already done),
//   - the required bridge migration has been applied, and
//   - the bridge DB contains claim data worth copying.
func (s BridgeSyncerStatus) ShouldMigrate() bool {
	return s.BridgeDBExists && !s.ClaimDBExists && s.MigrationOK && s.HasClaimData
}

// InspectBridgeSyncer performs a read-only inspection of the bridge and claim
// database files and returns a BridgeSyncerStatus summary. It never writes to
// either database.
func InspectBridgeSyncer(ctx context.Context, bridgeDBFilename, claimDBFilename string) (BridgeSyncerStatus, error) {
	var status BridgeSyncerStatus

	if _, err := os.Stat(bridgeDBFilename); err == nil {
		status.BridgeDBExists = true
	}
	if _, err := os.Stat(claimDBFilename); err == nil {
		status.ClaimDBExists = true
	}

	if !status.BridgeDBExists {
		return status, nil
	}

	bdb, err := db.NewSQLiteDB(bridgeDBFilename)
	if err != nil {
		return status, fmt.Errorf("InspectBridgeSyncer: failed to open bridge DB: %w", err)
	}
	defer bdb.Close()

	conn, err := bdb.Conn(ctx)
	if err != nil {
		return status, fmt.Errorf("InspectBridgeSyncer: failed to acquire connection: %w", err)
	}
	defer conn.Close()

	// Check that the required tables exist before querying anything else.
	present, err := checkBridgeTablesOnConn(ctx, conn)
	if err != nil {
		return status, fmt.Errorf("InspectBridgeSyncer: failed to check bridge tables: %w", err)
	}
	if !present {
		return status, nil
	}

	// Check whether the required migration has been applied.
	// A missing gorp_migrations table is treated as MigrationOK = false, not an error.
	// Any other failure (corruption, permissions, …) is surfaced so it is not silently masked.
	var migCount int
	err = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM gorp_migrations WHERE id = $1`, requiredBridgeMigration).Scan(&migCount)
	if err != nil {
		errMsg := strings.ToLower(err.Error())
		if !strings.Contains(errMsg, "no such table") || !strings.Contains(errMsg, "gorp_migrations") {
			return status, fmt.Errorf("InspectBridgeSyncer: failed to query gorp_migrations: %w", err)
		}
	} else {
		status.MigrationOK = migCount > 0
	}

	// Always check for claim data, even when the migration is missing, so that
	// Validate() can distinguish a harmless empty-DB case from a blocking one.
	// Check whether any claim-related tables contain data.
	var rowCount int
	err = conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT 1 FROM (SELECT 1 FROM claim       LIMIT 1)
			UNION ALL
			SELECT 1 FROM (SELECT 1 FROM set_claim   LIMIT 1)
			UNION ALL
			SELECT 1 FROM (SELECT 1 FROM unset_claim LIMIT 1)
		)`).Scan(&rowCount)
	if err != nil {
		return status, fmt.Errorf("InspectBridgeSyncer: failed to count claim rows: %w", err)
	}
	status.HasClaimData = rowCount > 0

	return status, nil
}

// ImportDataFromBridgesyncer copies block, claim, set_claim and unset_claim data from a
// bridgesync SQLite database (bridgeDBFilename) into the claimsync SQLite database
// (claimDBFilename), creating and migrating it if it does not yet exist.
//
// The caller is responsible for deciding whether a migration is needed (e.g. via
// InspectBridgeSyncer and BridgeSyncerStatus.ShouldMigrate) before calling this
// function. No precondition checks are performed here.
//
// The import is atomic: data is written to a temporary file first and only renamed
// to claimDBFilename on success, so a crash mid-import leaves claimDBFilename absent
// and the migration will be retried on the next startup.
//
// Column-level differences between bridge schema versions are handled automatically:
//   - block.hash            - present since bridgesync migration 0003; defaults to ''.
//   - claim.tx_hash         - present since bridgesync migration 0002; defaults to ''.
//   - claim.block_timestamp - present since bridgesync migration 0002; defaults to 0.
//   - claim.type            - present since bridgesync migration 0012; defaults to ''.
func ImportDataFromBridgesyncer(ctx context.Context,
	logger aggkitcommon.Logger,
	bridgeDBFilename string,
	claimDBFilename string) error {
	if logger == nil {
		logger = log.WithFields("module", "ImportDataFromBridgesyncer")
	}

	tmpFilename := claimDBFilename + ".import.tmp"
	// Remove any leftover tmp file from a previous failed attempt.
	os.Remove(tmpFilename)

	// All DB work happens on tmpFilename. The defers inside importDataToTmpFile
	// guarantee the DB/connection/transaction are fully closed before we return,
	// so the subsequent Rename is safe even on platforms that lock open files.
	blocksImported, claimsImported, setClaimsImported, unsetClaimsImported, err :=
		importDataToTmpFile(ctx, logger, bridgeDBFilename, tmpFilename)
	if err != nil {
		os.Remove(tmpFilename)
		return err
	}

	if err := os.Rename(tmpFilename, claimDBFilename); err != nil {
		os.Remove(tmpFilename)
		return fmt.Errorf("ImportDataFromBridgesyncer: failed to promote tmp DB: %w", err)
	}

	logger.Infof("import from bridgesyncer complete: blocks=%d claims=%d set_claims=%d unset_claims=%d",
		blocksImported, claimsImported, setClaimsImported, unsetClaimsImported)
	return nil
}

// importDataToTmpFile performs the actual copy from bridgeDBFilename into destFilename.
// It is a pure helper for ImportDataFromBridgesyncer: all deferred closes run when this
// function returns, ensuring the file is fully closed before the caller renames it.
func importDataToTmpFile(ctx context.Context,
	logger aggkitcommon.Logger,
	bridgeDBFilename string,
	destFilename string) (blocksImported, claimsImported, setClaimsImported, unsetClaimsImported int64, err error) {
	claimDB, err := db.NewSQLiteDB(destFilename)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("ImportDataFromBridgesyncer: failed to open claim DB: %w", err)
	}
	defer claimDB.Close()

	if err := migrations.RunMigrations(logger, claimDB); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("ImportDataFromBridgesyncer: failed to run claim DB migrations: %w", err)
	}

	// Use a single connection so that ATTACH and the subsequent transaction share the
	// same SQLite connection (ATTACH is per-connection in SQLite).
	conn, err := claimDB.Conn(ctx)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("ImportDataFromBridgesyncer: failed to acquire DB connection: %w", err)
	}
	defer conn.Close()

	// ATTACH the bridge DB so we can SELECT from it in the same query.
	// The three characters that act as URI delimiters inside a SQLite file URI path
	// ('%', '?', '#') are percent-encoded so they cannot be mistaken for query
	// separators or fragment identifiers.  The full URI is then passed as a bound
	// parameter (not interpolated into SQL) to eliminate any injection risk.
	escapedPath := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(bridgeDBFilename)
	attachURI := "file:" + escapedPath + "?mode=ro"
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS bridge`, attachURI); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("ImportDataFromBridgesyncer: failed to attach bridge DB: %w", err)
	}
	defer conn.ExecContext(ctx, `DETACH DATABASE bridge`) //nolint:errcheck

	hasBlockHash, err := bridgeColumnExists(ctx, conn, "block", "hash")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	hasClaimTxHash, err := bridgeColumnExists(ctx, conn, "claim", "tx_hash")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	hasClaimBlockTimestamp, err := bridgeColumnExists(ctx, conn, "claim", "block_timestamp")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	hasClaimType, err := bridgeColumnExists(ctx, conn, "claim", "type")
	if err != nil {
		return 0, 0, 0, 0, err
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("ImportDataFromBridgesyncer: failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	blocksImported, err = importBlocks(tx, hasBlockHash)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	claimsImported, err = importClaims(tx, hasClaimTxHash, hasClaimBlockTimestamp, hasClaimType)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	unsetClaimsImported, err = importUnsetClaims(tx)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	setClaimsImported, err = importSetClaims(tx)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("ImportDataFromBridgesyncer: failed to commit transaction: %w", err)
	}

	return blocksImported, claimsImported, setClaimsImported, unsetClaimsImported, nil
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
func ImportKeyValueFromBridgesyncer(
	ctx context.Context, bridgeDBFilename string, claimDBFilename string, owner string) error {
	logger := log.WithFields("module", "ImportKeyValueFromBridgesyncer")

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

	const compatibilityKey = "compatibility_content"

	var count int
	err = bdb.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM key_value WHERE key = $1`, compatibilityKey).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("readBridgeKeyValueRow: failed to count key_value rows: %w", err)
	}
	if count != 1 {
		return nil, fmt.Errorf("readBridgeKeyValueRow: expected exactly 1 row with key=%q, got %d", compatibilityKey, count)
	}

	row := &keyValueRow{}
	err = bdb.QueryRowContext(ctx,
		`SELECT key, value, updated_at FROM key_value WHERE key = $1`, compatibilityKey).
		Scan(&row.key, &row.value, &row.updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("readBridgeKeyValueRow: failed to read row: %w", err)
	}
	return row, nil
}
