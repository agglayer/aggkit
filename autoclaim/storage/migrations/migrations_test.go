package migrations

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	logger "github.com/agglayer/aggkit/log"
	migrate "github.com/rubenv/sql-migrate"
	"github.com/stretchr/testify/require"
)

func TestGetAutoClaimMigrations(t *testing.T) {
	migrations := GetAutoClaimMigrations()
	require.Len(t, migrations, 2)
	require.Equal(t, "autoclaim0001", migrations[0].ID)
	require.NotEmpty(t, migrations[0].SQL)
	require.Equal(t, "autoclaim0002", migrations[1].ID)
	require.NotEmpty(t, migrations[1].SQL)
}

func TestGetFullMigrations(t *testing.T) {
	full := GetFullMigrations()
	autoclaim := GetAutoClaimMigrations()
	require.NotEmpty(t, full)
	require.True(t, len(full) > len(autoclaim), "full migrations should include base + autoclaim")
	// Last entries should be autoclaim migrations
	last := full[len(full)-len(autoclaim):]
	require.Equal(t, autoclaim, last)
}

// TestAutoClaim0002MigratesExistingRows proves that an autoclaim0001-shaped database with pre-existing
// rows migrates correctly: request keys are recomputed to the source:destination:deposit_count format
// (source defaults to 0 for old rows), child transaction-attempt keys follow, source_network is 0, the
// new LER-cursor table exists, and the new UNIQUE(source_network, destination_network, deposit_count)
// constraint replaces the old origin-network based one.
func TestAutoClaim0002MigratesExistingRows(t *testing.T) {
	log := logger.GetDefaultLogger()
	dbPath := filepath.Join(t.TempDir(), "autoclaim.sqlite")
	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	// Apply base migrations + autoclaim0001 only (simulate a pre-existing autoclaim0001 database).
	require.NoError(t, db.RunMigrationsDBExtended(
		log, database,
		[]dbtypes.Migration{{ID: "autoclaim0001", SQL: autoClaim0001}},
		nil, migrate.Up, db.NoLimitMigrations,
	))

	now := time.Now().UTC()
	// Row 1 uses a non-zero origin_network in its old key to prove the re-key uses the source network
	// (0), not the token origin network.
	insertLegacyRequest(t, database, "5:1101:7", 5, 1101, 7, now)
	insertLegacyRequest(t, database, "0:1101:8", 0, 1101, 8, now)
	insertLegacyAttempt(t, database, "5:1101:7", now)

	// Apply the full migration set; only autoclaim0002 is pending.
	require.NoError(t, RunMigrations(log, database))

	// Request keys recomputed to 0:destination:deposit_count, source_network is 0, new columns default.
	type reqRow struct {
		key          string
		source       uint32
		origin       uint32
		verifyBlock  uint64
		lerIsNull    bool
		leafProofNil bool
	}
	rows, err := database.Query(`
		SELECT request_key, source_network, origin_network, verify_block_num,
			ler IS NULL, leaf_proof_json IS NULL
		FROM autoclaim_request ORDER BY deposit_count`)
	require.NoError(t, err)
	defer rows.Close()

	var got []reqRow
	for rows.Next() {
		var r reqRow
		require.NoError(t, rows.Scan(&r.key, &r.source, &r.origin, &r.verifyBlock, &r.lerIsNull, &r.leafProofNil))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []reqRow{
		{key: "0:1101:7", source: 0, origin: 5, verifyBlock: 0, lerIsNull: true, leafProofNil: true},
		{key: "0:1101:8", source: 0, origin: 0, verifyBlock: 0, lerIsNull: true, leafProofNil: true},
	}, got)

	// The child transaction attempt is re-pointed at the recomputed request key.
	var attemptKey string
	require.NoError(t, database.QueryRow(
		"SELECT request_key FROM autoclaim_transaction_attempt").Scan(&attemptKey))
	require.Equal(t, "0:1101:7", attemptKey)

	// The LER cursor table exists.
	var lerTable string
	require.NoError(t, database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'autoclaim_ler_cursor'").Scan(&lerTable))
	require.Equal(t, "autoclaim_ler_cursor", lerTable)

	// New uniqueness holds: a duplicate (source_network, destination_network, deposit_count) is rejected.
	_, err = database.Exec(insertRequestSQL(),
		"dup", 0, 999, 1101, 7, "detected", "0xdup", int64(1), int64(0), now, now, "{}")
	require.Error(t, err)

	// Old origin-network uniqueness is gone: same (origin_network, destination_network, deposit_count)
	// as row 1 (5, 1101, 7) is accepted because it has a distinct source_network.
	_, err = database.Exec(insertRequestSQL(),
		"2:1101:7", 2, 5, 1101, 7, "detected", "0xok", int64(2), int64(0), now, now, "{}")
	require.NoError(t, err)
}

func insertRequestSQL() string {
	return `INSERT INTO autoclaim_request (
		request_key, source_network, origin_network, destination_network, deposit_count, status,
		bridge_tx_hash, block_num, block_pos, created_at, updated_at, bridge_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func insertLegacyRequest(
	t *testing.T,
	database *sql.DB,
	key string,
	originNetwork, destinationNetwork, depositCount uint32,
	now time.Time,
) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO autoclaim_request (
			request_key, origin_network, destination_network, deposit_count, status,
			bridge_tx_hash, block_num, block_pos, created_at, updated_at, bridge_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		key, originNetwork, destinationNetwork, depositCount, "detected",
		"0xbridge", int64(depositCount), int64(0), now, now, "{}",
	)
	require.NoError(t, err)
}

func insertLegacyAttempt(
	t *testing.T,
	database *sql.DB,
	requestKey string,
	now time.Time,
) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO autoclaim_transaction_attempt (
			request_key, attempt_number, claimer_id, tx_manager_id, claim_tx_hash, status,
			created_at, updated_at, target_bridge_addr, attempt_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		requestKey, int64(1), "claimer", "0xtxm", "0xclaim", "sent",
		now, now, "0xbridgeaddr", "{}",
	)
	require.NoError(t, err)
}
