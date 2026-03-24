package storage

import (
	"context"
	"database/sql"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/db"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// bridgeDB helpers ---------------------------------------------------------------

// newBridgeDB creates a minimal bridgesync-like SQLite database at path and returns
// the open *sql.DB.  The schema matches bridgesync after all migrations have run
// (block.hash, claim.type, set_claim, unset_claim all present).
func newBridgeDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	bdb, err := db.NewSQLiteDB(path)
	require.NoError(t, err)
	t.Cleanup(func() { bdb.Close() })

	_, err = bdb.Exec(`
		CREATE TABLE gorp_migrations (
			id         VARCHAR(255) NOT NULL PRIMARY KEY,
			applied_at DATETIME
		);
		INSERT INTO gorp_migrations (id, applied_at) VALUES ('` + requiredBridgeMigration + `', strftime('%s','now'));
		CREATE TABLE block (
			num  BIGINT PRIMARY KEY,
			hash VARCHAR
		);
		CREATE TABLE claim (
			block_num               INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
			block_pos               INTEGER NOT NULL,
			tx_hash                 VARCHAR,
			global_index            TEXT NOT NULL,
			origin_network          INTEGER NOT NULL,
			origin_address          VARCHAR NOT NULL,
			destination_address     VARCHAR NOT NULL,
			amount                  TEXT NOT NULL,
			proof_local_exit_root   VARCHAR,
			proof_rollup_exit_root  VARCHAR,
			mainnet_exit_root       VARCHAR,
			rollup_exit_root        VARCHAR,
			global_exit_root        VARCHAR,
			destination_network     INTEGER NOT NULL,
			metadata                BLOB,
			is_message              BOOLEAN,
			block_timestamp         INTEGER,
			type                    TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (block_num, block_pos)
		);
		CREATE TABLE unset_claim (
			block_num                     INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
			block_pos                     INTEGER NOT NULL,
			tx_hash                       VARCHAR NOT NULL,
			global_index                  TEXT NOT NULL,
			unset_global_index_hash_chain VARCHAR NOT NULL,
			created_at                    INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
			PRIMARY KEY (block_num, block_pos)
		);
		CREATE TABLE set_claim (
			block_num    INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
			block_pos    INTEGER NOT NULL,
			tx_hash      VARCHAR NOT NULL,
			global_index TEXT NOT NULL,
			created_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
			PRIMARY KEY (block_num, block_pos)
		);
	`)
	require.NoError(t, err)
	return bdb
}

// insertBridgeBlock inserts a row into bridge.block.
func insertBridgeBlock(t *testing.T, bdb *sql.DB, num uint64, hash string) {
	t.Helper()
	_, err := bdb.Exec(`INSERT INTO block (num, hash) VALUES (?, ?)`, num, hash)
	require.NoError(t, err)
}

// insertBridgeClaim inserts a minimal row into bridge.claim.
func insertBridgeClaim(t *testing.T, bdb *sql.DB, blockNum uint64, blockPos uint64, globalIndex string) {
	t.Helper()
	_, err := bdb.Exec(`
		INSERT INTO claim
			(block_num, block_pos, global_index, origin_network, origin_address,
			 destination_address, amount, destination_network)
		VALUES (?, ?, ?, 1, '0x1111', '0x2222', '100', 2)`,
		blockNum, blockPos, globalIndex)
	require.NoError(t, err)
}

// insertBridgeSetClaim inserts a row into bridge.set_claim.
func insertBridgeSetClaim(t *testing.T, bdb *sql.DB, blockNum uint64, blockPos uint64, globalIndex string) {
	t.Helper()
	_, err := bdb.Exec(`
		INSERT INTO set_claim (block_num, block_pos, tx_hash, global_index)
		VALUES (?, ?, '0xabcd', ?)`, blockNum, blockPos, globalIndex)
	require.NoError(t, err)
}

// insertBridgeUnsetClaim inserts a row into bridge.unset_claim.
func insertBridgeUnsetClaim(t *testing.T, bdb *sql.DB, blockNum uint64, blockPos uint64, globalIndex string) {
	t.Helper()
	_, err := bdb.Exec(`
		INSERT INTO unset_claim
			(block_num, block_pos, tx_hash, global_index, unset_global_index_hash_chain)
		VALUES (?, ?, '0xdead', ?, '0xbeef')`, blockNum, blockPos, globalIndex)
	require.NoError(t, err)
}

// count helpers

func countRows(t *testing.T, claimPath, table string) int {
	t.Helper()
	d, err := db.NewSQLiteDB(claimPath)
	require.NoError(t, err)
	defer d.Close()
	var n int
	require.NoError(t, d.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&n))
	return n
}

// ---------------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------------

func TestImportDataFromBridgesyncer_Success(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge.db")
	claimPath := filepath.Join(dir, "claim.db")

	bdb := newBridgeDB(t, bridgePath)
	insertBridgeBlock(t, bdb, 10, common.HexToHash("0xaabb").Hex())
	insertBridgeClaim(t, bdb, 10, 0, big.NewInt(1).String())
	insertBridgeClaim(t, bdb, 10, 1, big.NewInt(2).String())
	insertBridgeSetClaim(t, bdb, 10, 2, big.NewInt(3).String())
	insertBridgeUnsetClaim(t, bdb, 10, 3, big.NewInt(4).String())
	bdb.Close()

	require.NoError(t, ImportDataFromBridgesyncer(context.Background(), nil, bridgePath, claimPath))

	require.Equal(t, 1, countRows(t, claimPath, "block"))
	require.Equal(t, 2, countRows(t, claimPath, "claim"))
	require.Equal(t, 1, countRows(t, claimPath, "set_claim"))
	require.Equal(t, 1, countRows(t, claimPath, "unset_claim"))
}

func TestImportDataFromBridgesyncer_Idempotent(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge.db")
	claimPath := filepath.Join(dir, "claim.db")

	bdb := newBridgeDB(t, bridgePath)
	insertBridgeBlock(t, bdb, 5, common.HexToHash("0x1234").Hex())
	insertBridgeClaim(t, bdb, 5, 0, big.NewInt(99).String())
	bdb.Close()

	require.NoError(t, ImportDataFromBridgesyncer(context.Background(), nil, bridgePath, claimPath))
	// Second call must succeed and not duplicate rows.
	require.NoError(t, ImportDataFromBridgesyncer(context.Background(), nil, bridgePath, claimPath))

	require.Equal(t, 1, countRows(t, claimPath, "block"))
	require.Equal(t, 1, countRows(t, claimPath, "claim"))
}

// ── BridgeSyncerStatus.ShouldMigrate ─────────────────────────────────────────

func TestBridgeSyncerStatus_ShouldMigrate(t *testing.T) {
	tests := []struct {
		name   string
		status BridgeSyncerStatus
		want   bool
	}{
		{
			name:   "all conditions met",
			status: BridgeSyncerStatus{BridgeDBExists: true, ClaimDBExists: false, MigrationOK: true, HasClaimData: true},
			want:   true,
		},
		{
			name:   "bridge DB missing",
			status: BridgeSyncerStatus{BridgeDBExists: false, ClaimDBExists: false, MigrationOK: true, HasClaimData: true},
			want:   false,
		},
		{
			name:   "claim DB already exists",
			status: BridgeSyncerStatus{BridgeDBExists: true, ClaimDBExists: true, MigrationOK: true, HasClaimData: true},
			want:   false,
		},
		{
			name:   "migration not applied",
			status: BridgeSyncerStatus{BridgeDBExists: true, ClaimDBExists: false, MigrationOK: false, HasClaimData: true},
			want:   false,
		},
		{
			name:   "no claim data",
			status: BridgeSyncerStatus{BridgeDBExists: true, ClaimDBExists: false, MigrationOK: true, HasClaimData: false},
			want:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.status.ShouldMigrate())
		})
	}
}

// ── BridgeSyncerStatus.Validate ───────────────────────────────────────────────

func TestBridgeSyncerStatus_Validate(t *testing.T) {
	tests := []struct {
		name    string
		status  BridgeSyncerStatus
		wantErr bool
	}{
		{
			name:    "blocking: bridge has data but migration missing",
			status:  BridgeSyncerStatus{BridgeDBExists: true, ClaimDBExists: false, MigrationOK: false, HasClaimData: true},
			wantErr: true,
		},
		{
			name:    "ok: bridge has data and migration applied",
			status:  BridgeSyncerStatus{BridgeDBExists: true, ClaimDBExists: false, MigrationOK: true, HasClaimData: true},
			wantErr: false,
		},
		{
			name:    "ok: bridge DB missing",
			status:  BridgeSyncerStatus{BridgeDBExists: false, ClaimDBExists: false, MigrationOK: false, HasClaimData: false},
			wantErr: false,
		},
		{
			name:    "ok: claim DB already exists (no migration needed)",
			status:  BridgeSyncerStatus{BridgeDBExists: true, ClaimDBExists: true, MigrationOK: false, HasClaimData: true},
			wantErr: false,
		},
		{
			name:    "ok: migration missing but no claim data",
			status:  BridgeSyncerStatus{BridgeDBExists: true, ClaimDBExists: false, MigrationOK: false, HasClaimData: false},
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.status.Validate()
			if tc.wantErr {
				require.ErrorContains(t, err, requiredBridgeMigration)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ── InspectBridgeSyncer ───────────────────────────────────────────────────────

func TestInspectBridgeSyncer_BridgeDBNotExist(t *testing.T) {
	dir := t.TempDir()
	status, err := InspectBridgeSyncer(context.Background(),
		filepath.Join(dir, "bridge.db"), filepath.Join(dir, "claim.db"))
	require.NoError(t, err)
	require.False(t, status.BridgeDBExists)
	require.False(t, status.ClaimDBExists)
	require.False(t, status.MigrationOK)
	require.False(t, status.HasClaimData)
}

func TestInspectBridgeSyncer_ClaimDBExists(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge.db")
	claimPath := filepath.Join(dir, "claim.db")

	bdb := newBridgeDB(t, bridgePath)
	bdb.Close()
	f, err := os.Create(claimPath)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	status, err := InspectBridgeSyncer(context.Background(), bridgePath, claimPath)
	require.NoError(t, err)
	require.True(t, status.BridgeDBExists)
	require.True(t, status.ClaimDBExists)
}

func TestInspectBridgeSyncer_MigrationMissing(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge.db")

	bdb := newBridgeDB(t, bridgePath)
	_, err := bdb.Exec(`DELETE FROM gorp_migrations WHERE id = ?`, requiredBridgeMigration)
	require.NoError(t, err)
	insertBridgeBlock(t, bdb, 1, common.HexToHash("0x01").Hex())
	insertBridgeClaim(t, bdb, 1, 0, big.NewInt(1).String())
	bdb.Close()

	status, err := InspectBridgeSyncer(context.Background(), bridgePath, filepath.Join(dir, "claim.db"))
	require.NoError(t, err)
	require.True(t, status.BridgeDBExists)
	require.False(t, status.MigrationOK)
	// HasClaimData is populated even when the migration is missing so that
	// Validate() can distinguish the blocking case from a harmless empty DB.
	require.True(t, status.HasClaimData)
	require.ErrorContains(t, status.Validate(), requiredBridgeMigration)
}

func TestInspectBridgeSyncer_NoClaimData(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge.db")

	bdb := newBridgeDB(t, bridgePath)
	insertBridgeBlock(t, bdb, 1, common.HexToHash("0xdeadbeef").Hex())
	bdb.Close()

	status, err := InspectBridgeSyncer(context.Background(), bridgePath, filepath.Join(dir, "claim.db"))
	require.NoError(t, err)
	require.True(t, status.BridgeDBExists)
	require.True(t, status.MigrationOK)
	require.False(t, status.HasClaimData)
}

func TestInspectBridgeSyncer_WithClaimData(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge.db")

	bdb := newBridgeDB(t, bridgePath)
	insertBridgeBlock(t, bdb, 1, common.HexToHash("0xaabb").Hex())
	insertBridgeClaim(t, bdb, 1, 0, big.NewInt(42).String())
	bdb.Close()

	status, err := InspectBridgeSyncer(context.Background(), bridgePath, filepath.Join(dir, "claim.db"))
	require.NoError(t, err)
	require.True(t, status.BridgeDBExists)
	require.False(t, status.ClaimDBExists)
	require.True(t, status.MigrationOK)
	require.True(t, status.HasClaimData)
}

// ── ImportKeyValueFromBridgesyncer ────────────────────────────────────────────

func TestImportKeyValueFromBridgesyncer_NoTable(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge.db")
	claimPath := filepath.Join(dir, "claim.db")

	// Bridge DB exists but has no key_value table.
	bdb, err := db.NewSQLiteDB(bridgePath)
	require.NoError(t, err)
	require.NoError(t, bdb.Ping())
	bdb.Close()

	require.NoError(t, ImportKeyValueFromBridgesyncer(bridgePath, claimPath, "my-owner"))

	// Claim DB must not have been created.
	_, statErr := os.Stat(claimPath)
	require.True(t, os.IsNotExist(statErr))
}

func TestImportKeyValueFromBridgesyncer_EmptyTable(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge.db")
	claimPath := filepath.Join(dir, "claim.db")

	bdb, err := db.NewSQLiteDB(bridgePath)
	require.NoError(t, err)
	_, err = bdb.Exec(`CREATE TABLE key_value (
		owner VARCHAR NOT NULL, key VARCHAR NOT NULL,
		value VARCHAR, updated_at INTEGER NOT NULL,
		PRIMARY KEY (key, owner))`)
	require.NoError(t, err)
	bdb.Close()

	require.NoError(t, ImportKeyValueFromBridgesyncer(bridgePath, claimPath, "my-owner"))

	_, statErr := os.Stat(claimPath)
	require.True(t, os.IsNotExist(statErr))
}

func TestImportKeyValueFromBridgesyncer_Success(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge.db")
	claimPath := filepath.Join(dir, "claim.db")

	bdb, err := db.NewSQLiteDB(bridgePath)
	require.NoError(t, err)
	_, err = bdb.Exec(`CREATE TABLE key_value (
		owner VARCHAR NOT NULL, key VARCHAR NOT NULL,
		value VARCHAR, updated_at INTEGER NOT NULL,
		PRIMARY KEY (key, owner))`)
	require.NoError(t, err)
	_, err = bdb.Exec(`INSERT INTO key_value (owner, key, value, updated_at) VALUES ('old-owner', 'compat', 'data', 1000)`)
	require.NoError(t, err)
	bdb.Close()

	require.NoError(t, ImportKeyValueFromBridgesyncer(bridgePath, claimPath, "new-owner"))

	cdb, err := db.NewSQLiteDB(claimPath)
	require.NoError(t, err)
	defer cdb.Close()

	var owner, key, value string
	var updatedAt int64
	err = cdb.QueryRow(`SELECT owner, key, value, updated_at FROM key_value LIMIT 1`).
		Scan(&owner, &key, &value, &updatedAt)
	require.NoError(t, err)
	require.Equal(t, "new-owner", owner)
	require.Equal(t, "compat", key)
	require.Equal(t, "data", value)
	require.Equal(t, int64(1000), updatedAt)
}

func TestImportKeyValueFromBridgesyncer_Idempotent(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge.db")
	claimPath := filepath.Join(dir, "claim.db")

	bdb, err := db.NewSQLiteDB(bridgePath)
	require.NoError(t, err)
	_, err = bdb.Exec(`CREATE TABLE key_value (
		owner VARCHAR NOT NULL, key VARCHAR NOT NULL,
		value VARCHAR, updated_at INTEGER NOT NULL,
		PRIMARY KEY (key, owner))`)
	require.NoError(t, err)
	_, err = bdb.Exec(`INSERT INTO key_value (owner, key, value, updated_at) VALUES ('old-owner', 'compat', 'data', 1000)`)
	require.NoError(t, err)
	bdb.Close()

	require.NoError(t, ImportKeyValueFromBridgesyncer(bridgePath, claimPath, "new-owner"))
	// Second call must not fail and must not duplicate the row.
	require.NoError(t, ImportKeyValueFromBridgesyncer(bridgePath, claimPath, "new-owner"))

	require.Equal(t, 1, countRows(t, claimPath, "key_value"))
}

// ── OldSchemaNoHash ───────────────────────────────────────────────────────────

func TestImportDataFromBridgesyncer_OldSchemaNoHash(t *testing.T) {
	dir := t.TempDir()
	bridgePath := filepath.Join(dir, "bridge_old.db")
	claimPath := filepath.Join(dir, "claim.db")

	// Bridge DB without block.hash (pre-migration 0003)
	bdb, err := db.NewSQLiteDB(bridgePath)
	require.NoError(t, err)
	// Simulates bridgesync schema after migration 0001 only:
	// block has no hash, claim has no tx_hash / block_timestamp / type.
	// gorp_migrations must still contain the required migration entry.
	_, err = bdb.Exec(`
		CREATE TABLE gorp_migrations (
			id         VARCHAR(255) NOT NULL PRIMARY KEY,
			applied_at DATETIME
		);
		INSERT INTO gorp_migrations (id, applied_at) VALUES ('` + requiredBridgeMigration + `', strftime('%s','now'));
		CREATE TABLE block (num BIGINT PRIMARY KEY);
		CREATE TABLE claim (
			block_num               INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
			block_pos               INTEGER NOT NULL,
			global_index            TEXT NOT NULL,
			origin_network          INTEGER NOT NULL,
			origin_address          VARCHAR NOT NULL,
			destination_address     VARCHAR NOT NULL,
			amount                  TEXT NOT NULL,
			proof_local_exit_root   VARCHAR,
			proof_rollup_exit_root  VARCHAR,
			mainnet_exit_root       VARCHAR,
			rollup_exit_root        VARCHAR,
			global_exit_root        VARCHAR,
			destination_network     INTEGER NOT NULL,
			metadata                BLOB,
			is_message              BOOLEAN,
			PRIMARY KEY (block_num, block_pos)
		);
		CREATE TABLE set_claim (
			block_num INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
			block_pos INTEGER NOT NULL, tx_hash VARCHAR NOT NULL,
			global_index TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
			PRIMARY KEY (block_num, block_pos)
		);
		CREATE TABLE unset_claim (
			block_num INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
			block_pos INTEGER NOT NULL, tx_hash VARCHAR NOT NULL,
			global_index TEXT NOT NULL,
			unset_global_index_hash_chain VARCHAR NOT NULL,
			created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
			PRIMARY KEY (block_num, block_pos)
		);
	`)
	require.NoError(t, err)
	_, err = bdb.Exec(`INSERT INTO block (num) VALUES (1)`)
	require.NoError(t, err)
	_, err = bdb.Exec(`
		INSERT INTO claim
			(block_num, block_pos, global_index, origin_network, origin_address,
			 destination_address, amount, destination_network)
		VALUES (1, 0, '42', 1, '0xaaaa', '0xbbbb', '50', 2)`)
	require.NoError(t, err)
	bdb.Close()

	err = ImportDataFromBridgesyncer(context.Background(), nil, bridgePath, claimPath)
	require.NoError(t, err)

	// block.hash should default to ''
	cdb, err := db.NewSQLiteDB(claimPath)
	require.NoError(t, err)
	defer cdb.Close()
	var hash string
	require.NoError(t, cdb.QueryRowContext(context.Background(), `SELECT hash FROM block WHERE num = 1`).Scan(&hash))
	require.Equal(t, "", hash)

	require.Equal(t, 1, countRows(t, claimPath, "claim"))
}
